//go:build unit

package packet

import (
	"testing"
)

// Benchmark DB.Lookup on a populated login-server database. Map lookup
// should be O(1), ~ns/op, 0 allocs.
func BenchmarkDB_Lookup(b *testing.B) {
	db := NewLoginServerDB()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = db.Lookup(HeaderCALOGIN)
	}
}

// Benchmark DB.Length on a populated login-server database.
func BenchmarkDB_Length(b *testing.B) {
	db := NewLoginServerDB()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = db.Length(HeaderCALOGIN)
	}
}

// Benchmark a Lookup miss to confirm the empty-branch path stays cheap.
func BenchmarkDB_LookupMiss(b *testing.B) {
	db := NewLoginServerDB()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = db.Lookup(0xFFFF)
	}
}

// Benchmark DB.Has across the full registered set. The gateway hot path
// will call Has/Length inside the per-packet dispatch loop.
func BenchmarkDB_Has(b *testing.B) {
	db := NewLoginServerDB()
	cmds := []uint16{
		HeaderCALOGIN,
		HeaderCAREQHASH,
		HeaderACACCEPTLOGIN,
		HeaderSCNOTIFYBAN,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.Has(cmds[i&3])
	}
}
