package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pkarpovich/allspeak-catalog/internal/api/mocks"
	"github.com/pkarpovich/allspeak-catalog/internal/blob"
	"github.com/pkarpovich/allspeak-catalog/internal/manifest"
)

func adminPost(s *Server, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func uploadBody(t *testing.T, sessionID string, files ...manifest.FileRef) string {
	t.Helper()
	b, err := json.Marshal(uploadRequest{SessionID: sessionID, Files: files})
	require.NoError(t, err)
	return string(b)
}

func fileRef(sha, name string, size int64) manifest.FileRef {
	return manifest.FileRef{Filename: name, Size: size, SHA256: sha}
}

func decodeUploadResponse(t *testing.T, rec *httptest.ResponseRecorder) uploadResponse {
	t.Helper()
	var resp uploadResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func TestNegotiateUploadsAllocatesSessionID(t *testing.T) {
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, _ string) (bool, error) { return false, nil },
		PresignPutFunc: func(_ context.Context, key string) (string, error) {
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(&mocks.SessionStoreMock{}, objectStore)

	body := uploadBody(t, "", fileRef(shaTrackA, "film.m4a", 73400320))
	rec := adminPost(s, "/api/v1/uploads", body)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeUploadResponse(t, rec)
	_, err := uuid.Parse(resp.SessionID)
	require.NoError(t, err, "server must allocate a valid uuid, got %q", resp.SessionID)

	require.Len(t, resp.Files, 1)
	assert.Equal(t, shaTrackA, resp.Files[0].SHA256)
	assert.Equal(t, "film.m4a", resp.Files[0].Filename)
	assert.False(t, resp.Files[0].Exists)
	assert.Equal(t, "https://r2.example/"+blob.Key(resp.SessionID, shaTrackA, "film.m4a"), resp.Files[0].UploadURL)
}

func TestNegotiateUploadsPassesThroughSessionID(t *testing.T) {
	const sessionID = "11111111-1111-1111-1111-111111111111"
	var existsKeys []string
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, key string) (bool, error) {
			existsKeys = append(existsKeys, key)
			return true, nil
		},
	}
	s := serverWith(&mocks.SessionStoreMock{}, objectStore)

	body := uploadBody(t, sessionID, fileRef(shaTrackA, "film.m4a", 73400320))
	rec := adminPost(s, "/api/v1/uploads", body)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeUploadResponse(t, rec)
	assert.Equal(t, sessionID, resp.SessionID)

	require.Len(t, resp.Files, 1)
	assert.True(t, resp.Files[0].Exists)
	assert.Empty(t, resp.Files[0].UploadURL)

	require.Len(t, existsKeys, 1)
	assert.Equal(t, blob.Key(sessionID, shaTrackA, "film.m4a"), existsKeys[0])
}

func TestNegotiateUploadsMixedExistsAndMissing(t *testing.T) {
	const sessionID = "11111111-1111-1111-1111-111111111111"
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, key string) (bool, error) {
			return strings.Contains(key, shaTrackA), nil
		},
		PresignPutFunc: func(_ context.Context, key string) (string, error) {
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(&mocks.SessionStoreMock{}, objectStore)

	body := uploadBody(t, sessionID,
		fileRef(shaTrackA, "film.m4a", 73400320),
		fileRef(shaTrackB, "film_alt.m4a", 70000000),
	)
	rec := adminPost(s, "/api/v1/uploads", body)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeUploadResponse(t, rec)
	require.Len(t, resp.Files, 2)

	assert.True(t, resp.Files[0].Exists)
	assert.Empty(t, resp.Files[0].UploadURL)

	assert.False(t, resp.Files[1].Exists)
	assert.Equal(t, "https://r2.example/"+blob.Key(sessionID, shaTrackB, "film_alt.m4a"), resp.Files[1].UploadURL)
	assert.Len(t, objectStore.PresignPutCalls(), 1)
}

func TestNegotiateUploadsSanitizesFilename(t *testing.T) {
	const sessionID = "11111111-1111-1111-1111-111111111111"
	var existsKeys, putKeys []string
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, key string) (bool, error) {
			existsKeys = append(existsKeys, key)
			return false, nil
		},
		PresignPutFunc: func(_ context.Context, key string) (string, error) {
			putKeys = append(putKeys, key)
			return "https://r2.example/" + key, nil
		},
	}
	s := serverWith(&mocks.SessionStoreMock{}, objectStore)

	body := uploadBody(t, sessionID, fileRef(shaTrackA, "sub/dir/a:b*.m4a", 1))
	rec := adminPost(s, "/api/v1/uploads", body)
	require.Equal(t, http.StatusOK, rec.Code)

	wantKey := blob.Key(sessionID, shaTrackA, "a_b_.m4a")
	resp := decodeUploadResponse(t, rec)
	require.Len(t, resp.Files, 1)
	assert.Equal(t, "a_b_.m4a", resp.Files[0].Filename)
	assert.Equal(t, "https://r2.example/"+wantKey, resp.Files[0].UploadURL)

	require.Equal(t, []string{wantKey}, existsKeys)
	require.Equal(t, []string{wantKey}, putKeys)
}

func TestNegotiateUploadsValidation(t *testing.T) {
	tests := []struct {
		name string
		file manifest.FileRef
	}{
		{name: "bad sha256", file: fileRef("not-hex", "film.m4a", 1)},
		{name: "uppercase sha256", file: fileRef(strings.ToUpper(shaTrackA), "film.m4a", 1)},
		{name: "zero size", file: fileRef(shaTrackA, "film.m4a", 0)},
		{name: "negative size", file: fileRef(shaTrackA, "film.m4a", -1)},
		{name: "empty filename after sanitize", file: fileRef(shaTrackA, ".", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objectStore := &mocks.ObjectStoreMock{
				ExistsFunc:     func(_ context.Context, _ string) (bool, error) { return false, nil },
				PresignPutFunc: func(_ context.Context, _ string) (string, error) { return "url", nil },
			}
			s := serverWith(&mocks.SessionStoreMock{}, objectStore)

			body := uploadBody(t, "", tt.file)
			rec := adminPost(s, "/api/v1/uploads", body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Empty(t, objectStore.ExistsCalls(), "must reject before touching blob")
		})
	}
}

func TestNegotiateUploadsEmptyFiles(t *testing.T) {
	s := serverWith(&mocks.SessionStoreMock{}, &mocks.ObjectStoreMock{})
	rec := adminPost(s, "/api/v1/uploads", `{"files":[]}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNegotiateUploadsInvalidJSON(t *testing.T) {
	s := serverWith(&mocks.SessionStoreMock{}, &mocks.ObjectStoreMock{})
	rec := adminPost(s, "/api/v1/uploads", `{"files":`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNegotiateUploadsExistsFailure(t *testing.T) {
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, _ string) (bool, error) {
			return false, errors.New("r2 down")
		},
	}
	s := serverWith(&mocks.SessionStoreMock{}, objectStore)

	body := uploadBody(t, "", fileRef(shaTrackA, "film.m4a", 1))
	rec := adminPost(s, "/api/v1/uploads", body)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestNegotiateUploadsPresignFailure(t *testing.T) {
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, _ string) (bool, error) { return false, nil },
		PresignPutFunc: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("presign boom")
		},
	}
	s := serverWith(&mocks.SessionStoreMock{}, objectStore)

	body := uploadBody(t, "", fileRef(shaTrackA, "film.m4a", 1))
	rec := adminPost(s, "/api/v1/uploads", body)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestNegotiateUploadsRequiresAdmin(t *testing.T) {
	s := serverWith(&mocks.SessionStoreMock{}, &mocks.ObjectStoreMock{})

	body := uploadBody(t, "", fileRef(shaTrackA, "film.m4a", 1))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+readToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}
