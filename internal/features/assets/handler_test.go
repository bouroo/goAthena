//go:build unit

package assets

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type stubReader struct {
	files map[string][]byte
}

func (s *stubReader) ReadFile(name string) ([]byte, error) {
	if data, ok := s.files[name]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("not found")
}

func (s *stubReader) HasFile(name string) bool {
	_, ok := s.files[name]
	return ok
}

func newTestHandler(t *testing.T) (*AssetHandler, *stubReader) {
	t.Helper()
	logger := zerolog.New(io.Discard)
	stub := &stubReader{files: map[string][]byte{
		"data/sprite/abc.spr":   []byte("sprite-bytes"),
		"data/sprite/hero.png":  {0x89, 0x50, 0x4E, 0x47},
		"data/sprite/photo.jpg": {0xFF, 0xD8, 0xFF, 0xE0},
		"data/sprite/scene.bmp": []byte("BM"),
		"data/texture/t.tga":    []byte("TGA"),
		"data/lua/intro.lua":    []byte("print('hi')\n"),
		"data/lua/file.lub":     []byte("compressed"),
		"data/npc/notes.txt":    []byte("hello"),
		"data/table.xml":        []byte("<root/>"),
		"data/audio/song.mp3":   []byte("ID3"),
		"data/audio/song.ogg":   []byte("OggS"),
		"data/map/prontera.rsw": []byte("rsw-data"),
		"data/map/prontera.gnd": []byte("gnd-data"),
		"data/map/prontera.gat": []byte("gat-data"),
		"data/sprite/x.spr":     []byte("spr-data"),
		"data/sprite/y.act":     []byte("act-data"),
		"data/file.bin":         []byte("binary"),
		"data/v1.2/sprite.png":  {0x89, 0x50, 0x4E, 0x47},
	}}
	return NewAssetHandler(stub, logger), stub
}

func TestAssetHandler_OPTIONS_Preflight(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodOptions, "/assets/data/sprite/abc.spr", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Equal(t, "*", rr.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "GET, OPTIONS", rr.Header().Get("Access-Control-Allow-Methods"))
	require.Equal(t, "Content-Type", rr.Header().Get("Access-Control-Allow-Headers"))
}

func TestAssetHandler_CORS_Headers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{"not_found", "/assets/data/missing.bin"},
		{"root", "/assets/"},
		{"empty_path", "/assets"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, _ := newTestHandler(t)
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rr := httptest.NewRecorder()

			h.ServeHTTP(rr, req)

			require.Equal(t, "*", rr.Header().Get("Access-Control-Allow-Origin"))
			require.Equal(t, "GET, OPTIONS", rr.Header().Get("Access-Control-Allow-Methods"))
			require.Equal(t, "Content-Type", rr.Header().Get("Access-Control-Allow-Headers"))
		})
	}
}

func TestAssetHandler_FileNotFound(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/data/missing.bin", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
	require.Equal(t, "*", rr.Header().Get("Access-Control-Allow-Origin"))
}

func TestAssetHandler_Success(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/data/sprite/abc.spr", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "application/octet-stream", rr.Header().Get("Content-Type"))
	require.Equal(t, "public, max-age=86400", rr.Header().Get("Cache-Control"))
	require.Equal(t, "12", rr.Header().Get("Content-Length"))
	require.Equal(t, []byte("sprite-bytes"), rr.Body.Bytes())
}

func TestAssetHandler_ContentType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path    string
		wantCT  string
		wantLen string
	}{
		{"/assets/data/sprite/hero.png", "image/png", "4"},
		{"/assets/data/sprite/photo.jpg", "image/jpeg", "4"},
		{"/assets/data/sprite/scene.bmp", "image/bmp", "2"},
		{"/assets/data/texture/t.tga", "image/x-tga", "3"},
		{"/assets/data/lua/intro.lua", "text/plain; charset=utf-8", "12"},
		{"/assets/data/lua/file.lub", "text/plain; charset=utf-8", "10"},
		{"/assets/data/npc/notes.txt", "text/plain; charset=utf-8", "5"},
		{"/assets/data/table.xml", "text/xml; charset=utf-8", "7"},
		{"/assets/data/audio/song.mp3", "audio/mpeg", "3"},
		{"/assets/data/audio/song.ogg", "audio/ogg", "4"},
		{"/assets/data/map/prontera.rsw", "application/octet-stream", "8"},
		{"/assets/data/map/prontera.gnd", "application/octet-stream", "8"},
		{"/assets/data/map/prontera.gat", "application/octet-stream", "8"},
		{"/assets/data/sprite/x.spr", "application/octet-stream", "8"},
		{"/assets/data/sprite/y.act", "application/octet-stream", "8"},
		{"/assets/data/file.bin", "application/octet-stream", "6"},
		// directory with a dot must still resolve the file extension.
		{"/assets/data/v1.2/sprite.png", "image/png", "4"},
	}

	for _, tc := range cases {
		t.Run(strings.TrimPrefix(tc.path, "/assets/"), func(t *testing.T) {
			t.Parallel()

			h, _ := newTestHandler(t)
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rr := httptest.NewRecorder()

			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			require.Equal(t, tc.wantCT, rr.Header().Get("Content-Type"))
			require.Equal(t, tc.wantLen, rr.Header().Get("Content-Length"))
		})
	}
}

func TestAssetHandler_HEAD_NoBody(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodHead, "/assets/data/sprite/abc.spr", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Empty(t, rr.Body.Bytes())
	require.Equal(t, "12", rr.Header().Get("Content-Length"))
}

func TestAssetHandler_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/assets/data/sprite/abc.spr", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Equal(t, "GET, HEAD, OPTIONS", rr.Header().Get("Allow"))
	require.Equal(t, "*", rr.Header().Get("Access-Control-Allow-Origin"))
}

func TestAssetHandler_PathTraversalRejected(t *testing.T) {
	t.Parallel()

	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/data/../safe.bin", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestAssetHandler_PathTraversal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{"unix_relative_parent", "/assets/../../../etc/passwd"},
		{"unix_relative_parent_deep", "/assets/foo/../../../../etc/passwd"},
		{"windows_backslash", "/assets/..\\..\\etc\\passwd"},
		{"bare_dotdot", "/assets/.."},
		{"mixed_slash", "/assets/data/.././../../etc/shadow"},
		{"encoded_not_decoded", "/assets/%2e%2e/etc/passwd"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, _ := newTestHandler(t)
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rr := httptest.NewRecorder()

			h.ServeHTTP(rr, req)

			require.NotEqual(t, http.StatusOK, rr.Code,
				"traversal path %q must not return 200", tc.path)
			require.NotContains(t, rr.Body.String(), "root:x:",
				"traversal path %q leaked /etc/passwd contents", tc.path)
		})
	}
}

func TestAssetHandler_ReadError(t *testing.T) {
	t.Parallel()

	logger := zerolog.New(io.Discard)
	stub := &errorReader{err: errors.New("boom")}
	h := NewAssetHandler(stub, logger)

	req := httptest.NewRequest(http.MethodGet, "/assets/data/x.png", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Equal(t, "*", rr.Header().Get("Access-Control-Allow-Origin"))
}

type errorReader struct {
	err error
}

func (e *errorReader) ReadFile(_ string) ([]byte, error) {
	return nil, e.err
}

func (e *errorReader) HasFile(_ string) bool { return true }

func TestSanitizePath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in     string
		want   string
		wantOk bool
	}{
		{"/assets/", "", false},
		{"/assets", "", false},
		{"/assets/data/sprite/abc.spr", "data/sprite/abc.spr", true},
		{"/assets/data/../safe.bin", "", false},
		{"/assets/../../../etc/passwd", "", false},
		{"/assets/..", "", false},
		{"/other/data/x.png", "", false},
		{"/assets//double//slash.lua", "double/slash.lua", true},
		{"/assets/..\\..\\etc", "", false},
		{"/assets/data/v1.2/sprite.png", "data/v1.2/sprite.png", true},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got, ok := sanitizePath(tc.in)
			require.Equal(t, tc.wantOk, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestContentTypeFor(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a.PNG":  "image/png",
		"a.Jpg":  "image/jpeg",
		"a.JPEG": "image/jpeg",
		"a.bmp":  "image/bmp",
		"a.TGA":  "image/x-tga",
		"a.lua":  "text/plain; charset=utf-8",
		"a.LUB":  "text/plain; charset=utf-8",
		"a.txt":  "text/plain; charset=utf-8",
		"a.xml":  "text/xml; charset=utf-8",
		"a.mp3":  "audio/mpeg",
		"a.OGG":  "audio/ogg",
		"a.rsw":  "application/octet-stream",
		"a.gnd":  "application/octet-stream",
		"a.gat":  "application/octet-stream",
		"a.spr":  "application/octet-stream",
		"a.act":  "application/octet-stream",
		"a.bin":  "application/octet-stream",
		"a":      "application/octet-stream",
		// Dots inside directory segments must not affect extension parsing.
		"data/v1.2/sprite.png": "image/png",
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, want, contentTypeFor(name))
		})
	}
}
