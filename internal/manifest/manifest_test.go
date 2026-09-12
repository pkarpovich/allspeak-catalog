package manifest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	shaD = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func track(label string, isDefault bool, sha string) Track {
	return Track{
		Label:     label,
		SortOrder: 0,
		IsDefault: isDefault,
		FileRef:   FileRef{Filename: "film.m4a", Size: 73400320, SHA256: sha},
	}
}

func subtitle() FileRef {
	return FileRef{Filename: "film.srt", Size: 152000, SHA256: shaC}
}

func clip() FileRef {
	return FileRef{Filename: "film.first-line.mp4", Size: 6200000, SHA256: shaD}
}

func validManifest() Manifest {
	return Manifest{
		Tracks:   []Track{track("ft.sidon", true, shaA)},
		Subtitle: subtitle(),
	}
}

func validManifestWithClip() Manifest {
	m := validManifest()
	m.Clip = clip()
	return m
}

func TestManifestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m *Manifest)
		wantErr string
	}{
		{name: "valid single track"},
		{
			name: "valid multiple tracks one default",
			mutate: func(m *Manifest) {
				m.Tracks = append(m.Tracks, track("alt", false, shaB))
			},
		},
		{
			name:    "no tracks",
			mutate:  func(m *Manifest) { m.Tracks = nil },
			wantErr: "tracks:",
		},
		{
			name:    "empty label",
			mutate:  func(m *Manifest) { m.Tracks[0].Label = "   " },
			wantErr: "tracks[0].label",
		},
		{
			name: "no default track",
			mutate: func(m *Manifest) {
				m.Tracks[0].IsDefault = false
			},
			wantErr: "isDefault=true, found 0",
		},
		{
			name: "two default tracks",
			mutate: func(m *Manifest) {
				m.Tracks = append(m.Tracks, track("alt", true, shaB))
			},
			wantErr: "isDefault=true, found 2",
		},
		{
			name:    "track sha256 too short",
			mutate:  func(m *Manifest) { m.Tracks[0].SHA256 = "abc" },
			wantErr: "tracks[0].sha256",
		},
		{
			name:    "track sha256 uppercase",
			mutate:  func(m *Manifest) { m.Tracks[0].SHA256 = strings.ToUpper(shaA) },
			wantErr: "tracks[0].sha256",
		},
		{
			name:    "track size zero",
			mutate:  func(m *Manifest) { m.Tracks[0].Size = 0 },
			wantErr: "tracks[0].size",
		},
		{
			name:    "track filename empty",
			mutate:  func(m *Manifest) { m.Tracks[0].Filename = "" },
			wantErr: "tracks[0].filename",
		},
		{
			name:    "subtitle missing",
			mutate:  func(m *Manifest) { m.Subtitle = FileRef{} },
			wantErr: "subtitle: required",
		},
		{
			name:    "subtitle bad sha256",
			mutate:  func(m *Manifest) { m.Subtitle.SHA256 = "nothex" },
			wantErr: "subtitle.sha256",
		},
		{
			name:    "subtitle size zero",
			mutate:  func(m *Manifest) { m.Subtitle.Size = 0 },
			wantErr: "subtitle.size",
		},
		{
			name:   "valid with clip",
			mutate: func(m *Manifest) { m.Clip = clip() },
		},
		{
			name: "clip bad sha256",
			mutate: func(m *Manifest) {
				m.Clip = clip()
				m.Clip.SHA256 = "nothex"
			},
			wantErr: "clip.sha256",
		},
		{
			name: "clip size zero",
			mutate: func(m *Manifest) {
				m.Clip = clip()
				m.Clip.Size = 0
			},
			wantErr: "clip.size",
		},
		{
			name: "clip filename empty after sanitization",
			mutate: func(m *Manifest) {
				m.Clip = clip()
				m.Clip.Filename = "."
			},
			wantErr: "clip.filename",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			if tt.mutate != nil {
				tt.mutate(&m)
			}
			err := m.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "plain", input: "film.m4a", want: "film.m4a"},
		{name: "strips path", input: "/Users/x/Movies/film.m4a", want: "film.m4a"},
		{name: "trailing slash", input: "a/b/", want: "b"},
		{name: "keeps spaces and dash", input: "ft sidon-v2.m4a", want: "ft sidon-v2.m4a"},
		{name: "forbidden chars replaced", input: "my:file*?.srt", want: "my_file__.srt"},
		{name: "forbidden only", input: "@#$", want: "___"},
		{name: "unicode replaced", input: "café.m4a", want: "caf_.m4a"},
		{name: "empty", input: "", wantErr: true},
		{name: "root", input: "/", wantErr: true},
		{name: "dot", input: ".", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeFilename(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSanitizeFilenameIdempotent(t *testing.T) {
	once, err := SanitizeFilename("my:weird/na*me.srt")
	require.NoError(t, err)
	twice, err := SanitizeFilename(once)
	require.NoError(t, err)
	assert.Equal(t, once, twice)
}

func TestManifestSanitize(t *testing.T) {
	m := Manifest{
		Tracks: []Track{
			{Label: "ft.sidon", IsDefault: true, FileRef: FileRef{Filename: "sub/dir/a:b*.m4a", Size: 1, SHA256: shaA}},
			{Label: "alt", FileRef: FileRef{Filename: "/Movies/alt?.m4a", Size: 1, SHA256: shaB}},
		},
		Subtitle: FileRef{Filename: "weird|name.srt", Size: 1, SHA256: shaC},
		Clip:     FileRef{Filename: "/clips/first:line*.mp4", Size: 1, SHA256: shaD},
	}
	require.NoError(t, m.Sanitize())
	assert.Equal(t, "a_b_.m4a", m.Tracks[0].Filename)
	assert.Equal(t, "alt_.m4a", m.Tracks[1].Filename)
	assert.Equal(t, "weird_name.srt", m.Subtitle.Filename)
	assert.Equal(t, "first_line_.mp4", m.Clip.Filename)
}

func TestManifestSanitizeWithoutClip(t *testing.T) {
	m := validManifest()
	require.NoError(t, m.Sanitize())
	assert.Equal(t, FileRef{}, m.Clip)
}

func TestManifestSanitizeErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m *Manifest)
		wantErr string
	}{
		{
			name:    "track filename empty after sanitize",
			mutate:  func(m *Manifest) { m.Tracks[0].Filename = "." },
			wantErr: "tracks[0].filename",
		},
		{
			name:    "subtitle filename empty after sanitize",
			mutate:  func(m *Manifest) { m.Subtitle.Filename = "/" },
			wantErr: "subtitle.filename",
		},
		{
			name: "clip filename empty after sanitize",
			mutate: func(m *Manifest) {
				m.Clip = clip()
				m.Clip.Filename = "."
			},
			wantErr: "clip.filename",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			tt.mutate(&m)
			err := m.Sanitize()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestManifestFiles(t *testing.T) {
	m := Manifest{
		Tracks: []Track{
			track("ft.sidon", true, shaA),
			track("alt", false, shaB),
		},
		Subtitle: subtitle(),
	}
	files := m.Files()
	require.Len(t, files, 3)
	assert.Equal(t, shaA, files[0].SHA256)
	assert.Equal(t, shaB, files[1].SHA256)
	assert.Equal(t, shaC, files[2].SHA256)
}

func TestManifestFilesWithClip(t *testing.T) {
	m := validManifestWithClip()
	files := m.Files()
	require.Len(t, files, 3)
	assert.Equal(t, shaA, files[0].SHA256)
	assert.Equal(t, shaC, files[1].SHA256)
	assert.Equal(t, clip(), files[2])
}

func TestManifestJSONRoundTrip(t *testing.T) {
	in := validManifest()
	raw, err := json.Marshal(in)
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"tracks": [{"label":"ft.sidon","sortOrder":0,"isDefault":true,
		            "filename":"film.m4a","size":73400320,
		            "sha256":"`+shaA+`"}],
		"subtitle": {"filename":"film.srt","size":152000,"sha256":"`+shaC+`"}
	}`, string(raw))

	assert.NotContains(t, string(raw), `"clip"`)

	var out Manifest
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in, out)
}

func TestManifestJSONRoundTripWithClip(t *testing.T) {
	in := validManifestWithClip()
	raw, err := json.Marshal(in)
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"tracks": [{"label":"ft.sidon","sortOrder":0,"isDefault":true,
		            "filename":"film.m4a","size":73400320,
		            "sha256":"`+shaA+`"}],
		"subtitle": {"filename":"film.srt","size":152000,"sha256":"`+shaC+`"},
		"clip": {"filename":"film.first-line.mp4","size":6200000,"sha256":"`+shaD+`"}
	}`, string(raw))

	var out Manifest
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in, out)
}
