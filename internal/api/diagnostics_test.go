package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pkarpovich/allspeak-catalog/internal/api/mocks"
	"github.com/pkarpovich/allspeak-catalog/internal/blob"
	"github.com/pkarpovich/allspeak-catalog/internal/store"
)

const diagSessionID = "44a5d3f8-884b-4820-8f9e-608a1c8cb5a5"

func readPost(s *Server, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+readToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func knownSessionStore() *mocks.SessionStoreMock {
	return &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, id string) (store.Session, error) {
			if id != diagSessionID {
				return store.Session{}, store.ErrNotFound
			}
			return store.Session{ID: id}, nil
		},
	}
}

type putCall struct {
	key, contentType string
	body             []byte
}

func recordingPuts(calls *[]putCall) *mocks.ObjectStoreMock {
	return &mocks.ObjectStoreMock{
		PutFunc: func(_ context.Context, key, contentType string, body []byte) error {
			*calls = append(*calls, putCall{key: key, contentType: contentType, body: body})
			return nil
		},
	}
}

func TestUploadSessionDiagnostics(t *testing.T) {
	var puts []putCall
	s := serverWith(knownSessionStore(), recordingPuts(&puts))
	body := []byte("{\"event\":\"session\"}\n")

	rec := readPost(s, "/api/v1/sessions/"+strings.ToUpper(diagSessionID)+"/diagnostics?name=clayface-20261104-1742.jsonl", body)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var got diagnosticsStored
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, diagnosticsStored{Key: "diagnostics/" + diagSessionID + "/clayface-20261104-1742.jsonl", Size: len(body)}, got)
	require.Len(t, puts, 1)
	assert.Equal(t, putCall{key: got.Key, contentType: "application/x-ndjson", body: body}, puts[0])
}

func TestUploadCrashDiagnostics(t *testing.T) {
	var puts []putCall
	s := serverWith(&mocks.SessionStoreMock{}, recordingPuts(&puts))

	rec := readPost(s, "/api/v1/diagnostics/crashes?name=crash-2026-11-04T194512Z.json", []byte(`{"crashDiagnostics":[]}`))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Len(t, puts, 1)
	assert.Equal(t, "diagnostics/crashes/crash-2026-11-04T194512Z.json", puts[0].key)
	assert.Equal(t, "application/json", puts[0].contentType)
}

func TestUploadDiagnosticsRejectsBadNames(t *testing.T) {
	names := []string{"", ".hidden", "../escape.jsonl", "a/b.jsonl", "space name.jsonl", "colon:time.json", strings.Repeat("a", 129)}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var puts []putCall
			s := serverWith(knownSessionStore(), recordingPuts(&puts))

			rec := readPost(s, "/api/v1/sessions/"+diagSessionID+"/diagnostics?name="+url.QueryEscape(name), []byte("x"))

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Empty(t, puts)
		})
	}
}

func TestUploadDiagnosticsAcceptsLongestName(t *testing.T) {
	var puts []putCall
	s := serverWith(knownSessionStore(), recordingPuts(&puts))

	rec := readPost(s, "/api/v1/diagnostics/crashes?name="+strings.Repeat("a", 128), []byte("x"))

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestUploadDiagnosticsRejectsEmptyAndOversizedBodies(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want int
	}{
		{name: "empty", body: nil, want: http.StatusBadRequest},
		{name: "over 8 MB", body: bytes.Repeat([]byte("x"), MaxDiagnosticsSize+1), want: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var puts []putCall
			s := serverWith(knownSessionStore(), recordingPuts(&puts))

			rec := readPost(s, "/api/v1/sessions/"+diagSessionID+"/diagnostics?name=log.jsonl", tt.body)

			assert.Equal(t, tt.want, rec.Code)
			assert.Empty(t, puts)
		})
	}
}

func TestUploadDiagnosticsAcceptsExactly8MB(t *testing.T) {
	var puts []putCall
	s := serverWith(knownSessionStore(), recordingPuts(&puts))

	rec := readPost(s, "/api/v1/sessions/"+diagSessionID+"/diagnostics?name=log.jsonl", bytes.Repeat([]byte("x"), MaxDiagnosticsSize))

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestSessionDiagnosticsUnknownSession(t *testing.T) {
	var puts []putCall
	s := serverWith(knownSessionStore(), recordingPuts(&puts))

	post := readPost(s, "/api/v1/sessions/00000000-0000-0000-0000-000000000000/diagnostics?name=log.jsonl", []byte("x"))
	get := readGet(s, "/api/v1/sessions/00000000-0000-0000-0000-000000000000/diagnostics")

	assert.Equal(t, http.StatusNotFound, post.Code)
	assert.Equal(t, http.StatusNotFound, get.Code)
	assert.Empty(t, puts)
}

func TestUploadDiagnosticsStorageFailure(t *testing.T) {
	objectStore := &mocks.ObjectStoreMock{
		PutFunc: func(_ context.Context, _, _ string, _ []byte) error { return errors.New("r2 down") },
	}
	s := serverWith(knownSessionStore(), objectStore)

	rec := readPost(s, "/api/v1/sessions/"+diagSessionID+"/diagnostics?name=log.jsonl", []byte("x"))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestListSessionDiagnostics(t *testing.T) {
	prefix := "diagnostics/" + diagSessionID + "/"
	older := time.Date(2026, 11, 4, 19, 0, 0, 0, time.UTC)
	objectStore := &mocks.ObjectStoreMock{
		ListFunc: func(_ context.Context, p string) ([]blob.Object, error) {
			require.Equal(t, prefix, p)
			return []blob.Object{
				{Key: prefix + "old.jsonl", Size: 100, LastModified: older},
				{Key: prefix + "new.jsonl", Size: 200, LastModified: older.Add(time.Hour)},
				{Key: prefix + "nested/skip.jsonl", Size: 1, LastModified: older},
			}, nil
		},
		PresignGetFunc: func(_ context.Context, key string) (string, error) {
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(knownSessionStore(), objectStore)

	rec := readGet(s, "/api/v1/sessions/"+diagSessionID+"/diagnostics")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got diagnosticsList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []diagnosticsFile{
		{Name: "new.jsonl", Size: 200, UploadedAt: older.Add(time.Hour), URL: "https://r2.example/" + prefix + "new.jsonl"},
		{Name: "old.jsonl", Size: 100, UploadedAt: older, URL: "https://r2.example/" + prefix + "old.jsonl"},
	}, got.Files)
	assert.WithinDuration(t, time.Now().Add(blob.PresignExpiry), got.URLsExpireAt, time.Minute)
}

func TestListCrashDiagnosticsEmpty(t *testing.T) {
	objectStore := &mocks.ObjectStoreMock{
		ListFunc: func(_ context.Context, p string) ([]blob.Object, error) {
			require.Equal(t, "diagnostics/crashes/", p)
			return nil, nil
		},
	}
	s := serverWith(&mocks.SessionStoreMock{}, objectStore)

	rec := readGet(s, "/api/v1/diagnostics/crashes")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `[]`, mustField(t, rec.Body.Bytes(), "files"))
}

func TestListDiagnosticsStorageFailure(t *testing.T) {
	objectStore := &mocks.ObjectStoreMock{
		ListFunc: func(_ context.Context, _ string) ([]blob.Object, error) { return nil, errors.New("r2 down") },
	}
	s := serverWith(&mocks.SessionStoreMock{}, objectStore)

	rec := readGet(s, "/api/v1/diagnostics/crashes")

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestDiagnosticsRequireToken(t *testing.T) {
	s := serverWith(knownSessionStore(), &mocks.ObjectStoreMock{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/crashes?name=a.json", strings.NewReader("x"))
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func mustField(t *testing.T, body []byte, field string) string {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &m))
	raw, ok := m[field]
	require.True(t, ok, "missing field %q", field)
	return string(raw)
}
