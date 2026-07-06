package assets

import (
	"fmt"

	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

// DecodeEUCKR converts EUC-KR encoded bytes to a UTF-8 string.
func DecodeEUCKR(b []byte) (string, error) {
	out, _, err := transform.String(korean.EUCKR.NewDecoder(), string(b))
	if err != nil {
		return "", fmt.Errorf("euc-kr decode: %w", err)
	}
	return out, nil
}

// EncodeEUCKR converts a UTF-8 string to EUC-KR encoded bytes.
func EncodeEUCKR(s string) ([]byte, error) {
	out, _, err := transform.Bytes(korean.EUCKR.NewEncoder(), []byte(s))
	if err != nil {
		return nil, fmt.Errorf("euc-kr encode: %w", err)
	}
	return out, nil
}
