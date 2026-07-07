package assets

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
)

// FileReader is the read interface for the asset handler. GRFSet
// implements it; tests can substitute a stub.
type FileReader interface {
	ReadFile(name string) ([]byte, error)
	HasFile(name string) bool
}

// AssetHandler is an http.Handler that serves files from a FileReader
// (typically a GRFSet). Mount at /assets/ ; the remaining path after
// the prefix is the GRF file path (e.g., /assets/data/sprite/abc.spr
// resolves to the GRF entry "data/sprite/abc.spr").
//
// CORS headers (Access-Control-Allow-Origin: *) are set on every
// response, including errors, so roBrowser can fetch assets from a
// Remote Client across origins without preflight rejection.
type AssetHandler struct {
	set    FileReader
	logger zerolog.Logger
}

// NewAssetHandler constructs an AssetHandler that reads from set. The
// logger is enriched with the "assets" component tag.
func NewAssetHandler(set FileReader, logger zerolog.Logger) *AssetHandler {
	return &AssetHandler{
		set:    set,
		logger: logger.With().Str("component", "assets").Logger(),
	}
}

// ServeHTTP dispatches asset requests. All responses carry CORS
// headers; OPTIONS short-circuits to 204 for preflight.
func (h *AssetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.setCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name, ok := sanitizePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if !h.set.HasFile(name) {
		h.logger.Debug().Str("path", name).Msg("asset not found")
		http.NotFound(w, r)
		return
	}

	data, err := h.set.ReadFile(name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error().Err(err).Str("path", name).Msg("asset read failed")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	ctype := contentTypeFor(name)
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", assetMaxAgeSeconds))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	if _, err := w.Write(data); err != nil { // #nosec G705 -- binary asset payload from GRF, not user-submitted HTML
		h.logger.Warn().Err(err).Str("path", name).Msg("asset write failed")
	}
}

// setCORS writes the CORS response headers used by every reply.
func (h *AssetHandler) setCORS(w http.ResponseWriter) {
	hdr := w.Header()
	hdr.Set("Access-Control-Allow-Origin", "*")
	hdr.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	hdr.Set("Access-Control-Allow-Headers", "Content-Type")
}

// sanitizePath strips the /assets/ mount prefix and returns a cleaned
// GRF-relative path. The second return value is false when the request
// targets the mount root itself, when the prefix is missing, or when
// the path contains a directory-traversal segment (".." or "..\").
//
// Any ".." sequence anywhere in the path is rejected outright so the
// value cannot be used to escape the archive namespace regardless of
// downstream normalization quirks.
func sanitizePath(rawPath string) (string, bool) {
	const prefix = "/assets/"
	if !strings.HasPrefix(rawPath, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(rawPath, prefix)
	rest = strings.TrimPrefix(rest, "/")

	if strings.Contains(rest, "..") {
		return "", false
	}

	cleaned := path.Clean(rest)
	if cleaned == "." || cleaned == "/" || cleaned == "" {
		return "", false
	}
	return cleaned, true
}

// contentTypeFor returns a Content-Type header value for the GRF
// file extension. Unknown extensions fall through to
// application/octet-stream so the browser still caches them.
func contentTypeFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".bmp":
		return "image/bmp"
	case ".tga":
		return "image/x-tga"
	case ".lua", ".lub":
		return "text/plain; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".xml":
		return "text/xml; charset=utf-8"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".rsw", ".gnd", ".gat", ".spr", ".act":
		return "application/octet-stream"
	default:
		return "application/octet-stream"
	}
}

// assetMaxAgeSeconds is the Cache-Control max-age sent with successful
// responses. Exposed as a constant for tests.
const assetMaxAgeSeconds = 86400
