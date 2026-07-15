package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/pkarpovich/allspeak-catalog/internal/blob"
	"github.com/pkarpovich/allspeak-catalog/internal/manifest"
)

type uploadRequest struct {
	SessionID string             `json:"sessionId"`
	Files     []manifest.FileRef `json:"files"`
}

type uploadFileResult struct {
	SHA256    string `json:"sha256"`
	Filename  string `json:"filename"`
	Exists    bool   `json:"exists,omitempty"`
	UploadURL string `json:"uploadUrl,omitempty"`
}

type uploadResponse struct {
	SessionID string             `json:"sessionId"`
	Files     []uploadFileResult `json:"files"`
}

func (s *Server) negotiateUploads(w http.ResponseWriter, r *http.Request) {
	var req uploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(req.Files) == 0 {
		s.writeError(w, http.StatusBadRequest, "files: at least one file required")
		return
	}
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	results := make([]uploadFileResult, 0, len(req.Files))
	for i := range req.Files {
		f := req.Files[i]
		clean, err := manifest.SanitizeFilename(f.Filename)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("files[%d].filename: %s", i, err))
			return
		}
		f.Filename = clean
		if err := f.Validate(); err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("files[%d].%s", i, err))
			return
		}
		result, err := s.negotiateFile(r.Context(), sessionID, f)
		if err != nil {
			s.logger.Error("negotiate upload", "sessionId", sessionID, "sha256", f.SHA256, "error", err)
			s.writeError(w, http.StatusBadGateway, "negotiate upload")
			return
		}
		results = append(results, result)
	}
	s.writeJSON(w, http.StatusOK, uploadResponse{SessionID: sessionID, Files: results})
}

func (s *Server) negotiateFile(ctx context.Context, sessionID string, f manifest.FileRef) (uploadFileResult, error) {
	key := blob.Key(sessionID, f.SHA256, f.Filename)
	exists, err := s.blob.Exists(ctx, key)
	if err != nil {
		return uploadFileResult{}, err
	}
	result := uploadFileResult{SHA256: f.SHA256, Filename: f.Filename}
	if exists {
		result.Exists = true
		return result, nil
	}
	url, err := s.blob.PresignPut(ctx, key)
	if err != nil {
		return uploadFileResult{}, err
	}
	result.UploadURL = url
	return result, nil
}
