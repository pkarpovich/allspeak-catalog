package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

type authLevel int

const (
	authRead authLevel = iota
	authAdmin
)

func (s *Server) auth(level authLevel, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := s.bearerToken(r)
		if !ok {
			s.writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		switch {
		case s.tokenMatches(token, s.admin):
			// admin token satisfies every level
		case level == authRead && s.tokenMatches(token, s.read):
			// read token is accepted on read endpoints
		case s.tokenMatches(token, s.read):
			s.writeError(w, http.StatusForbidden, "read token cannot access admin endpoint")
			return
		default:
			s.writeError(w, http.StatusUnauthorized, "unknown bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

func (s *Server) tokenMatches(provided, expected string) bool {
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}
