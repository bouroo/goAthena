//go:build unit

package packet

import "testing"

// The inventory index convention is the project's highest-risk silent
// divergence: the whole e2e suite transmits index 1, which is the one value
// where a wrong offset happens to resolve to a valid row. These tests pin the
// convention against the rAthena source (clif.cpp:122-128) instead of against
// goAthena's own handlers, so a future off-by-one cannot pass both.
func TestInventoryIndexConvention(t *testing.T) {
	t.Parallel()

	// The offset itself is fixed by rathena/src/map/clif.cpp:122-128.
	if clientInventoryIndexOffset != 2 {
		t.Fatalf("clientInventoryIndexOffset = %d, want 2 (clif.cpp:122-128)", clientInventoryIndexOffset)
	}

	// Round-trip: every server row maps to a client index and back.
	for row := uint16(0); row < 200; row++ {
		if got := ServerIndex(ClientIndex(row)); got != row {
			t.Fatalf("ServerIndex(ClientIndex(%d)) = %d, want %d", row, got, row)
		}
	}

	// Pinned values: server row 0 is client index 2 (the client reserves 0/1).
	cases := []struct {
		serverRow   uint16
		clientIndex uint16
	}{
		{0, 2},
		{1, 3},
		{2, 4},
		{3, 5},
	}
	for _, tc := range cases {
		if got := ClientIndex(tc.serverRow); got != tc.clientIndex {
			t.Errorf("ClientIndex(%d) = %d, want %d", tc.serverRow, got, tc.clientIndex)
		}
		if got := ServerIndex(tc.clientIndex); got != tc.serverRow {
			t.Errorf("ServerIndex(%d) = %d, want %d", tc.clientIndex, got, tc.serverRow)
		}
	}
}

// A client index below the offset must NOT wrap into a small valid-looking row:
// it has to fail a `>= len(rows)` bound check. This is the property callers rely
// on, so it is asserted directly rather than assumed.
func TestServerIndexUnderflowFailsBoundsCheck(t *testing.T) {
	t.Parallel()

	for _, clientIndex := range []uint16{0, 1} {
		got := ServerIndex(clientIndex)
		// A 4-row inventory is the smallest realistic case; the wrapped value
		// must exceed it.
		if got < 4 {
			t.Errorf("ServerIndex(%d) = %d, want a value >= 4 so the bounds check rejects it", clientIndex, got)
		}
	}
}
