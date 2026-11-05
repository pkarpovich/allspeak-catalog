package api

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pkarpovich/allspeak-catalog/internal/blob"
	"github.com/pkarpovich/allspeak-catalog/internal/store"
)

// MaxDiagnosticsSize caps one diagnostics upload. The bytes pass through the
// server (unlike session files) so the phone can upload with a single
// background request and the cap can be enforced.
const MaxDiagnosticsSize = 8 << 20

const crashPrefix = "diagnostics/crashes/"

var diagnosticsName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$`)

type diagnosticsFile struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	UploadedAt time.Time `json:"uploadedAt"`
	URL        string    `json:"url"`
}

type diagnosticsList struct {
	Files        []diagnosticsFile `json:"files"`
	URLsExpireAt time.Time         `json:"urlsExpireAt"`
}

type diagnosticsStored struct {
	Key  string `json:"key"`
	Size int    `json:"size"`
}

func sessionDiagnosticsPrefix(id string) string {
	return "diagnostics/" + id + "/"
}

func (s *Server) uploadSessionDiagnostics(w http.ResponseWriter, r *http.Request) {
	id, ok := s.knownSession(w, r)
	if !ok {
		return
	}
	s.storeDiagnostics(w, r, sessionDiagnosticsPrefix(id), "application/x-ndjson")
}

func (s *Server) uploadCrashDiagnostics(w http.ResponseWriter, r *http.Request) {
	s.storeDiagnostics(w, r, crashPrefix, "application/json")
}

func (s *Server) listSessionDiagnostics(w http.ResponseWriter, r *http.Request) {
	id, ok := s.knownSession(w, r)
	if !ok {
		return
	}
	s.listDiagnostics(w, r, sessionDiagnosticsPrefix(id))
}

func (s *Server) listCrashDiagnostics(w http.ResponseWriter, r *http.Request) {
	s.listDiagnostics(w, r, crashPrefix)
}

func (s *Server) knownSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := strings.ToLower(r.PathValue("id"))
	_, err := s.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "session not found")
		return "", false
	}
	if err != nil {
		s.logger.Error("get session", "id", id, "error", err)
		s.writeError(w, http.StatusInternalServerError, "get session")
		return "", false
	}
	return id, true
}

func (s *Server) storeDiagnostics(w http.ResponseWriter, r *http.Request, prefix, contentType string) {
	name := r.URL.Query().Get("name")
	if !diagnosticsName.MatchString(name) {
		s.writeError(w, http.StatusBadRequest, "name: must match [A-Za-z0-9._-]{1,128} and not start with a dot")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxDiagnosticsSize))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		s.writeError(w, http.StatusRequestEntityTooLarge, "body: larger than 8 MB")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "body: read failed")
		return
	}
	if len(body) == 0 {
		s.writeError(w, http.StatusBadRequest, "body: empty")
		return
	}
	key := prefix + name
	if err := s.blob.Put(r.Context(), key, contentType, body); err != nil {
		s.logger.Error("store diagnostics", "key", key, "error", err)
		s.writeError(w, http.StatusBadGateway, "store object")
		return
	}
	s.writeJSON(w, http.StatusCreated, diagnosticsStored{Key: key, Size: len(body)})
}

func (s *Server) listDiagnostics(w http.ResponseWriter, r *http.Request, prefix string) {
	objects, err := s.blob.List(r.Context(), prefix)
	if err != nil {
		s.logger.Error("list diagnostics", "prefix", prefix, "error", err)
		s.writeError(w, http.StatusBadGateway, "list objects")
		return
	}
	slices.SortFunc(objects, func(a, b blob.Object) int {
		return b.LastModified.Compare(a.LastModified)
	})
	files := make([]diagnosticsFile, 0, len(objects))
	for _, o := range objects {
		name := strings.TrimPrefix(o.Key, prefix)
		if strings.Contains(name, "/") {
			continue
		}
		url, err := s.blob.PresignGet(r.Context(), o.Key)
		if err != nil {
			s.logger.Error("presign diagnostics", "key", o.Key, "error", err)
			s.writeError(w, http.StatusBadGateway, "presign object url")
			return
		}
		files = append(files, diagnosticsFile{
			Name:       name,
			Size:       o.Size,
			UploadedAt: o.LastModified.UTC(),
			URL:        url,
		})
	}
	s.writeJSON(w, http.StatusOK, diagnosticsList{
		Files:        files,
		URLsExpireAt: time.Now().UTC().Add(blob.PresignExpiry),
	})
}
