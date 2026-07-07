//go:build unit

package packet

import "testing"

func TestNewMapServerDB_HasAllEntries(t *testing.T) {
	t.Parallel()

	db := NewMapServerDB()

	type expect struct {
		cmd       uint16
		name      string
		length    int
		direction Direction
	}
	// 2 C→S + 3 S→C, all fixed-length = 5 entries total.
	checks := []expect{
		{HeaderCZENTER, "CZ_ENTER", sizeCZEnter, DirectionClientToServer},
		{HeaderCZREQUESTMOVE, "CZ_REQUEST_MOVE", sizeCZRequestMove, DirectionClientToServer},

		{HeaderZCREFUSEENTER, "ZC_REFUSE_ENTER", sizeZCRefuseEnter, DirectionServerToClient},
		{HeaderZCACCEPTENTER, "ZC_ACCEPT_ENTER", sizeZCAcceptEnter, DirectionServerToClient},
		{HeaderZCNOTIFYPLAYERMOVE, "ZC_NOTIFY_PLAYERMOVE", sizeZCNotifyPlayerMove, DirectionServerToClient},
	}

	for _, c := range checks {
		def, ok := db.Lookup(c.cmd)
		if !ok {
			t.Errorf("Lookup(0x%04x) missing from map DB", c.cmd)
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

func TestNewMapServerDB_Size(t *testing.T) {
	t.Parallel()

	db := NewMapServerDB()
	// 2 C→S + 3 S→C = 5.
	const want = 5
	if db.Size() != want {
		t.Errorf("NewMapServerDB Size() = %d, want %d", db.Size(), want)
	}
}

func TestNewMapServerDB_LengthLookup(t *testing.T) {
	t.Parallel()

	db := NewMapServerDB()

	cases := []struct {
		cmd  uint16
		want int
	}{
		{HeaderCZENTER, sizeCZEnter},
		{HeaderCZREQUESTMOVE, sizeCZRequestMove},
		{HeaderZCACCEPTENTER, sizeZCAcceptEnter},
		{HeaderZCREFUSEENTER, sizeZCRefuseEnter},
		{HeaderZCNOTIFYPLAYERMOVE, sizeZCNotifyPlayerMove},
	}
	for _, c := range cases {
		got, ok := db.Length(c.cmd)
		if !ok {
			t.Errorf("Length(0x%04x) ok = false, want true", c.cmd)
			continue
		}
		if got != c.want {
			t.Errorf("Length(0x%04x) = %d, want %d", c.cmd, got, c.want)
		}
	}
}

func TestNewMapServerDB_NoDuplicateIDs(t *testing.T) {
	t.Parallel()

	db := NewMapServerDB()
	seen := make(map[uint16]struct{}, db.Size())
	for _, def := range mapDBEntries() {
		if _, dup := seen[def.ID]; dup {
			t.Errorf("duplicate ID 0x%04x (%s) in map DB", def.ID, def.Name)
		}
		seen[def.ID] = struct{}{}
	}
	if len(seen) != db.Size() {
		t.Errorf("distinct IDs (%d) != db.Size() (%d)", len(seen), db.Size())
	}
}

// mapDBEntries returns the entries NewMapServerDB registers, for
// invariant checking without exposing internals.
func mapDBEntries() []Definition {
	db := NewMapServerDB()
	out := make([]Definition, 0, db.Size())
	for _, def := range db.entries {
		out = append(out, def)
	}
	return out
}
