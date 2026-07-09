//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// P2A: inventory parse + encode round-trip tests. The exercises cover
// the per-packet wire shape (opcode, length, field offsets) and
// confirm that an Encode → Parse round-trip preserves the input
// values for every field the proto InventoryItem model surfaces.

func TestParseCZUseItem(t *testing.T) {
	t.Parallel()

	goodFrame := func() []byte {
		f := make([]byte, sizeCZUseItem2)
		writeLE16(f[0:], HeaderCZUSEITEM2)
		writeLE16(f[2:], 0x00AB) // inventory index
		writeLE32(f[4:], 0x00001092)
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CZUseItemRequest
	}{
		{
			name:    "valid frame",
			frame:   goodFrame,
			wantErr: false,
			want:    CZUseItemRequest{Index: 0x00AB, AID: 0x00001092},
		},
		{
			name:    "trailing bytes are ignored",
			frame:   append(goodFrame, 0x00, 0x00, 0x00),
			wantErr: false,
			want:    CZUseItemRequest{Index: 0x00AB, AID: 0x00001092},
		},
		{
			name:       "frame too short",
			frame:      goodFrame[:sizeCZUseItem2-1],
			wantErr:    true,
			wantErrSub: "want at least 8 bytes",
		},
		{
			name:       "wrong cmd",
			frame:      append([]byte(nil), append([]byte{0xff, 0xff}, goodFrame[2:]...)...),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCZUseItem(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCZUseItem err = nil, want error")
				}
				if tc.wantErrSub != "" && !contains(err.Error(), tc.wantErrSub) {
					t.Errorf("ParseCZUseItem err = %q, want substring %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCZUseItem err = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("ParseCZUseItem = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCZUseItemRequest_Encode(t *testing.T) {
	t.Parallel()

	r := CZUseItemRequest{Index: 0x0042, AID: 0x00001092}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()
	if len(got) != sizeCZUseItem2 {
		t.Fatalf("len = %d, want %d", len(got), sizeCZUseItem2)
	}
	if got[0] != 0x39 || got[1] != 0x04 {
		t.Errorf("header = %02x %02x, want 39 04 (LE 0x0439)", got[0], got[1])
	}
	if v := binary.LittleEndian.Uint16(got[2:]); v != 0x0042 {
		t.Errorf("index = 0x%04x, want 0x0042", v)
	}
	if v := binary.LittleEndian.Uint32(got[4:]); v != 0x00001092 {
		t.Errorf("AID = 0x%x, want 0x1092", v)
	}
}

func TestParseCZReqWearEquip(t *testing.T) {
	t.Parallel()

	goodFrame := func() []byte {
		f := make([]byte, sizeCZReqWearEquipV5)
		writeLE16(f[0:], HeaderCZREQWEAREQUIPV5)
		writeLE16(f[2:], 0x0010) // inventory index
		writeLE32(f[4:], 0x00000002)
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CZReqWearEquipRequest
	}{
		{
			name:    "valid frame",
			frame:   goodFrame,
			wantErr: false,
			want:    CZReqWearEquipRequest{Index: 0x0010, Position: 0x00000002},
		},
		{
			name:       "frame too short",
			frame:      goodFrame[:sizeCZReqWearEquipV5-1],
			wantErr:    true,
			wantErrSub: "want at least 8 bytes",
		},
		{
			name:       "wrong cmd",
			frame:      append([]byte(nil), append([]byte{0xff, 0xff}, goodFrame[2:]...)...),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCZReqWearEquip(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCZReqWearEquip err = nil, want error")
				}
				if tc.wantErrSub != "" && !contains(err.Error(), tc.wantErrSub) {
					t.Errorf("ParseCZReqWearEquip err = %q, want substring %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCZReqWearEquip err = %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseCZReqWearEquip = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCZReqWearEquipRequest_Encode(t *testing.T) {
	t.Parallel()

	r := CZReqWearEquipRequest{Index: 0x0010, Position: 0x00000002}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	got := buf.Bytes()
	if len(got) != sizeCZReqWearEquipV5 {
		t.Fatalf("len = %d, want %d", len(got), sizeCZReqWearEquipV5)
	}
	if got[0] != 0x98 || got[1] != 0x09 {
		t.Errorf("header = %02x %02x, want 98 09 (LE 0x0998)", got[0], got[1])
	}
	if v := binary.LittleEndian.Uint16(got[2:]); v != 0x0010 {
		t.Errorf("index = 0x%04x, want 0x0010", v)
	}
	if v := binary.LittleEndian.Uint32(got[4:]); v != 0x00000002 {
		t.Errorf("position = 0x%x, want 0x2", v)
	}
}

func TestParseCZReqTakeoffEquip(t *testing.T) {
	t.Parallel()

	goodFrame := func() []byte {
		f := make([]byte, sizeCZReqTakeoffEquip)
		writeLE16(f[0:], HeaderCZREQTAKEOFFEQUIP)
		writeLE16(f[2:], 0x0011)
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CZReqTakeoffEquipRequest
	}{
		{
			name:    "valid frame",
			frame:   goodFrame,
			wantErr: false,
			want:    CZReqTakeoffEquipRequest{Index: 0x0011},
		},
		{
			name:       "frame too short",
			frame:      goodFrame[:sizeCZReqTakeoffEquip-1],
			wantErr:    true,
			wantErrSub: "want at least 4 bytes",
		},
		{
			name:       "wrong cmd",
			frame:      append([]byte(nil), append([]byte{0xff, 0xff}, goodFrame[2:]...)...),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCZReqTakeoffEquip(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCZReqTakeoffEquip err = nil, want error")
				}
				if tc.wantErrSub != "" && !contains(err.Error(), tc.wantErrSub) {
					t.Errorf("ParseCZReqTakeoffEquip err = %q, want substring %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCZReqTakeoffEquip err = %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseCZReqTakeoffEquip = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCZReqTakeoffEquipRequest_Encode(t *testing.T) {
	t.Parallel()

	r := CZReqTakeoffEquipRequest{Index: 0x0011}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	got := buf.Bytes()
	if len(got) != sizeCZReqTakeoffEquip {
		t.Fatalf("len = %d, want %d", len(got), sizeCZReqTakeoffEquip)
	}
	if got[0] != 0xab || got[1] != 0x00 {
		t.Errorf("header = %02x %02x, want ab 00 (LE 0x00ab)", got[0], got[1])
	}
	if v := binary.LittleEndian.Uint16(got[2:]); v != 0x0011 {
		t.Errorf("index = 0x%04x, want 0x0011", v)
	}
}

func TestInventoryListNormalResponse_Encode(t *testing.T) {
	t.Parallel()

	resp := InventoryListNormalResponse{
		Items: []InventoryNormalItem{
			{
				Index: 2,
				ITID:  0x0AB, // 171 = Red Potion
				Type:  0,     // IT_HEALING
				Count: 10,
				Flag:  0x01, // IsIdentified
			},
			{
				Index: 3,
				ITID:  0x0C8, // 200 = Blue Potion
				Type:  0,
				Count: 5,
				Flag:  0x01,
			},
		},
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	got := buf.Bytes()

	const wantLen = 4 + 2*sizeNormalItem
	if len(got) != wantLen {
		t.Fatalf("len = %d, want %d", len(got), wantLen)
	}
	// Opcode = 0x00a3 LE.
	if got[0] != 0xa3 || got[1] != 0x00 {
		t.Errorf("header = %02x %02x, want a3 00", got[0], got[1])
	}
	// packetLength = wantLen.
	if plen := binary.LittleEndian.Uint16(got[2:]); plen != wantLen {
		t.Errorf("packetLength = %d, want %d", plen, wantLen)
	}

	// First item: index=2, ITID=0xAB, type=0, count=10, WearState=0,
	// then 4*uint16 cards (all zero), then HireExpireDate=0,
	// bindOnEquipType=0, Flag=0x01.
	off := 4
	if v := binary.LittleEndian.Uint16(got[off:]); v != 2 {
		t.Errorf("item[0].index = %d, want 2", v)
	}
	if v := binary.LittleEndian.Uint16(got[off+2:]); v != 0xAB {
		t.Errorf("item[0].ITID = 0x%x, want 0xAB", v)
	}
	if v := got[off+4]; v != 0 {
		t.Errorf("item[0].type = %d, want 0", v)
	}
	if v := binary.LittleEndian.Uint16(got[off+5:]); v != 10 {
		t.Errorf("item[0].count = %d, want 10", v)
	}
	if v := binary.LittleEndian.Uint32(got[off+7:]); v != 0 {
		t.Errorf("item[0].WearState = %d, want 0", v)
	}
	if v := got[off+25]; v != 0x01 {
		t.Errorf("item[0].Flag = 0x%02x, want 0x01", v)
	}

	// Second item at the second 26-byte slot.
	off = 4 + sizeNormalItem
	if v := binary.LittleEndian.Uint16(got[off:]); v != 3 {
		t.Errorf("item[1].index = %d, want 3", v)
	}
	if v := binary.LittleEndian.Uint16(got[off+2:]); v != 0xC8 {
		t.Errorf("item[1].ITID = 0x%x, want 0xC8", v)
	}
	if v := binary.LittleEndian.Uint16(got[off+5:]); v != 5 {
		t.Errorf("item[1].count = %d, want 5", v)
	}
}

func TestInventoryListNormalResponse_EmptyIsFourBytes(t *testing.T) {
	t.Parallel()

	var resp InventoryListNormalResponse
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	if len(buf.Bytes()) != 4 {
		t.Errorf("empty len = %d, want 4", len(buf.Bytes()))
	}
	if buf.Bytes()[0] != 0xa3 || buf.Bytes()[1] != 0x00 {
		t.Errorf("empty header = %02x %02x, want a3 00", buf.Bytes()[0], buf.Bytes()[1])
	}
}

func TestInventoryListEquipResponse_Encode(t *testing.T) {
	t.Parallel()

	resp := InventoryListEquipResponse{
		Items: []InventoryEquipItem{
			{
				Index:            9,
				ITID:             0x0538, // 1336 = Knife
				Type:             3,      // IT_WEAPON
				Location:         0x0002, // EQP_HAND_R
				RefiningLevel:    0,
				ItemSpriteNumber: 1,
				Flag:             0x01, // IsIdentified
			},
		},
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	got := buf.Bytes()

	const wantLen = 4 + sizeEquipItem
	if len(got) != wantLen {
		t.Fatalf("len = %d, want %d", len(got), wantLen)
	}
	// Opcode = 0x00a4 LE.
	if got[0] != 0xa4 || got[1] != 0x00 {
		t.Errorf("header = %02x %02x, want a4 00", got[0], got[1])
	}
	if plen := binary.LittleEndian.Uint16(got[2:]); plen != wantLen {
		t.Errorf("packetLength = %d, want %d", plen, wantLen)
	}

	off := 4
	if v := binary.LittleEndian.Uint16(got[off:]); v != 9 {
		t.Errorf("item.index = %d, want 9", v)
	}
	if v := binary.LittleEndian.Uint16(got[off+2:]); v != 0x0538 {
		t.Errorf("item.ITID = 0x%x, want 0x0538", v)
	}
	if v := got[off+4]; v != 3 {
		t.Errorf("item.type = %d, want 3", v)
	}
	if v := binary.LittleEndian.Uint32(got[off+5:]); v != 0x0002 {
		t.Errorf("item.location = 0x%x, want 0x2", v)
	}
	if v := binary.LittleEndian.Uint32(got[off+9:]); v != 0 {
		t.Errorf("item.WearState = %d, want 0", v)
	}
	if v := got[off+13]; v != 0 {
		t.Errorf("item.RefiningLevel = %d, want 0", v)
	}
	if v := binary.LittleEndian.Uint16(got[off+28:]); v != 1 {
		t.Errorf("item.ItemSpriteNumber = %d, want 1", v)
	}
	if v := got[off+30]; v != 0 {
		t.Errorf("item.OptionCount = %d, want 0", v)
	}
	if v := got[off+56]; v != 0x01 {
		t.Errorf("item.Flag = 0x%02x, want 0x01", v)
	}
}

func TestInventoryListEquipResponse_EmptyIsFourBytes(t *testing.T) {
	t.Parallel()

	var resp InventoryListEquipResponse
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	if len(buf.Bytes()) != 4 {
		t.Errorf("empty len = %d, want 4", len(buf.Bytes()))
	}
	if buf.Bytes()[0] != 0xa4 || buf.Bytes()[1] != 0x00 {
		t.Errorf("empty header = %02x %02x, want a4 00", buf.Bytes()[0], buf.Bytes()[1])
	}
}

func TestReqWearEquipAckResponse_Encode(t *testing.T) {
	t.Parallel()

	r := ReqWearEquipAckResponse{
		Index:            0x000B,
		WearLocation:     0x00000002,
		ItemSpriteNumber: 1,
		Result:           1,
	}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	got := buf.Bytes()
	if len(got) != sizeZCReqWearEquipAckV5 {
		t.Fatalf("len = %d, want %d", len(got), sizeZCReqWearEquipAckV5)
	}
	if got[0] != 0x99 || got[1] != 0x09 {
		t.Errorf("header = %02x %02x, want 99 09 (LE 0x0999)", got[0], got[1])
	}
	if v := binary.LittleEndian.Uint16(got[2:]); v != 0x000B {
		t.Errorf("index = 0x%04x, want 0x000B", v)
	}
	if v := binary.LittleEndian.Uint32(got[4:]); v != 0x00000002 {
		t.Errorf("wearLocation = 0x%x, want 0x2", v)
	}
	if v := binary.LittleEndian.Uint16(got[8:]); v != 1 {
		t.Errorf("sprite = %d, want 1", v)
	}
	if v := got[10]; v != 1 {
		t.Errorf("result = %d, want 1", v)
	}
}

func TestReqTakeoffEquipAckResponse_Encode(t *testing.T) {
	t.Parallel()

	r := ReqTakeoffEquipAckResponse{
		Index:        0x000B,
		WearLocation: 0x00000002,
		Flag:         0, // 0 = success (inverted for PACKETVER >= 20110824)
	}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	got := buf.Bytes()
	if len(got) != sizeZCReqTakeoffEquipAck {
		t.Fatalf("len = %d, want %d", len(got), sizeZCReqTakeoffEquipAck)
	}
	if got[0] != 0x9a || got[1] != 0x09 {
		t.Errorf("header = %02x %02x, want 9a 09 (LE 0x099a)", got[0], got[1])
	}
	if v := binary.LittleEndian.Uint16(got[2:]); v != 0x000B {
		t.Errorf("index = 0x%04x, want 0x000B", v)
	}
	if v := binary.LittleEndian.Uint32(got[4:]); v != 0x00000002 {
		t.Errorf("wearLocation = 0x%x, want 0x2", v)
	}
	if v := got[8]; v != 0 {
		t.Errorf("flag = %d, want 0 (success on the wire)", v)
	}
}

func TestUseItemAck2Response_Encode(t *testing.T) {
	t.Parallel()

	r := UseItemAck2Response{
		Index:  4, // server index 2 + 2 (clif.cpp:4482)
		ItemID: 0x0AB,
		AID:    0x00001092,
		Amount: 9,
		Result: 1,
	}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	got := buf.Bytes()
	if len(got) != sizeZCUseItemAck2 {
		t.Fatalf("len = %d, want %d", len(got), sizeZCUseItemAck2)
	}
	if got[0] != 0xc8 || got[1] != 0x01 {
		t.Errorf("header = %02x %02x, want c8 01 (LE 0x01c8)", got[0], got[1])
	}
	if v := binary.LittleEndian.Uint16(got[2:]); v != 4 {
		t.Errorf("index = %d, want 4", v)
	}
	if v := binary.LittleEndian.Uint16(got[4:]); v != 0x0AB {
		t.Errorf("itemId = 0x%x, want 0xAB", v)
	}
	if v := binary.LittleEndian.Uint32(got[6:]); v != 0x00001092 {
		t.Errorf("AID = 0x%x, want 0x1092", v)
	}
	if v := binary.LittleEndian.Uint16(got[10:]); v != 9 {
		t.Errorf("amount = %d, want 9", v)
	}
	if v := got[12]; v != 1 {
		t.Errorf("result = %d, want 1", v)
	}
}

// contains is a substring helper local to this test file. Re-declared
// here rather than imported to avoid a new testutil dep.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
