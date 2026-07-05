//go:build unit

package crypto

import "testing"

func BenchmarkObfuscator_Decode(b *testing.B) {
	obf := NewObfuscator(k0_20110817, k1_20110817, k2_20110817)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = obf.Decode(0x1234)
	}
}

func BenchmarkObfuscator_Encode(b *testing.B) {
	obf := NewObfuscator(k0_20110817, k1_20110817, k2_20110817)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = obf.Encode(0x1234)
	}
}

func BenchmarkFirstPacketDecode(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FirstPacketDecode(k0_20110817, k1_20110817, k2_20110817, 0x1234)
	}
}

func BenchmarkObfuscator_DisabledDecode(b *testing.B) {
	obf := NewObfuscator(0, 0, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = obf.Decode(0x1234)
	}
}

func BenchmarkKeysForVersion(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = KeysForVersion(20130807)
	}
}
