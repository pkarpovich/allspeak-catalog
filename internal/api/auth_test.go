package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAuth(t *testing.T) {
	s := newTestServer(t)

	tests := []struct {
		name       string
		level      authLevel
		header     string
		wantStatus int
		wantNext   bool
	}{
		{name: "read endpoint missing token", level: authRead, header: "", wantStatus: http.StatusUnauthorized},
		{name: "read endpoint unknown token", level: authRead, header: "Bearer nope", wantStatus: http.StatusUnauthorized},
		{name: "read endpoint malformed header", level: authRead, header: "Token " + readToken, wantStatus: http.StatusUnauthorized},
		{name: "read endpoint read token", level: authRead, header: "Bearer " + readToken, wantStatus: http.StatusOK, wantNext: true},
		{name: "read endpoint admin token", level: authRead, header: "Bearer " + adminToken, wantStatus: http.StatusOK, wantNext: true},
		{name: "admin endpoint missing token", level: authAdmin, header: "", wantStatus: http.StatusUnauthorized},
		{name: "admin endpoint unknown token", level: authAdmin, header: "Bearer nope", wantStatus: http.StatusUnauthorized},
		{name: "admin endpoint read token", level: authAdmin, header: "Bearer " + readToken, wantStatus: http.StatusForbidden},
		{name: "admin endpoint admin token", level: authAdmin, header: "Bearer " + adminToken, wantStatus: http.StatusOK, wantNext: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reached bool
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/resource", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			s.auth(tt.level, next).ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantNext, reached)
		})
	}
}
