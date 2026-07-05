//go:build unit

package net

import (
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// buildLoginBurst builds a single-byte slice containing n consecutive
// CA_LOGIN packets (fixed 55 bytes), each with the cmd id patched in.
//
// Login DB has many packet types; we use HeaderCALOGIN because it has a
// stable 55-byte length. Each instance is filled with a unique byte so the
// bytes are not all-zero and the compiler cannot collapse them.
func buildLoginBurst(n int) []byte {
	const pktSize = 55 // sizeCALogin

	burst := make([]byte, 0, n*pktSize)
	pkt := make([]byte, pktSize)
	pkt[0] = byte(packet.HeaderCALOGIN)
	pkt[1] = byte(packet.HeaderCALOGIN >> 8)
	for i := 0; i < n; i++ {
		b := make([]byte, pktSize)
		copy(b, pkt)
		burst = append(burst, b...)
	}
	return burst
}

func BenchmarkNext_LoginBurst(b *testing.B) {
	const n = 200
	burst := buildLoginBurst(n)
	db := packet.NewLoginServerDB()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dec := NewLoginDecoder(db)
		dec.Feed(burst)
		for j := 0; j < n; j++ {
			if _, _, err := dec.Next(); err != nil {
				b.Fatalf("iter %d frame %d: %v", i, j, err)
			}
		}
	}
}

func BenchmarkNext_LoginBurstSingleDecoder(b *testing.B) {
	const n = 200
	burst := buildLoginBurst(n)
	db := packet.NewLoginServerDB()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dec := NewLoginDecoder(db)
		dec.Feed(burst)
		for j := 0; j < n; j++ {
			_, _, _ = dec.Next()
		}
	}
}
