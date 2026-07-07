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
	require.Equal(t, "*", rr.Header().Get("Access-Control-Allow-Origin"))
}

func TestAssetHandler_PathTraversalRejected(t *testing.T) {
	t.Parallel()

	h, stub := newTestHandler(t)
	stub.files["data/safe.bin"] = []byte("ok")

	req := httptest.NewRequest(http.MethodGet, "/assets/data/../safe.bin", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	// path.Clean collapses ../ → "safe.bin"; the stub doesn't have it
	// at that name, so we expect 404 (not 200).
	require.Equal(t, http.StatusNotFound, rr.Code)
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

func TestResolveGRFPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{"/assets/", ""},
		{"/assets", ""},
		{"/assets/data/sprite/abc.spr", "data/sprite/abc.spr"},
		{"/assets/data/../safe.bin", "safe.bin"},
		{"/other/data/x.png", ""},
		{"/assets//double//slash.lua", "double/slash.lua"},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, resolveGRFPath(tc.in))
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
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, want, contentTypeFor(name))
		})
	}
}
