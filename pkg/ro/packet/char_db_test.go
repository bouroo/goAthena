//go:build unit

package packet

import "testing"

func TestNewCharServerDB_HasAllEntries(t *testing.T) {
	t.Parallel()

	db := NewCharServerDB()

	type expect struct {
		cmd       uint16
		name      string
		length    int
		direction Direction
	}
	// 2 C→S fixed + 3 S→C (2 fixed, 1 variable) = 5 total entries.
	checks := []expect{
		{HeaderCHENTER, "CH_ENTER", sizeCHEnter, DirectionClientToServer},
		{HeaderCHSELECTCHAR, "CH_SELECT_CHAR", sizeCHSelectChar, DirectionClientToServer},

		{HeaderHCREFUSEENTER, "HC_REFUSE_ENTER", sizeHCRefuseEnter, DirectionServerToClient},
		{HeaderHCACCEPTENTER, "HC_ACCEPT_ENTER", VariableLength, DirectionServerToClient},
		{HeaderHCNOTIFYZONESVR, "HC_NOTIFY_ZONESVR", sizeHCNotifyZone, DirectionServerToClient},
	}

	for _, c := range checks {
		def, ok := db.Lookup(c.cmd)
		if !ok {
			t.Errorf("Lookup(0x%04x) missing from char DB", c.cmd)
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

func TestNewCharServerDB_Size(t *testing.T) {
	t.Parallel()

	db := NewCharServerDB()
	// 2 C→S + 3 S→C = 5.
	const want = 5
	if db.Size() != want {
		t.Errorf("NewCharServerDB Size() = %d, want %d", db.Size(), want)
	}
}

func TestNewCharServerDB_NoDuplicateIDs(t *testing.T) {
	t.Parallel()

	db := NewCharServerDB()
	seen := make(map[uint16]struct{}, db.Size())
	for _, def := range charDBEntries() {
		if _, dup := seen[def.ID]; dup {
			t.Errorf("duplicate ID 0x%04x (%s) in char DB", def.ID, def.Name)
		}
		seen[def.ID] = struct{}{}
	}
	if len(seen) != db.Size() {
		t.Errorf("distinct IDs (%d) != db.Size() (%d)", len(seen), db.Size())
	}
}

func TestNewCharServerDB_VariableLengthConvention(t *testing.T) {
	t.Parallel()

	db := NewCharServerDB()
	got, ok := db.Length(HeaderHCACCEPTENTER)
	if !ok {
		t.Fatalf("Length(HeaderHCACCEPTENTER) ok = false, want true")
	}
	if got != VariableLength {
		t.Errorf("Length(HeaderHCACCEPTENTER) = %d, want VariableLength (%d)", got, VariableLength)
	}
}

// charDBEntries returns the same entries NewCharServerDB registers, for
// invariant checking without exposing internals.
func charDBEntries() []Definition {
	db := NewCharServerDB()
	out := make([]Definition, 0, db.Size())
	for _, def := range db.entries {
		out = append(out, def)
	}
	return out
}

func TestDB_Merge(t *testing.T) {
	t.Parallel()

	loginDB := NewLoginServerDB()
	charDB := NewCharServerDB()

	// Merge into a fresh DB to prove the receiver is independent of the
	// sources.
	combined := NewDB()
	combined.Merge(loginDB)
	combined.Merge(charDB)

	// Login-side entries still findable.
	if def, ok := combined.Lookup(HeaderCALOGIN); !ok || def.Name != "CA_LOGIN" {
		t.Errorf("merged DB missing CA_LOGIN (ok=%v, def=%+v)", ok, def)
	}
	if def, ok := combined.Lookup(HeaderACREFUSELOGIN); !ok || def.Name != "AC_REFUSE_LOGIN" {
		t.Errorf("merged DB missing AC_REFUSE_LOGIN (ok=%v, def=%+v)", ok, def)
	}

	// Char-side entries findable.
	if def, ok := combined.Lookup(HeaderCHENTER); !ok || def.Name != "CH_ENTER" {
		t.Errorf("merged DB missing CH_ENTER (ok=%v, def=%+v)", ok, def)
	}
	if def, ok := combined.Lookup(HeaderHCNOTIFYZONESVR); !ok || def.Name != "HC_NOTIFY_ZONESVR" {
		t.Errorf("merged DB missing HC_NOTIFY_ZONESVR (ok=%v, def=%+v)", ok, def)
	}
	if def, ok := combined.Lookup(HeaderHCACCEPTENTER); !ok || def.Length != VariableLength {
		t.Errorf("merged DB HC_ACCEPT_ENTER lookup wrong (ok=%v, def=%+v)", ok, def)
	}

	// Source DBs are untouched after merging.
	if combined.Size() != loginDB.Size()+charDB.Size() {
		t.Errorf("combined Size() = %d, want login+char = %d", combined.Size(), loginDB.Size()+charDB.Size())
	}
}

func TestDB_Merge_OverwritesExistingEntry(t *testing.T) {
	t.Parallel()

	dst := NewDB()
	dst.Register(Definition{ID: HeaderCHENTER, Name: "STALE", Length: 999})

	src := NewDB()
	src.Register(Definition{ID: HeaderCHENTER, Name: "CH_ENTER", Length: sizeCHEnter})

	dst.Merge(src)

	got, ok := dst.Lookup(HeaderCHENTER)
	if !ok {
		t.Fatalf("Lookup after Merge ok = false, want true")
	}
	if got.Name != "CH_ENTER" {
		t.Errorf("Name = %q, want %q (Merge should overwrite like Register)", got.Name, "CH_ENTER")
	}
	if got.Length != sizeCHEnter {
		t.Errorf("Length = %d, want %d", got.Length, sizeCHEnter)
	}
}

func TestDB_Merge_DoesNotMutateSource(t *testing.T) {
	t.Parallel()

	// Merging must not destroy or alias the source DB (the gateway needs
	// to keep using the login and char DBs after combining them).
	src := NewDB()
	src.Register(Definition{ID: 0x1234, Name: "SRC", Length: 7})

	dst := NewDB()
	dst.Merge(src)

	if src.Size() != 1 {
		t.Errorf("source Size() after Merge = %d, want 1 (Merge must not clear source)", src.Size())
	}
	def, ok := src.Lookup(0x1234)
	if !ok || def.Name != "SRC" || def.Length != 7 {
		t.Errorf("source entry mutated: got %+v ok=%v", def, ok)
	}
}
