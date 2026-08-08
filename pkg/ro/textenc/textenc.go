// Package textenc transcodes Ragnarok Online single-byte wire-format text to and from Go UTF-8 strings.
//
// The RO client wire format is single-byte (CP874 for Thai, EUC-KR for Korean);
// goAthena stores and processes everything as UTF-8. Every packet-codec boundary
// that touches character names, chat, or NPC text must run through this package.
package textenc

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/korean"
)

// errUnknownCodepage is the sentinel returned (wrapped) for any unrecognized
// codepage value or alias. Callers can match it with errors.Is.
var errUnknownCodepage = errors.New("unknown codepage")

// Codepage selects how wire bytes map to UTF-8 strings. UTF8 (zero value) is
// passthrough; CP874 and EUCKR delegate to golang.org/x/text.
type Codepage uint8

const (
	// UTF8 treats bytes as already UTF-8 encoded (used by web/roBrowser clients).
	UTF8 Codepage = 0
	// CP874 is Windows-874 / TIS-620, used by Thai Classic clients.
	CP874 Codepage = 1
	// EUCKR is used by Korean clients.
	EUCKR Codepage = 2
)

// encodingFor returns the x/text Encoding for the codepage, or nil for unknown.
func encodingFor(c Codepage) encoding.Encoding {
	switch c {
	case CP874:
		return charmap.Windows874
	case EUCKR:
		return korean.EUCKR
	default:
		return nil
	}
}

// String returns the canonical IANA-style MIME label for the codepage.
func (c Codepage) String() string {
	switch c {
	case UTF8:
		return "utf-8"
	case CP874:
		return "windows-874"
	case EUCKR:
		return "euc-kr"
	default:
		return fmt.Sprintf("codepage(%d)", uint8(c))
	}
}

// ParseCodepage parses an operator-facing alias into a Codepage. Comparison is
// case-insensitive. The empty string is treated as UTF8 (sensible default).
func ParseCodepage(s string) (Codepage, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "utf-8", "utf8":
		return UTF8, nil
	case "windows-874", "cp874", "tis-620", "tis620":
		return CP874, nil
	case "euc-kr", "euckr":
		return EUCKR, nil
	default:
		return 0, fmt.Errorf("textenc: parse codepage %q: %w", s, errUnknownCodepage)
	}
}

// Decode transcodes wire-format bytes into a UTF-8 string. UTF8 is passthrough
// and performs no validation — callers may feed already-UTF-8 web bytes.
func (c Codepage) Decode(b []byte) (string, error) {
	switch c {
	case UTF8:
		return string(b), nil
	case CP874, EUCKR:
		if len(b) == 0 {
			return "", nil
		}
		out, err := encodingFor(c).NewDecoder().Bytes(b)
		if err != nil {
			return "", fmt.Errorf("textenc: decode %s: %w", c, err)
		}
		return string(out), nil
	default:
		return "", fmt.Errorf("textenc: decode %w: %d", errUnknownCodepage, uint8(c))
	}
}

// Encode transcodes a UTF-8 string into wire-format bytes. UTF8 is passthrough.
// Characters that cannot be represented in the target codepage produce a
// wrapped error; they are never silently dropped.
func (c Codepage) Encode(s string) ([]byte, error) {
	switch c {
	case UTF8:
		return []byte(s), nil
	case CP874, EUCKR:
		if len(s) == 0 {
			return []byte{}, nil
		}
		out, err := encodingFor(c).NewEncoder().Bytes([]byte(s))
		if err != nil {
			return nil, fmt.Errorf("textenc: encode %s: %w", c, err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("textenc: encode %w: %d", errUnknownCodepage, uint8(c))
	}
}
