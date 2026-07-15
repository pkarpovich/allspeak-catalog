package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pkarpovich/allspeak-catalog/internal/api/mocks"
	"github.com/pkarpovich/allspeak-catalog/internal/blob"
	"github.com/pkarpovich/allspeak-catalog/internal/manifest"
	"github.com/pkarpovich/allspeak-catalog/internal/store"
)

const validSessionID = "11111111-1111-1111-1111-111111111111"

func adminReq(s *Server, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func sampleManifest() manifest.Manifest {
	return sampleSession().Manifest
}

func finalizeBody(t *testing.T, req finalizeRequest) string {
	t.Helper()
	b, err := json.Marshal(req)
	require.NoError(t, err)
	return string(b)
}

func existsAll() *mocks.ObjectStoreMock {
	return &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, _ string) (bool, error) { return true, nil },
	}
}

func decodeFinalizeResponse(t *testing.T, rec *httptest.ResponseRecorder) finalizeResponse {
	t.Helper()
	var resp finalizeResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func TestCreateSession(t *testing.T) {
	var created store.Session
	sessionStore := &mocks.SessionStoreMock{
		CreateFunc: func(_ context.Context, session store.Session) error {
			created = session
			return nil
		},
	}
	s := serverWith(sessionStore, existsAll())

	body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: "Some Film", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeFinalizeResponse(t, rec)
	assert.Equal(t, validSessionID, resp.ID)
	assert.Equal(t, 1, resp.Revision)

	assert.Equal(t, validSessionID, created.ID)
	assert.Equal(t, "Some Film", created.Title)
	assert.Equal(t, sampleManifest(), created.Manifest)
}

func TestCreateSessionTrimsTitle(t *testing.T) {
	var created store.Session
	sessionStore := &mocks.SessionStoreMock{
		CreateFunc: func(_ context.Context, session store.Session) error {
			created = session
			return nil
		},
	}
	s := serverWith(sessionStore, existsAll())

	body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: "  Some Film  ", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Some Film", created.Title)
}

func TestCreateSessionVerifiesEveryObject(t *testing.T) {
	var existsKeys []string
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, key string) (bool, error) {
			existsKeys = append(existsKeys, key)
			return true, nil
		},
	}
	sessionStore := &mocks.SessionStoreMock{
		CreateFunc: func(_ context.Context, _ store.Session) error { return nil },
	}
	s := serverWith(sessionStore, objectStore)

	body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: "Some Film", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, []string{
		blob.Key(validSessionID, shaTrackA, "film.m4a"),
		blob.Key(validSessionID, shaTrackB, "film_alt.m4a"),
		blob.Key(validSessionID, shaSubtitle, "film.srt"),
	}, existsKeys)
}

func TestCreateSessionMissingObjects(t *testing.T) {
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, key string) (bool, error) {
			return strings.Contains(key, shaTrackA), nil
		},
	}
	sessionStore := &mocks.SessionStoreMock{
		CreateFunc: func(_ context.Context, _ store.Session) error { return nil },
	}
	s := serverWith(sessionStore, objectStore)

	body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: "Some Film", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
	require.Equal(t, http.StatusConflict, rec.Code)

	var resp struct {
		Missing []string `json:"missing"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, []string{shaTrackB, shaSubtitle}, resp.Missing)
	assert.Empty(t, sessionStore.CreateCalls())
}

func TestCreateSessionVerifyFailure(t *testing.T) {
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, _ string) (bool, error) {
			return false, errors.New("r2 down")
		},
	}
	sessionStore := &mocks.SessionStoreMock{
		CreateFunc: func(_ context.Context, _ store.Session) error { return nil },
	}
	s := serverWith(sessionStore, objectStore)

	body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: "Some Film", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Empty(t, sessionStore.CreateCalls())
}

func TestCreateSessionInvalidUUID(t *testing.T) {
	objectStore := existsAll()
	s := serverWith(&mocks.SessionStoreMock{}, objectStore)

	body := finalizeBody(t, finalizeRequest{SessionID: "not-a-uuid", Title: "Some Film", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, objectStore.ExistsCalls(), "must reject invalid uuid before touching blob")
}

func TestCreateSessionDuplicate(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		CreateFunc: func(_ context.Context, _ store.Session) error { return store.ErrExists },
	}
	s := serverWith(sessionStore, existsAll())

	body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: "Some Film", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestCreateSessionStoreError(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		CreateFunc: func(_ context.Context, _ store.Session) error { return errors.New("db down") },
	}
	s := serverWith(sessionStore, existsAll())

	body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: "Some Film", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestCreateSessionInvalidManifest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(m *manifest.Manifest)
	}{
		{name: "no tracks", mutate: func(m *manifest.Manifest) { m.Tracks = nil }},
		{name: "two defaults", mutate: func(m *manifest.Manifest) { m.Tracks[1].IsDefault = true }},
		{name: "bad sha256", mutate: func(m *manifest.Manifest) { m.Tracks[0].SHA256 = "abc" }},
		{name: "zero size", mutate: func(m *manifest.Manifest) { m.Tracks[0].Size = 0 }},
		{name: "subtitle missing", mutate: func(m *manifest.Manifest) { m.Subtitle = manifest.FileRef{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objectStore := existsAll()
			s := serverWith(&mocks.SessionStoreMock{}, objectStore)

			m := sampleManifest()
			tt.mutate(&m)
			body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: "Some Film", Manifest: m})
			rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Empty(t, objectStore.ExistsCalls(), "must reject invalid manifest before touching blob")
		})
	}
}

func TestCreateSessionInvalidTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
	}{
		{name: "empty", title: ""},
		{name: "whitespace", title: "   "},
		{name: "too long", title: strings.Repeat("a", maxTitleLen+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := serverWith(&mocks.SessionStoreMock{}, existsAll())
			body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: tt.title, Manifest: sampleManifest()})
			rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestCreateSessionTitleLength(t *testing.T) {
	tests := []struct {
		name       string
		title      string
		wantStatus int
	}{
		{name: "max ascii accepted", title: strings.Repeat("a", maxTitleLen), wantStatus: http.StatusOK},
		{name: "over max ascii rejected", title: strings.Repeat("a", maxTitleLen+1), wantStatus: http.StatusBadRequest},
		{name: "max multibyte accepted", title: strings.Repeat("я", maxTitleLen), wantStatus: http.StatusOK},
		{name: "over max multibyte rejected", title: strings.Repeat("я", maxTitleLen+1), wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionStore := &mocks.SessionStoreMock{
				CreateFunc: func(_ context.Context, _ store.Session) error { return nil },
			}
			s := serverWith(sessionStore, existsAll())
			body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: tt.title, Manifest: sampleManifest()})
			rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestCreateSessionInvalidJSON(t *testing.T) {
	s := serverWith(&mocks.SessionStoreMock{}, existsAll())
	rec := adminReq(s, http.MethodPost, "/api/v1/sessions", `{"title":`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateSessionSanitizesFilename(t *testing.T) {
	var created store.Session
	var existsKeys []string
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, key string) (bool, error) {
			existsKeys = append(existsKeys, key)
			return true, nil
		},
	}
	sessionStore := &mocks.SessionStoreMock{
		CreateFunc: func(_ context.Context, session store.Session) error {
			created = session
			return nil
		},
	}
	s := serverWith(sessionStore, objectStore)

	m := manifest.Manifest{
		Tracks: []manifest.Track{
			{Label: "ft.sidon", IsDefault: true, FileRef: manifest.FileRef{Filename: "sub/dir/a:b*.m4a", Size: 1, SHA256: shaTrackA}},
		},
		Subtitle: manifest.FileRef{Filename: "weird|name.srt", Size: 1, SHA256: shaSubtitle},
	}
	body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: "Film", Manifest: m})
	rec := adminReq(s, http.MethodPost, "/api/v1/sessions", body)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "a_b_.m4a", created.Manifest.Tracks[0].Filename)
	assert.Equal(t, "weird_name.srt", created.Manifest.Subtitle.Filename)

	assert.Equal(t, []string{
		blob.Key(validSessionID, shaTrackA, "a_b_.m4a"),
		blob.Key(validSessionID, shaSubtitle, "weird_name.srt"),
	}, existsKeys)
}

func TestUpdateSession(t *testing.T) {
	var updated store.Session
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return sampleSession(), nil
		},
		UpdateManifestFunc: func(_ context.Context, session store.Session) (int, error) {
			updated = session
			return 3, nil
		},
	}
	s := serverWith(sessionStore, existsAll())

	body := finalizeBody(t, finalizeRequest{Title: "Better Dub", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPut, "/api/v1/sessions/"+validSessionID, body)
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeFinalizeResponse(t, rec)
	assert.Equal(t, validSessionID, resp.ID)
	assert.Equal(t, 3, resp.Revision)

	assert.Equal(t, validSessionID, updated.ID)
	assert.Equal(t, "Better Dub", updated.Title)
	assert.Equal(t, sampleManifest(), updated.Manifest)
}

func TestUpdateSessionNotFound(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return store.Session{}, store.ErrNotFound
		},
	}
	s := serverWith(sessionStore, existsAll())

	body := finalizeBody(t, finalizeRequest{Title: "Better Dub", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPut, "/api/v1/sessions/"+validSessionID, body)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, sessionStore.UpdateManifestCalls())
}

func TestUpdateSessionMissingObjects(t *testing.T) {
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, key string) (bool, error) {
			return !strings.Contains(key, shaSubtitle), nil
		},
	}
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return sampleSession(), nil
		},
		UpdateManifestFunc: func(_ context.Context, _ store.Session) (int, error) { return 0, nil },
	}
	s := serverWith(sessionStore, objectStore)

	body := finalizeBody(t, finalizeRequest{Title: "Better Dub", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPut, "/api/v1/sessions/"+validSessionID, body)
	require.Equal(t, http.StatusConflict, rec.Code)

	var resp struct {
		Missing []string `json:"missing"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, []string{shaSubtitle}, resp.Missing)
	assert.Empty(t, sessionStore.UpdateManifestCalls())
}

func TestUpdateSessionGetError(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return store.Session{}, errors.New("db down")
		},
	}
	s := serverWith(sessionStore, existsAll())

	body := finalizeBody(t, finalizeRequest{Title: "Better Dub", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPut, "/api/v1/sessions/"+validSessionID, body)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Empty(t, sessionStore.UpdateManifestCalls())
}

func TestUpdateSessionDeletedDuringUpdate(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return sampleSession(), nil
		},
		UpdateManifestFunc: func(_ context.Context, _ store.Session) (int, error) {
			return 0, store.ErrNotFound
		},
	}
	s := serverWith(sessionStore, existsAll())

	body := finalizeBody(t, finalizeRequest{Title: "Better Dub", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPut, "/api/v1/sessions/"+validSessionID, body)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUpdateSessionStoreError(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return sampleSession(), nil
		},
		UpdateManifestFunc: func(_ context.Context, _ store.Session) (int, error) {
			return 0, errors.New("db down")
		},
	}
	s := serverWith(sessionStore, existsAll())

	body := finalizeBody(t, finalizeRequest{Title: "Better Dub", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPut, "/api/v1/sessions/"+validSessionID, body)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUpdateSessionVerifyFailure(t *testing.T) {
	objectStore := &mocks.ObjectStoreMock{
		ExistsFunc: func(_ context.Context, _ string) (bool, error) {
			return false, errors.New("r2 down")
		},
	}
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return sampleSession(), nil
		},
		UpdateManifestFunc: func(_ context.Context, _ store.Session) (int, error) { return 0, nil },
	}
	s := serverWith(sessionStore, objectStore)

	body := finalizeBody(t, finalizeRequest{Title: "Better Dub", Manifest: sampleManifest()})
	rec := adminReq(s, http.MethodPut, "/api/v1/sessions/"+validSessionID, body)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Empty(t, sessionStore.UpdateManifestCalls())
}

func TestUpdateSessionInvalidManifest(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		GetFunc: func(_ context.Context, _ string) (store.Session, error) {
			return sampleSession(), nil
		},
	}
	s := serverWith(sessionStore, existsAll())

	m := sampleManifest()
	m.Tracks = nil
	body := finalizeBody(t, finalizeRequest{Title: "Better Dub", Manifest: m})
	rec := adminReq(s, http.MethodPut, "/api/v1/sessions/"+validSessionID, body)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, sessionStore.GetCalls(), "must reject invalid manifest before touching store")
}

func TestDeleteSession(t *testing.T) {
	var deletedID string
	sessionStore := &mocks.SessionStoreMock{
		DeleteFunc: func(_ context.Context, id string) error {
			deletedID = id
			return nil
		},
	}
	s := serverWith(sessionStore, &mocks.ObjectStoreMock{})

	rec := adminReq(s, http.MethodDelete, "/api/v1/sessions/"+validSessionID, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, validSessionID, deletedID)
}

func TestDeleteSessionNotFound(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		DeleteFunc: func(_ context.Context, _ string) error { return store.ErrNotFound },
	}
	s := serverWith(sessionStore, &mocks.ObjectStoreMock{})

	rec := adminReq(s, http.MethodDelete, "/api/v1/sessions/"+validSessionID, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteSessionStoreError(t *testing.T) {
	sessionStore := &mocks.SessionStoreMock{
		DeleteFunc: func(_ context.Context, _ string) error { return errors.New("db down") },
	}
	s := serverWith(sessionStore, &mocks.ObjectStoreMock{})

	rec := adminReq(s, http.MethodDelete, "/api/v1/sessions/"+validSessionID, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestFinalizeRequiresAdmin(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/sessions"},
		{name: "update", method: http.MethodPut, path: "/api/v1/sessions/" + validSessionID},
		{name: "delete", method: http.MethodDelete, path: "/api/v1/sessions/" + validSessionID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := serverWith(&mocks.SessionStoreMock{}, &mocks.ObjectStoreMock{})
			body := finalizeBody(t, finalizeRequest{SessionID: validSessionID, Title: "Film", Manifest: sampleManifest()})
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+readToken)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			assert.Equal(t, http.StatusForbidden, rec.Code)
		})
	}
}
