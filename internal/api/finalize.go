package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/pkarpovich/allspeak-catalog/internal/blob"
	"github.com/pkarpovich/allspeak-catalog/internal/manifest"
	"github.com/pkarpovich/allspeak-catalog/internal/store"
)

const maxTitleLen = 200

type finalizeRequest struct {
	SessionID string            `json:"sessionId"`
	Title     string            `json:"title"`
	Manifest  manifest.Manifest `json:"manifest"`
}

type finalizeResponse struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeFinalize(w, r)
	if !ok {
		return
	}
	if _, err := uuid.Parse(req.SessionID); err != nil {
		s.writeError(w, http.StatusBadRequest, "sessionId: must be a valid uuid")
		return
	}
	if !s.verifyObjects(r.Context(), w, req.SessionID, req.Manifest) {
		return
	}
	session := store.Session{ID: req.SessionID, Title: req.Title, Manifest: req.Manifest}
	err := s.store.Create(r.Context(), session)
	if errors.Is(err, store.ErrExists) {
		s.writeError(w, http.StatusConflict, "session already exists")
		return
	}
	if err != nil {
		s.logger.Error("create session", "id", req.SessionID, "error", err)
		s.writeError(w, http.StatusInternalServerError, "create session")
		return
	}
	s.writeJSON(w, http.StatusOK, finalizeResponse{ID: req.SessionID, Revision: 1})
}

func (s *Server) updateSession(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeFinalize(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	_, err := s.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		s.logger.Error("get session", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "get session")
		return
	}
	if !s.verifyObjects(r.Context(), w, id, req.Manifest) {
		return
	}
	session := store.Session{ID: id, Title: req.Title, Manifest: req.Manifest}
	revision, err := s.store.UpdateManifest(r.Context(), session)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		s.logger.Error("update session", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "update session")
		return
	}
	s.writeJSON(w, http.StatusOK, finalizeResponse{ID: id, Revision: revision})
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.Delete(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		s.logger.Error("delete session", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "delete session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) decodeFinalize(w http.ResponseWriter, r *http.Request) (finalizeRequest, bool) {
	var req finalizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid json body")
		return finalizeRequest{}, false
	}
	if err := req.Manifest.Sanitize(); err != nil {
		s.writeError(w, http.StatusBadRequest, "manifest."+err.Error())
		return finalizeRequest{}, false
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		s.writeError(w, http.StatusBadRequest, "title: must not be empty")
		return finalizeRequest{}, false
	}
	if utf8.RuneCountInString(title) > maxTitleLen {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("title: must be at most %d chars", maxTitleLen))
		return finalizeRequest{}, false
	}
	req.Title = title
	if err := req.Manifest.Validate(); err != nil {
		s.writeError(w, http.StatusBadRequest, "manifest."+err.Error())
		return finalizeRequest{}, false
	}
	return req, true
}

func (s *Server) verifyObjects(ctx context.Context, w http.ResponseWriter, sessionID string, m manifest.Manifest) bool {
	var missing []string
	for _, f := range m.Files() {
		exists, err := s.blob.Exists(ctx, blob.Key(sessionID, f.SHA256, f.Filename))
		if err != nil {
			s.logger.Error("verify object", "sessionId", sessionID, "sha256", f.SHA256, "error", err)
			s.writeError(w, http.StatusBadGateway, "verify object")
			return false
		}
		if !exists {
			missing = append(missing, f.SHA256)
		}
	}
	if len(missing) > 0 {
		s.writeJSON(w, http.StatusConflict, map[string][]string{"missing": missing})
		return false
	}
	return true
}
