package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pkarpovich/allspeak-catalog/internal/api/mocks"
	"github.com/pkarpovich/allspeak-catalog/internal/blob"
	"github.com/pkarpovich/allspeak-catalog/internal/manifest"
	"github.com/pkarpovich/allspeak-catalog/internal/store"
)

const (
	shaTrackA      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaTrackB      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaSubtitle    = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	shaClip        = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	shaFingerprint = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func serverWith(sessionStore SessionStore, objectStore ObjectStore) *Server {
	return NewServer(Config{
		AdminToken: adminToken,
		ReadToken:  readToken,
		Store:      sessionStore,
		Blob:       objectStore,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func readGet(s *Server, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+readToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func sampleSession() store.Session {
	created := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	return store.Session{
		ID:        "11111111-1111-1111-1111-111111111111",
		Title:     "Some Film",
		Revision:  2,
		CreatedAt: created,
		UpdatedAt: created.Add(time.Hour),
		Manifest: manifest.Manifest{
			Tracks: []manifest.Track{
				{
					Label:     "ft.sidon",
					SortOrder: 0,
					IsDefault: true,
					FileRef:   manifest.FileRef{Filename: "film.m4a", Size: 73400320, SHA256: shaTrackA},
				},
				{
					Label:     "alt",
					SortOrder: 1,
					IsDefault: false,
					FileRef:   manifest.FileRef{Filename: "film_alt.m4a", Size: 70000000, SHA256: shaTrackB},
				},
			},
			Subtitle: manifest.FileRef{Filename: "film.srt", Size: 152000, SHA256: shaSubtitle},
		},
	}
}

func sampleSessionWithClip() store.Session {
	session := sampleSession()
	session.Manifest.Clip = manifest.FileRef{Filename: "film.first-line.mp4", Size: 6200000, SHA256: shaClip}
	return session
}

func sampleSessionWithFingerprint() store.Session {
	session := sampleSessionWithClip()
	session.Manifest.Fingerprint = manifest.FileRef{Filename: "film.shazamcatalog", Size: 980000, SHA256: shaFingerprint}
	return session
}

func TestListCatalog(t *testing.T) {
	session := sampleSession()
	sessionStore := &mocks.SessionStoreMock{
		ListFunc: func(_ context.Context) ([]store.Session, error) {
			return []store.Session{session}, nil
		},
	}
	s := serverWith(sessionStore, &mocks.ObjectStoreMock{})

	rec := readGet(s, "/api/v1/catalog")
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Sessions []catalogItem `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Sessions, 1)

	got := resp.Sessions[0]
	assert.Equal(t, session.ID, got.ID)
	assert.Equal(t, session.Title, got.Title)
	assert.Equal(t, session.Revision, got.Revision)
	assert.True(t, session.UpdatedAt.Equal(got.UpdatedAt))
	assert.Equal(t, int64(73400320+70000000+152000), got.TotalSize)
	assert.Equal(t, []string{"ft.sidon", "alt"}, got.TrackLabels)
}

func TestListCatalogEmpty(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		ListFunc: func(_ context.Context) ([]store.Session, error) {
			return nil, nil
		},
	}
	s := serverWith(sessionStore, &mocks.ObjectStoreMock{})

	rec := readGet(s, "/api/v1/catalog")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"sessions":[]}`, rec.Body.String())
}

func TestListCatalogStoreError(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		ListFunc: func(_ context.Context) ([]store.Session, error) {
			return nil, errors.New("db down")
		},
	}
	s := serverWith(sessionStore, &mocks.ObjectStoreMock{})

	rec := readGet(s, "/api/v1/catalog")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetSession(t *testing.T) {
	session := sampleSession()
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, id string) (store.Session, error) {
			assert.Equal(t, session.ID, id)
			return session, nil
		},
	}
	objectStore := &mocks.ObjectStoreMock{
		PresignGetFunc: func(_ context.Context, key string) (string, error) {
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(sessionStore, objectStore)

	rec := readGet(s, "/api/v1/sessions/"+session.ID)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Revision int    `json:"revision"`
		Tracks   []struct {
			Label     string `json:"label"`
			SortOrder int    `json:"sortOrder"`
			IsDefault bool   `json:"isDefault"`
			Filename  string `json:"filename"`
			Size      int64  `json:"size"`
			SHA256    string `json:"sha256"`
			URL       string `json:"url"`
		} `json:"tracks"`
		Subtitle struct {
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
			URL      string `json:"url"`
		} `json:"subtitle"`
		URLsExpireAt time.Time `json:"urlsExpireAt"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	assert.Equal(t, session.ID, resp.ID)
	assert.Equal(t, session.Title, resp.Title)
	assert.Equal(t, session.Revision, resp.Revision)

	require.Len(t, resp.Tracks, 2)
	assert.Equal(t, "ft.sidon", resp.Tracks[0].Label)
	assert.True(t, resp.Tracks[0].IsDefault)
	wantKey0 := blob.Key(session.ID, shaTrackA, "film.m4a")
	assert.Equal(t, "https://r2.example/"+wantKey0, resp.Tracks[0].URL)
	assert.Equal(t, "https://r2.example/"+blob.Key(session.ID, shaTrackB, "film_alt.m4a"), resp.Tracks[1].URL)

	assert.Equal(t, "film.srt", resp.Subtitle.Filename)
	assert.Equal(t, "https://r2.example/"+blob.Key(session.ID, shaSubtitle, "film.srt"), resp.Subtitle.URL)

	assert.True(t, resp.URLsExpireAt.After(time.Now()))
	assert.True(t, resp.URLsExpireAt.Before(time.Now().Add(2*time.Hour)))
}

func TestGetSessionPresignKeyUsesStoredFilenameVerbatim(t *testing.T) {
	session := sampleSession()
	session.Manifest.Tracks = []manifest.Track{
		{
			Label:     "ft.sidon",
			SortOrder: 0,
			IsDefault: true,
			FileRef:   manifest.FileRef{Filename: "a:b*.m4a", Size: 1, SHA256: shaTrackA},
		},
	}
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return session, nil
		},
	}
	var keys []string
	objectStore := &mocks.ObjectStoreMock{
		PresignGetFunc: func(_ context.Context, key string) (string, error) {
			keys = append(keys, key)
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(sessionStore, objectStore)

	rec := readGet(s, "/api/v1/sessions/"+session.ID)
	require.Equal(t, http.StatusOK, rec.Code)

	require.NotEmpty(t, keys)
	assert.True(t, strings.HasSuffix(keys[0], "-a:b*.m4a"),
		"read path must use the stored filename verbatim, got %q", keys[0])
	assert.Equal(t, blob.Key(session.ID, shaTrackA, "a:b*.m4a"), keys[0])
}

func TestGetSessionNotFound(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return store.Session{}, store.ErrNotFound
		},
	}
	s := serverWith(sessionStore, &mocks.ObjectStoreMock{})

	rec := readGet(s, "/api/v1/sessions/unknown")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetSessionStoreError(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return store.Session{}, errors.New("db down")
		},
	}
	s := serverWith(sessionStore, &mocks.ObjectStoreMock{})

	rec := readGet(s, "/api/v1/sessions/whatever")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetSessionPresignFailure(t *testing.T) {
	session := sampleSession()
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return session, nil
		},
	}
	objectStore := &mocks.ObjectStoreMock{
		PresignGetFunc: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("presign boom")
		},
	}
	s := serverWith(sessionStore, objectStore)

	rec := readGet(s, "/api/v1/sessions/"+session.ID)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestListCatalogTotalSizeIncludesClip(t *testing.T) {
	session := sampleSessionWithClip()
	sessionStore := &mocks.SessionStoreMock{
		ListFunc: func(_ context.Context) ([]store.Session, error) {
			return []store.Session{session}, nil
		},
	}
	s := serverWith(sessionStore, &mocks.ObjectStoreMock{})

	rec := readGet(s, "/api/v1/catalog")
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Sessions []catalogItem `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Sessions, 1)
	assert.Equal(t, int64(73400320+70000000+152000+6200000), resp.Sessions[0].TotalSize)
	assert.Equal(t, []string{"ft.sidon", "alt"}, resp.Sessions[0].TrackLabels)
}

func TestGetSessionWithClip(t *testing.T) {
	session := sampleSessionWithClip()
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return session, nil
		},
	}
	objectStore := &mocks.ObjectStoreMock{
		PresignGetFunc: func(_ context.Context, key string) (string, error) {
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(sessionStore, objectStore)

	rec := readGet(s, "/api/v1/sessions/"+session.ID)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Clip struct {
			Filename string `json:"filename"`
			Size     int64  `json:"size"`
			SHA256   string `json:"sha256"`
			URL      string `json:"url"`
		} `json:"clip"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	assert.Equal(t, "film.first-line.mp4", resp.Clip.Filename)
	assert.Equal(t, int64(6200000), resp.Clip.Size)
	assert.Equal(t, shaClip, resp.Clip.SHA256)
	wantKey := blob.Key(session.ID, shaClip, "film.first-line.mp4")
	assert.Equal(t, "https://r2.example/"+wantKey, resp.Clip.URL)
}

func TestGetSessionWithoutClipOmitsKey(t *testing.T) {
	session := sampleSession()
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return session, nil
		},
	}
	objectStore := &mocks.ObjectStoreMock{
		PresignGetFunc: func(_ context.Context, key string) (string, error) {
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(sessionStore, objectStore)

	rec := readGet(s, "/api/v1/sessions/"+session.ID)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"clip"`)
}

func TestGetSessionClipPresignFailure(t *testing.T) {
	session := sampleSessionWithClip()
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return session, nil
		},
	}
	clipKey := blob.Key(session.ID, shaClip, "film.first-line.mp4")
	objectStore := &mocks.ObjectStoreMock{
		PresignGetFunc: func(_ context.Context, key string) (string, error) {
			if key == clipKey {
				return "", errors.New("presign boom")
			}
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(sessionStore, objectStore)

	rec := readGet(s, "/api/v1/sessions/"+session.ID)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestListCatalogTotalSizeIncludesFingerprint(t *testing.T) {
	session := sampleSessionWithFingerprint()
	sessionStore := &mocks.SessionStoreMock{
		ListFunc: func(_ context.Context) ([]store.Session, error) {
			return []store.Session{session}, nil
		},
	}
	s := serverWith(sessionStore, &mocks.ObjectStoreMock{})

	rec := readGet(s, "/api/v1/catalog")
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Sessions []catalogItem `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Sessions, 1)
	assert.Equal(t, int64(73400320+70000000+152000+6200000+980000), resp.Sessions[0].TotalSize)
}

func TestGetSessionWithFingerprint(t *testing.T) {
	session := sampleSessionWithFingerprint()
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return session, nil
		},
	}
	objectStore := &mocks.ObjectStoreMock{
		PresignGetFunc: func(_ context.Context, key string) (string, error) {
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(sessionStore, objectStore)

	rec := readGet(s, "/api/v1/sessions/"+session.ID)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Fingerprint struct {
			Filename string `json:"filename"`
			Size     int64  `json:"size"`
			SHA256   string `json:"sha256"`
			URL      string `json:"url"`
		} `json:"fingerprint"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	assert.Equal(t, "film.shazamcatalog", resp.Fingerprint.Filename)
	assert.Equal(t, int64(980000), resp.Fingerprint.Size)
	assert.Equal(t, shaFingerprint, resp.Fingerprint.SHA256)
	wantKey := blob.Key(session.ID, shaFingerprint, "film.shazamcatalog")
	assert.Equal(t, "https://r2.example/"+wantKey, resp.Fingerprint.URL)
}

func TestGetSessionWithoutFingerprintOmitsKey(t *testing.T) {
	session := sampleSessionWithClip()
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return session, nil
		},
	}
	objectStore := &mocks.ObjectStoreMock{
		PresignGetFunc: func(_ context.Context, key string) (string, error) {
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(sessionStore, objectStore)

	rec := readGet(s, "/api/v1/sessions/"+session.ID)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"clip"`)
	assert.NotContains(t, rec.Body.String(), `"fingerprint"`)
}

func TestGetSessionFingerprintPresignFailure(t *testing.T) {
	session := sampleSessionWithFingerprint()
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return session, nil
		},
	}
	fingerprintKey := blob.Key(session.ID, shaFingerprint, "film.shazamcatalog")
	objectStore := &mocks.ObjectStoreMock{
		PresignGetFunc: func(_ context.Context, key string) (string, error) {
			if key == fingerprintKey {
				return "", errors.New("presign boom")
			}
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(sessionStore, objectStore)

	rec := readGet(s, "/api/v1/sessions/"+session.ID)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}
