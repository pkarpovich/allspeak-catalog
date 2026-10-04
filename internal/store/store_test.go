package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pkarpovich/allspeak-catalog/internal/manifest"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	shaD = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	shaE = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func testManifest(defaultSHA string) manifest.Manifest {
	return manifest.Manifest{
		Tracks: []manifest.Track{{
			Label:     "ft.sidon",
			SortOrder: 0,
			IsDefault: true,
			FileRef:   manifest.FileRef{Filename: "film.m4a", Size: 73400320, SHA256: defaultSHA},
		}},
		Subtitle: manifest.FileRef{Filename: "film.srt", Size: 152000, SHA256: shaC},
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(filepath.Join(t.TempDir(), "catalog.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	return st
}

func fixedClock(times ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		t := times[i%len(times)]
		i++
		return t
	}
}

func TestCreateAndGet(t *testing.T) {
	st := newTestStore(t)
	created := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	st.now = fixedClock(created)

	in := Session{ID: "sess-1", Title: "Zelda", Manifest: testManifest(shaA)}
	require.NoError(t, st.Create(context.Background(), in))

	got, err := st.Get(context.Background(), "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", got.ID)
	assert.Equal(t, "Zelda", got.Title)
	assert.Equal(t, 1, got.Revision)
	assert.Equal(t, testManifest(shaA), got.Manifest)
	assert.True(t, created.Equal(got.CreatedAt))
	assert.True(t, created.Equal(got.UpdatedAt))
}

func TestGetNotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.Get(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestCreateDuplicate(t *testing.T) {
	st := newTestStore(t)
	in := Session{ID: "dup", Title: "one", Manifest: testManifest(shaA)}
	require.NoError(t, st.Create(context.Background(), in))

	err := st.Create(context.Background(), Session{ID: "dup", Title: "two", Manifest: testManifest(shaB)})
	assert.ErrorIs(t, err, ErrExists)

	got, err := st.Get(context.Background(), "dup")
	require.NoError(t, err)
	assert.Equal(t, "one", got.Title)
	assert.Equal(t, 1, got.Revision)
}

func TestUpdateManifest(t *testing.T) {
	st := newTestStore(t)
	created := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 7, 15, 13, 30, 0, 0, time.UTC)
	st.now = fixedClock(created, updated)

	require.NoError(t, st.Create(context.Background(), Session{ID: "s", Title: "v1", Manifest: testManifest(shaA)}))

	next := testManifest(shaB)
	revision, err := st.UpdateManifest(context.Background(), Session{ID: "s", Title: "v2", Manifest: next})
	require.NoError(t, err)
	assert.Equal(t, 2, revision)

	got, err := st.Get(context.Background(), "s")
	require.NoError(t, err)
	assert.Equal(t, "v2", got.Title)
	assert.Equal(t, 2, got.Revision)
	assert.Equal(t, next, got.Manifest)
	assert.True(t, created.Equal(got.CreatedAt))
	assert.True(t, updated.Equal(got.UpdatedAt))
}

func TestUpdateManifestNotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.UpdateManifest(context.Background(), Session{ID: "nope", Title: "x", Manifest: testManifest(shaA)})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestListOrdering(t *testing.T) {
	st := newTestStore(t)
	st.now = fixedClock(
		time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
	)

	for _, id := range []string{"first", "second", "third"} {
		require.NoError(t, st.Create(context.Background(), Session{ID: id, Title: id, Manifest: testManifest(shaA)}))
	}

	sessions, err := st.List(context.Background())
	require.NoError(t, err)
	require.Len(t, sessions, 3)
	assert.Equal(t, "third", sessions[0].ID)
	assert.Equal(t, "second", sessions[1].ID)
	assert.Equal(t, "first", sessions[2].ID)
}

func TestListEmpty(t *testing.T) {
	st := newTestStore(t)
	sessions, err := st.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestDelete(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.Create(context.Background(), Session{ID: "gone", Title: "x", Manifest: testManifest(shaA)}))

	require.NoError(t, st.Delete(context.Background(), "gone"))

	_, err := st.Get(context.Background(), "gone")
	assert.ErrorIs(t, err, ErrNotFound)

	assert.ErrorIs(t, st.Delete(context.Background(), "gone"), ErrNotFound)
}

func TestDeleteNotFound(t *testing.T) {
	st := newTestStore(t)
	assert.ErrorIs(t, st.Delete(context.Background(), "missing"), ErrNotFound)
}

func TestNewCreatesMissingParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "catalog.db")

	st, err := New(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	sessions, err := st.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestReopenExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")

	first, err := New(path)
	require.NoError(t, err)
	require.NoError(t, first.Create(context.Background(), Session{ID: "persist", Title: "kept", Manifest: testManifest(shaA)}))
	require.NoError(t, first.Close())

	second, err := New(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })

	got, err := second.Get(context.Background(), "persist")
	require.NoError(t, err)
	assert.Equal(t, "kept", got.Title)
	assert.Equal(t, testManifest(shaA), got.Manifest)
}

func TestGetMalformedRow(t *testing.T) {
	tests := []struct {
		name      string
		createdAt string
		updatedAt string
		manifest  string
		wantErr   string
	}{
		{name: "bad created_at", createdAt: "nope", updatedAt: "2026-07-15T12:00:00Z", manifest: "{}", wantErr: "parse created_at"},
		{name: "bad updated_at", createdAt: "2026-07-15T12:00:00Z", updatedAt: "nope", manifest: "{}", wantErr: "parse updated_at"},
		{name: "bad manifest", createdAt: "2026-07-15T12:00:00Z", updatedAt: "2026-07-15T12:00:00Z", manifest: "{not json}", wantErr: "unmarshal manifest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newTestStore(t)
			_, err := st.db.Exec(
				`INSERT INTO sessions (`+columns+`) VALUES (?, ?, 1, ?, ?, ?)`,
				"bad", "title", tt.createdAt, tt.updatedAt, tt.manifest,
			)
			require.NoError(t, err)

			_, err = st.Get(context.Background(), "bad")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	st := newTestStore(t)
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n*3)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("s-%d", i)
			if err := st.Create(context.Background(), Session{ID: id, Title: id, Manifest: testManifest(shaA)}); err != nil {
				errs <- err
				return
			}
			if _, err := st.Get(context.Background(), id); err != nil {
				errs <- err
			}
			if _, err := st.List(context.Background()); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	sessions, err := st.List(context.Background())
	require.NoError(t, err)
	assert.Len(t, sessions, n)
}

func TestManifestJSONFidelity(t *testing.T) {
	st := newTestStore(t)
	full := manifest.Manifest{
		Tracks: []manifest.Track{
			{Label: "ft.sidon", SortOrder: 0, IsDefault: true, FileRef: manifest.FileRef{Filename: "a.m4a", Size: 100, SHA256: shaA}},
			{Label: "alt dub", SortOrder: 1, IsDefault: false, FileRef: manifest.FileRef{Filename: "b.m4a", Size: 200, SHA256: shaB}},
		},
		Subtitle:    manifest.FileRef{Filename: "s.srt", Size: 50, SHA256: shaC},
		Clip:        manifest.FileRef{Filename: "c.mp4", Size: 300, SHA256: shaD},
		Fingerprint: manifest.FileRef{Filename: "f.shazamcatalog", Size: 400, SHA256: shaE},
	}
	require.NoError(t, st.Create(context.Background(), Session{ID: "multi", Title: strings.Repeat("t", 10), Manifest: full}))

	got, err := st.Get(context.Background(), "multi")
	require.NoError(t, err)
	assert.Equal(t, full, got.Manifest)
}
