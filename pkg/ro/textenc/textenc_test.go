//go:build unit

package textenc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodepageString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		c    Codepage
		want string
	}{
		{UTF8, "utf-8"},
		{CP874, "windows-874"},
		{EUCKR, "euc-kr"},
		{Codepage(99), "codepage(99)"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.c.String())
		})
	}
}

func TestParseCodepage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		want    Codepage
		wantErr bool
	}{
		// UTF8 family
		{"", UTF8, false},
		{"utf-8", UTF8, false},
		{"UTF-8", UTF8, false},
		{"Utf8", UTF8, false},
		{"utf8", UTF8, false},
		{"UTF8", UTF8, false},
		{"  utf-8  ", UTF8, false},

		// CP874 family
		{"windows-874", CP874, false},
		{"WINDOWS-874", CP874, false},
		{"cp874", CP874, false},
		{"CP874", CP874, false},
		{"tis-620", CP874, false},
		{"TIS-620", CP874, false},
		{"tis620", CP874, false},
		{"TIS620", CP874, false},

		// EUCKR family
		{"euc-kr", EUCKR, false},
		{"EUC-KR", EUCKR, false},
		{"euckr", EUCKR, false},
		{"EUCKR", EUCKR, false},

		// Unknown
		{"latin-1", 0, true},
		{"shift-jis", 0, true},
		{"garbage", 0, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCodepage(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, errUnknownCodepage)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestUTF8Passthrough(t *testing.T) {
	t.Parallel()

	in := []byte("Hello, สวัสดี, 안녕 — 🚀")
	decoded, err := UTF8.Decode(in)
	require.NoError(t, err)
	assert.Equal(t, string(in), decoded)

	encoded, err := UTF8.Encode(string(in))
	require.NoError(t, err)
	assert.Equal(t, in, encoded)
}

func TestASCIIPassthroughAll(t *testing.T) {
	t.Parallel()

	ascii := []byte("Hello, World! 0123456789")
	for _, c := range []Codepage{UTF8, CP874, EUCKR} {
		c := c
		t.Run(c.String(), func(t *testing.T) {
			t.Parallel()
			decoded, err := c.Decode(ascii)
			require.NoError(t, err)
			assert.Equal(t, string(ascii), decoded)

			encoded, err := c.Encode(string(ascii))
			require.NoError(t, err)
			assert.Equal(t, ascii, encoded)
		})
	}
}

func TestCP874RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []string{
		"สวัสดี",      // "hello" (transliterated)
		"ภาษาไทย",     // "Thai language"
		"ABC ผสม 123", // mixed ASCII + Thai
	}
	for _, original := range cases {
		original := original
		t.Run(original, func(t *testing.T) {
			t.Parallel()
			encoded, err := CP874.Encode(original)
			require.NoError(t, err)
			require.NotEmpty(t, encoded)

			decoded, err := CP874.Decode(encoded)
			require.NoError(t, err)
			assert.Equal(t, original, decoded)
		})
	}
}

// TestCP874KnownBytes asserts the exact CP874 byte sequence for the Thai word
// "สวัสดี". CP874 maps Thai U+0E01..U+0E5B to bytes 0xA1..0xFB, so each rune
// encodes to a single byte: byte = 0xA0 + (rune - 0xE00).
//
//	U+0E2A 'ส' = 0xCA
//	U+0E27 'ว' = 0xC7
//	U+0E31 'ั' = 0xD1  (o angsuan — sara am, NOT 0xB4)
//	U+0E2A 'ส' = 0xCA
//	U+0E14 'ด' = 0xB4
//	U+0E35 'ี' = 0xD5  (sara ii, NOT 0xB6)
//
// "สวัสดี" means "hello" in transliteration and is the canonical 6-rune
// fixture the gateway packet codec relies on.
func TestCP874KnownBytes(t *testing.T) {
	t.Parallel()

	original := "สวัสดี"
	want := []byte{0xCA, 0xC7, 0xD1, 0xCA, 0xB4, 0xD5} // ส ว ั ส ด ี

	encoded, err := CP874.Encode(original)
	require.NoError(t, err)
	assert.Equal(t, want, encoded, "CP874 must map each Thai rune to its single-byte slot 0xA1..0xFB")

	decoded, err := CP874.Decode(want)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
}

func TestEUCKRRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []string{
		"안녕",            // "hello"
		"안녕하세요, World!", // mixed
	}
	for _, original := range cases {
		original := original
		t.Run(original, func(t *testing.T) {
			t.Parallel()
			encoded, err := EUCKR.Encode(original)
			require.NoError(t, err)
			require.NotEmpty(t, encoded)

			decoded, err := EUCKR.Decode(encoded)
			require.NoError(t, err)
			assert.Equal(t, original, decoded)
		})
	}
}

func TestEUCKRKnownBytes(t *testing.T) {
	t.Parallel()

	original := "안녕"
	want := []byte{0xBE, 0xC8, 0xB3, 0xE7}

	encoded, err := EUCKR.Encode(original)
	require.NoError(t, err)
	assert.Equal(t, want, encoded)

	decoded, err := EUCKR.Decode(want)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
}

func TestEncodeUnrepresentable(t *testing.T) {
	t.Parallel()

	t.Run("korean into CP874", func(t *testing.T) {
		t.Parallel()
		// EUC-KR has 0xBE 0xC8 as a complete lead+trail pair (0xA1..0xFE);
		// pure-Hangul-fallback-replacing CP874 has no slot for these codepoints.
		_, err := CP874.Encode("안녕")
		require.Error(t, err)
	})

	t.Run("emoji into CP874", func(t *testing.T) {
		t.Parallel()
		_, err := CP874.Encode("hi 🚀")
		require.Error(t, err)
	})

	t.Run("emoji into EUCKR", func(t *testing.T) {
		t.Parallel()
		_, err := EUCKR.Encode("hi 🚀")
		require.Error(t, err)
	})

	t.Run("emoji into UTF8", func(t *testing.T) {
		t.Parallel()
		out, err := UTF8.Encode("hi 🚀")
		require.NoError(t, err)
		assert.Equal(t, []byte("hi 🚀"), out)
	})
}

func TestUnknownCodepageValue(t *testing.T) {
	t.Parallel()

	bad := Codepage(200)

	_, err := bad.Decode([]byte("hello"))
	require.Error(t, err)
	assert.ErrorIs(t, err, errUnknownCodepage)

	_, err = bad.Encode("hello")
	require.Error(t, err)
	assert.ErrorIs(t, err, errUnknownCodepage)

	assert.Equal(t, "codepage(200)", bad.String())
}

// TestDecodeRobust asserts that the library does not panic on arbitrary wire
// garbage. Windows-874 and EUC-KR decoders substitute U+FFFD for unmappable
// bytes rather than returning errors, so the contract under test is the
// no-panic guarantee and that callers receive a usable string back.
func TestDecodeRobust(t *testing.T) {
	t.Parallel()

	garbage := []byte{0x00, 0x80, 0xC0, 0xFF, 0xFE, 0x01, 0x7F}
	for _, c := range []Codepage{CP874, EUCKR} {
		c := c
		t.Run(c.String(), func(t *testing.T) {
			t.Parallel()
			out, err := c.Decode(garbage)
			require.NoError(t, err)
			assert.NotEmpty(t, out)
		})
	}
}

func TestEncodingFor(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, encodingFor(CP874))
	assert.NotNil(t, encodingFor(EUCKR))
	assert.Nil(t, encodingFor(UTF8))
	assert.Nil(t, encodingFor(Codepage(250)))
}

// TestEmptyInput asserts the empty-input fast-paths round-trip to empty for
// every codepage without touching the underlying transformer.
func TestEmptyInput(t *testing.T) {
	t.Parallel()

	for _, c := range []Codepage{UTF8, CP874, EUCKR} {
		c := c
		t.Run(c.String(), func(t *testing.T) {
			t.Parallel()

			decoded, err := c.Decode(nil)
			require.NoError(t, err)
			assert.Empty(t, decoded)

			decoded, err = c.Decode([]byte{})
			require.NoError(t, err)
			assert.Empty(t, decoded)

			encoded, err := c.Encode("")
			require.NoError(t, err)
			assert.Empty(t, encoded)
		})
	}
}
