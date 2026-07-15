package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/pkarpovich/allspeak-catalog/internal/blob"
	"github.com/pkarpovich/allspeak-catalog/internal/manifest"
	"github.com/pkarpovich/allspeak-catalog/internal/store"
)

type catalogItem struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Revision    int       `json:"revision"`
	UpdatedAt   time.Time `json:"updatedAt"`
	TotalSize   int64     `json:"totalSize"`
	TrackLabels []string  `json:"trackLabels"`
}

func newCatalogItem(session store.Session) catalogItem {
	var total int64
	for _, f := range session.Manifest.Files() {
		total += f.Size
	}
	labels := make([]string, 0, len(session.Manifest.Tracks))
	for _, t := range session.Manifest.Tracks {
		labels = append(labels, t.Label)
	}
	return catalogItem{
		ID:          session.ID,
		Title:       session.Title,
		Revision:    session.Revision,
		UpdatedAt:   session.UpdatedAt,
		TotalSize:   total,
		TrackLabels: labels,
	}
}

type fileWithURL struct {
	manifest.FileRef
	URL string `json:"url"`
}

type trackWithURL struct {
	Label     string `json:"label"`
	SortOrder int    `json:"sortOrder"`
	IsDefault bool   `json:"isDefault"`
	fileWithURL
}

type sessionDetail struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Revision     int            `json:"revision"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	Tracks       []trackWithURL `json:"tracks"`
	Subtitle     fileWithURL    `json:"subtitle"`
	URLsExpireAt time.Time      `json:"urlsExpireAt"`
}

func (s *Server) listCatalog(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.List(r.Context())
	if err != nil {
		s.logger.Error("list catalog", "error", err)
		s.writeError(w, http.StatusInternalServerError, "list catalog")
		return
	}
	items := make([]catalogItem, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, newCatalogItem(session))
	}
	s.writeJSON(w, http.StatusOK, map[string][]catalogItem{"sessions": items})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		s.logger.Error("get session", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "get session")
		return
	}
	detail, err := s.sessionDetail(r.Context(), session)
	if err != nil {
		s.logger.Error("presign session files", "id", id, "error", err)
		s.writeError(w, http.StatusBadGateway, "presign object url")
		return
	}
	s.writeJSON(w, http.StatusOK, detail)
}

func (s *Server) sessionDetail(ctx context.Context, session store.Session) (sessionDetail, error) {
	tracks := make([]trackWithURL, 0, len(session.Manifest.Tracks))
	for _, t := range session.Manifest.Tracks {
		url, err := s.presignFile(ctx, session.ID, t.FileRef)
		if err != nil {
			return sessionDetail{}, err
		}
		tracks = append(tracks, trackWithURL{
			Label:       t.Label,
			SortOrder:   t.SortOrder,
			IsDefault:   t.IsDefault,
			fileWithURL: fileWithURL{FileRef: t.FileRef, URL: url},
		})
	}
	subtitleURL, err := s.presignFile(ctx, session.ID, session.Manifest.Subtitle)
	if err != nil {
		return sessionDetail{}, err
	}
	return sessionDetail{
		ID:           session.ID,
		Title:        session.Title,
		Revision:     session.Revision,
		CreatedAt:    session.CreatedAt,
		UpdatedAt:    session.UpdatedAt,
		Tracks:       tracks,
		Subtitle:     fileWithURL{FileRef: session.Manifest.Subtitle, URL: subtitleURL},
		URLsExpireAt: time.Now().UTC().Add(blob.PresignExpiry),
	}, nil
}

func (s *Server) presignFile(ctx context.Context, sessionID string, f manifest.FileRef) (string, error) {
	return s.blob.PresignGet(ctx, blob.Key(sessionID, f.SHA256, f.Filename))
}
