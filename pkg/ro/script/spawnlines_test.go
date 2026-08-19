package script

import (
	"reflect"
	"testing"
)

// Real corpus lines (npc/pre-re/mobs/fields/geffen.txt and payon-style MVP
// lines) prove the scanner: plain field spawn, box spawn with delays, boss,
// and non-spawn lines that must be skipped (comments, citycleaners,
// scripted blocks).
func TestParseSpawnLines_Corpus(t *testing.T) {
	src := []byte(`//===== rAthena Script ==================================
//= Geffen Fields Monster Spawn Script
// comment

gef_fild00,0,0	monster	Poring	1002,50
gef_fild00,0,0	monster	Fabre	1007,50
gef_fild00,54,212,5,5	monster	Green Plant	1080,3,360000,180000
pay_fild01,0,0	boss_monster	Eddga	1115,1,3600000,7200000
prontera,150,150	shop	Tool Dealer	84,501:50
prt_fild08,60,150,0,0	script	Cool Guy	105,{ mes "hi"; close; }
`)
	defs := ParseSpawnLines(src)
	want := []SpawnDef{
		{MapName: "gef_fild00", X: 0, Y: 0, Class: 1002, Name: "Poring", Amount: 50},
		{MapName: "gef_fild00", X: 0, Y: 0, Class: 1007, Name: "Fabre", Amount: 50},
		{MapName: "gef_fild00", X: 54, Y: 212, XSize: 5, YSize: 5, Class: 1080, Name: "Green Plant", Amount: 3, Delay1: 360000, Delay2: 180000},
		{MapName: "pay_fild01", X: 0, Y: 0, Class: 1115, Name: "Eddga", Amount: 1, Delay1: 3600000, Delay2: 7200000},
	}
	if !reflect.DeepEqual(defs, want) {
		t.Fatalf("defs =\n%+v\nwant\n%+v", defs, want)
	}
}

// A single-delay tail (d1 only) folds to Delay1 == Delay2; malformed tails
// are skipped, not errors.
func TestParseSpawnLines_DelayAndMalformed(t *testing.T) {
	src := []byte(`gef_fild01,10,20	monster	Roda Frog	1012,30,1200
bad line no tabs
x,y	monster	Foo	1
m,1,1	monster	Bar	notanumber,5
`)
	defs := ParseSpawnLines(src)
	want := []SpawnDef{{MapName: "gef_fild01", X: 10, Y: 20, Class: 1012, Name: "Roda Frog", Amount: 30, Delay1: 1200, Delay2: 1200}}
	if !reflect.DeepEqual(defs, want) {
		t.Fatalf("defs = %+v, want %+v", defs, want)
	}
}

func TestParseSpawnLines_Empty(t *testing.T) {
	if got := ParseSpawnLines(nil); got != nil {
		t.Fatalf("nil src = %+v, want nil", got)
	}
	if got := ParseSpawnLines([]byte("// only comments\n\n")); got != nil {
		t.Fatalf("comment-only src = %+v, want nil", got)
	}
}

// The NPC grammar must reject spawn lines (the reason this scanner exists),
// and the compiler must record placed-NPC placements on the set.
func TestCompile_NPCsRecorded_Placement(t *testing.T) {
	src := []byte("prontera,101,288,3\tscript\tShuger#pront\t98,{\n\tmes \"yo\";\n\tclose;\n}\n" +
		"-\tscript\tFloater\t111,{\n\tmes \"f\";\n\tclose;\n}\n")
	set, err := Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := []NPCDef{{Name: "Shuger#pront", MapName: "prontera", X: 101, Y: 288, Facing: 3, Sprite: 98}}
	if !reflect.DeepEqual(set.NPCs, want) {
		t.Fatalf("NPCs = %+v, want %+v", set.NPCs, want)
	}
}

// A shop NPC carries placement in ShopDef (existing behavior); no change
// expected there — this pins the seam the seeder will consume.
func TestCompile_ShopPlacementUnchanged(t *testing.T) {
	src := []byte("izlude,105,99,0\tshop\tButcher#iz\t54,517:-1\n")
	set, err := Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(set.Shops) != 1 || set.Shops[0].MapName != "izlude" || set.Shops[0].X != 105 {
		t.Fatalf("shops = %+v", set.Shops)
	}
	if len(set.NPCs) != 0 {
		t.Fatalf("shop must not add an NPC entry: %+v", set.NPCs)
	}
}
