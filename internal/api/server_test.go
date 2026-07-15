package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pkarpovich/allspeak-catalog/internal/api/mocks"
)

var (
	_ SessionStore = (*mocks.SessionStoreMock)(nil)
	_ ObjectStore  = (*mocks.ObjectStoreMock)(nil)
)

const (
	adminToken = "admin-secret-token"
	readToken  = "read-secret-token"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(Config{
		AdminToken: adminToken,
		ReadToken:  readToken,
		Store:      &mocks.SessionStoreMock{},
		Blob:       &mocks.ObjectStoreMock{},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestHealth(t *testing.T) {
	s := newTestServer(t)

	for _, path := range []string{"/health", "/api/health"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, path)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"), path)
		assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String(), path)
	}
}

func TestHealthNeedsNoToken(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
