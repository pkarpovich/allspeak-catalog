package manifest

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	reSHA256      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reForbidden   = regexp.MustCompile(`[^A-Za-z0-9._ -]`)
	errEmptyAfter = errors.New("empty after sanitization")
)

// FileRef is a single content-addressed file in a manifest.
type FileRef struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

// Track is an audio track: a FileRef plus presentation metadata.
type Track struct {
	Label     string `json:"label"`
	SortOrder int    `json:"sortOrder"`
	IsDefault bool   `json:"isDefault"`
	FileRef
}

// Manifest is the wire and storage shape of a session's files.
type Manifest struct {
	Tracks   []Track `json:"tracks"`
	Subtitle FileRef `json:"subtitle"`
}

// Validate enforces every finalize validation rule, returning an error that
// names the offending field.
func (m Manifest) Validate() error {
	if len(m.Tracks) == 0 {
		return errors.New("tracks: at least one track required")
	}
	defaults := 0
	for i, t := range m.Tracks {
		if strings.TrimSpace(t.Label) == "" {
			return fmt.Errorf("tracks[%d].label: must not be empty", i)
		}
		if t.IsDefault {
			defaults++
		}
		if err := t.Validate(); err != nil {
			return fmt.Errorf("tracks[%d].%w", i, err)
		}
	}
	if defaults != 1 {
		return fmt.Errorf("tracks: exactly one track must have isDefault=true, found %d", defaults)
	}
	if m.Subtitle == (FileRef{}) {
		return errors.New("subtitle: required")
	}
	if err := m.Subtitle.Validate(); err != nil {
		return fmt.Errorf("subtitle.%w", err)
	}
	return nil
}

// Validate checks a single file reference: sha256 is 64 lowercase hex chars,
// size is positive, and filename survives sanitization. The error names the
// offending field.
func (f FileRef) Validate() error {
	if !reSHA256.MatchString(f.SHA256) {
		return fmt.Errorf("sha256: must be 64 lowercase hex chars, got %q", f.SHA256)
	}
	if f.Size <= 0 {
		return fmt.Errorf("size: must be > 0, got %d", f.Size)
	}
	if _, err := SanitizeFilename(f.Filename); err != nil {
		return fmt.Errorf("filename: %w", err)
	}
	return nil
}

// Sanitize replaces every filename in the manifest with its sanitized form.
// It is the single API-boundary sanitization point for finalize: callers
// sanitize once here before validation, key computation, and storage, so read
// paths use the stored names verbatim. The error names the offending field.
func (m *Manifest) Sanitize() error {
	for i := range m.Tracks {
		clean, err := SanitizeFilename(m.Tracks[i].Filename)
		if err != nil {
			return fmt.Errorf("tracks[%d].filename: %w", i, err)
		}
		m.Tracks[i].Filename = clean
	}
	clean, err := SanitizeFilename(m.Subtitle.Filename)
	if err != nil {
		return fmt.Errorf("subtitle.filename: %w", err)
	}
	m.Subtitle.Filename = clean
	return nil
}

// Files returns every file in the manifest (tracks then subtitle) as a flat
// list for iteration by api and blob callers.
func (m Manifest) Files() []FileRef {
	files := make([]FileRef, 0, len(m.Tracks)+1)
	for _, t := range m.Tracks {
		files = append(files, t.FileRef)
	}
	return append(files, m.Subtitle)
}

// SanitizeFilename reduces name to its last path component, replaces every
// character outside [A-Za-z0-9._ -] with '_', and errors if nothing usable
// remains. It is idempotent: sanitizing a sanitized name is a no-op.
func SanitizeFilename(name string) (string, error) {
	base := path.Base(name)
	if base == "" || base == "." || base == "/" {
		return "", fmt.Errorf("%w: %q", errEmptyAfter, name)
	}
	return reForbidden.ReplaceAllString(base, "_"), nil
}
