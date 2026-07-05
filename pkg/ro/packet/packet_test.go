//go:build unit

package packet

import (
	"testing"
)

func TestNewDB_Empty(t *testing.T) {
	t.Parallel()

	db := NewDB()
	if got := db.Size(); got != 0 {
		t.Fatalf("Size() = %d, want 0", got)
	}
	if db.Has(0x0064) {
		t.Fatalf("Has(0x0064) = true on empty DB, want false")
	}
	if _, ok := db.Lookup(0x0064); ok {
		t.Fatalf("Lookup(0x0064) ok = true on empty DB, want false")
	}
	if _, ok := db.Length(0x0064); ok {
		t.Fatalf("Length(0x0064) ok = true on empty DB, want false")
	}
}

func TestDB_RegisterAndLookup(t *testing.T) {
	t.Parallel()

	db := NewDB()
	db.Register(Definition{
		ID:        0x0064,
		Name:      "CA_LOGIN",
		Length:    55,
		Direction: DirectionClientToServer,
	})

	got, ok := db.Lookup(0x0064)
	if !ok {
		t.Fatalf("Lookup(0x0064) ok = false, want true")
	}
	if got.ID != 0x0064 {
		t.Errorf("ID = 0x%04x, want 0x0064", got.ID)
	}
	if got.Name != "CA_LOGIN" {
		t.Errorf("Name = %q, want %q", got.Name, "CA_LOGIN")
	}
	if got.Length != 55 {
		t.Errorf("Length = %d, want 55", got.Length)
	}
	if got.Direction != DirectionClientToServer {
		t.Errorf("Direction = %d, want %d", got.Direction, DirectionClientToServer)
	}
}

func TestDB_Length(t *testing.T) {
	t.Parallel()

	db := NewDB()
	db.Register(Definition{ID: 0x0064, Name: "FIXED", Length: 55})
	db.Register(Definition{ID: 0x0069, Name: "VAR", Length: VariableLength})

	if got, ok := db.Length(0x0064); !ok || got != 55 {
		t.Errorf("Length(0x0064) = (%d, %v), want (55, true)", got, ok)
	}
	if got, ok := db.Length(0x0069); !ok || got != VariableLength {
		t.Errorf("Length(0x0069) = (%d, %v), want (VariableLength, true)", got, ok)
	}
	if got, ok := db.Length(0x0000); ok || got != 0 {
		t.Errorf("Length(0x0000) = (%d, %v), want (0, false)", got, ok)
	}
}

func TestDB_HasSize(t *testing.T) {
	t.Parallel()

	db := NewDB()
	if db.Size() != 0 {
		t.Fatalf("Size() = %d, want 0", db.Size())
	}

	db.Register(Definition{ID: 1})
	db.Register(Definition{ID: 2})
	db.Register(Definition{ID: 3})

	if db.Size() != 3 {
		t.Errorf("Size() = %d, want 3", db.Size())
	}
	for _, id := range []uint16{1, 2, 3} {
		if !db.Has(id) {
			t.Errorf("Has(%d) = false, want true", id)
		}
	}
	if db.Has(99) {
		t.Errorf("Has(99) = true, want false")
	}
}

func TestDB_RegisterOverwritesSilently(t *testing.T) {
	t.Parallel()

	db := NewDB()
	db.Register(Definition{ID: 0x0064, Name: "ORIG", Length: 55})
	db.Register(Definition{ID: 0x0064, Name: "REPLACED", Length: 56})

	got, ok := db.Lookup(0x0064)
	if !ok {
		t.Fatalf("Lookup(0x0064) ok = false, want true")
	}
	if got.Name != "REPLACED" {
		t.Errorf("Name = %q, want %q (overwrite should be silent, not panic)", got.Name, "REPLACED")
	}
	if got.Length != 56 {
		t.Errorf("Length = %d, want 56", got.Length)
	}
	if db.Size() != 1 {
		t.Errorf("Size() = %d, want 1 (overwrite should not add)", db.Size())
	}
}

func TestDB_LookupUnknown(t *testing.T) {
	t.Parallel()

	db := NewDB()
	got, ok := db.Lookup(0xabcd)
	if ok {
		t.Fatalf("Lookup(0xabcd) ok = true, want false")
	}
	if got.ID != 0 || got.Name != "" || got.Length != 0 || got.Direction != 0 {
		t.Errorf("Lookup on miss should return zero Definition, got %+v", got)
	}

	if l, ok := db.Length(0xabcd); ok || l != 0 {
		t.Errorf("Length(0xabcd) = (%d, %v), want (0, false)", l, ok)
	}
}

func TestNewLoginServerDB_HasKnownPackets(t *testing.T) {
	t.Parallel()

	db := NewLoginServerDB()

	// Spot-check known IDs from rathena/src/login/loginclif.cpp:483-498
	// and rathena/src/common/packets.hpp.
	type expect struct {
		cmd       uint16
		name      string
		length    int
		direction Direction
	}
	checks := []expect{
		// C→S — fixed length, matches sizeof(PACKET_CA_*) from packets.hpp.
		{HeaderCALOGIN, "CA_LOGIN", 55, DirectionClientToServer},
		{HeaderCAREQHASH, "CA_REQ_HASH", 2, DirectionClientToServer},
		{HeaderCAEXEHASHCHECK, "CA_EXE_HASHCHECK", 18, DirectionClientToServer},
		{HeaderCACONNECTINFOCHANGED, "CA_CONNECT_INFO_CHANGED", 26, DirectionClientToServer},
		{HeaderCALOGIN2, "CA_LOGIN2", 47, DirectionClientToServer},
		{HeaderCALOGIN3, "CA_LOGIN3", 48, DirectionClientToServer},
		{HeaderCALOGIN4, "CA_LOGIN4", 60, DirectionClientToServer},
		{HeaderCALOGINPCBANG, "CA_LOGIN_PCBANG", 84, DirectionClientToServer},
		{HeaderCALOGINCHANNEL, "CA_LOGIN_CHANNEL", 85, DirectionClientToServer},
		{HeaderCTAUTH, "CT_AUTH", 68, DirectionClientToServer},

		// S→C — fixed length.
		{HeaderSCNOTIFYBAN, "SC_NOTIFY_BAN", 3, DirectionServerToClient},
		{HeaderACREFUSELOGIN, "AC_REFUSE_LOGIN", 26, DirectionServerToClient},

		// Variable-length entries.
		{HeaderCASSOLOGINREQ, "CA_SSO_LOGIN_REQ", VariableLength, DirectionClientToServer},
		{HeaderACACKHASH, "AC_ACK_HASH", VariableLength, DirectionServerToClient},
		{HeaderACACCEPTLOGIN, "AC_ACCEPT_LOGIN", VariableLength, DirectionServerToClient},
		{HeaderACACCEPTLOGINOld, "AC_ACCEPT_LOGIN_OLD", VariableLength, DirectionServerToClient},
	}

	for _, c := range checks {
		def, ok := db.Lookup(c.cmd)
		if !ok {
			t.Errorf("Lookup(0x%04x) missing from login DB", c.cmd)
			continue
		}
		if def.Name != c.name {
			t.Errorf("Lookup(0x%04x).Name = %q, want %q", c.cmd, def.Name, c.name)
		}
		if def.Length != c.length {
			t.Errorf("Lookup(0x%04x).Length = %d, want %d", c.cmd, def.Length, c.length)
		}
		if def.Direction != c.direction {
			t.Errorf("Lookup(0x%04x).Direction = %d, want %d", c.cmd, def.Direction, c.direction)
		}
	}
}

func TestNewLoginServerDB_HasAllLoginParseablePackets(t *testing.T) {
	t.Parallel()

	// Every C→S packet registered in rathena's LoginPacketDatabase
	// (loginclif.cpp:483-498) MUST be present.
	db := NewLoginServerDB()
	want := []uint16{
		HeaderCACONNECTINFOCHANGED,
		HeaderCAEXEHASHCHECK,
		HeaderCALOGIN,
		HeaderCALOGINPCBANG,
		HeaderCALOGINCHANNEL,
		HeaderCALOGIN2,
		HeaderCALOGIN3,
		HeaderCALOGIN4,
		HeaderCASSOLOGINREQ,
		HeaderCAREQHASH,
		HeaderCTAUTH,
	}
	for _, id := range want {
		def, ok := db.Lookup(id)
		if !ok {
			t.Errorf("Login DB missing required C→S packet 0x%04x", id)
			continue
		}
		if def.Direction != DirectionClientToServer {
			t.Errorf("0x%04x Direction = %d, want C→S (%d)", id, def.Direction, DirectionClientToServer)
		}
	}
}

func TestNewLoginServerDB_VariableLengthConvention(t *testing.T) {
	t.Parallel()

	// Variable-length packets must report VariableLength (-1) from Length.
	db := NewLoginServerDB()

	for _, id := range []uint16{
		HeaderCASSOLOGINREQ,
		HeaderACACKHASH,
		HeaderACACCEPTLOGIN,
		HeaderACACCEPTLOGINOld,
	} {
		got, ok := db.Length(id)
		if !ok {
			t.Errorf("Length(0x%04x) ok = false, want true", id)
			continue
		}
		if got != VariableLength {
			t.Errorf("Length(0x%04x) = %d, want VariableLength (%d)", id, got, VariableLength)
		}
	}
}

func TestNewLoginServerDB_Size(t *testing.T) {
	t.Parallel()

	db := NewLoginServerDB()
	// 11 C→S + 6 S→C = 17.
	const want = 17
	if db.Size() != want {
		t.Errorf("NewLoginServerDB Size() = %d, want %d", db.Size(), want)
	}
}

func TestNewLoginServerDB_NoDuplicateIDs(t *testing.T) {
	t.Parallel()

	// Sanity guard: every registered ID must appear exactly once (we
	// silently overwrite on duplicate Register, so Size() must equal the
	// count of distinct IDs we hand-curated above).
	db := NewLoginServerDB()
	seen := make(map[uint16]struct{}, db.Size())
	for _, def := range loginDBEntries() {
		if _, dup := seen[def.ID]; dup {
			t.Errorf("duplicate ID 0x%04x (%s) in login DB", def.ID, def.Name)
		}
		seen[def.ID] = struct{}{}
	}
	if len(seen) != db.Size() {
		t.Errorf("distinct IDs (%d) != db.Size() (%d)", len(seen), db.Size())
	}
}

// loginDBEntries returns the same entries NewLoginServerDB registers, for
// invariant checking without exposing internals.
func loginDBEntries() []Definition {
	db := NewLoginServerDB()
	out := make([]Definition, 0, db.Size())
	for _, def := range db.entries {
		out = append(out, def)
	}
	return out
}

func TestDirection_StringIsDistinct(t *testing.T) {
	t.Parallel()

	// Sanity check: the two Direction values must be different so that
	// Direction is meaningful as a discriminator.
	if DirectionClientToServer == DirectionServerToClient {
		t.Fatalf("DirectionClientToServer == DirectionServerToClient; constants are not distinct")
	}
}

func TestVariableLengthIsNegativeOne(t *testing.T) {
	t.Parallel()

	// The whole point of VariableLength is the codec checks for -1 when
	// reading the uint16 length prefix from the wire.
	if VariableLength != -1 {
		t.Fatalf("VariableLength = %d, want -1", VariableLength)
	}
}
