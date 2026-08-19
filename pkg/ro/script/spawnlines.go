package script

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSpawnLines extracts monster-spawn definitions from rAthena mob-spawn
// script text (`npc/pre-re/mobs/**.txt`). These files are not NPC scripts:
// each data line is
//
//	map,x,y[,xs,ys]<TAB>monster|boss_monster|mini<TAB>Name<TAB>class,amount[,d1,d2]
//
// and the full NPC grammar rejects them, so this scanner runs as a fallback
// for files that fail Compile. Comment (`//`) and blank lines are skipped;
// lines that do not match are skipped rather than erroring — the corpus is
// large and heterogeneous, and a single odd line must not drop a file's
// spawns (mirrors the tolerant-loader doctrine in ADR-0003).
func ParseSpawnLines(src []byte) []SpawnDef {
	var defs []SpawnDef
	for _, line := range strings.Split(string(src), "\n") {
		if d, ok := parseSpawnLine(line); ok {
			defs = append(defs, d)
		}
	}
	return defs
}

// parseSpawnLine parses one spawn line. Validity requires: a placed
// `map,x,y[,xs,ys]` prefix, a monster-type word, a display name, and a
// `class,amount` tail (delays optional).
func parseSpawnLine(line string) (SpawnDef, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") {
		return SpawnDef{}, false
	}
	fields := strings.Split(trimmed, "\t")
	if len(fields) != 4 {
		return SpawnDef{}, false
	}
	switch fields[1] {
	case "monster", "boss_monster", "mini":
	default:
		return SpawnDef{}, false
	}

	parts := strings.Split(fields[0], ",")
	if len(parts) < 3 || len(parts) > 5 {
		return SpawnDef{}, false
	}
	x, err := atoi(parts[1])
	if err != nil {
		return SpawnDef{}, false
	}
	y, err := atoi(parts[2])
	if err != nil {
		return SpawnDef{}, false
	}
	var xs, ys int
	if len(parts) == 5 {
		xs, err = atoi(parts[3])
		if err != nil {
			return SpawnDef{}, false
		}
		ys, err = atoi(parts[4])
		if err != nil {
			return SpawnDef{}, false
		}
	}

	tail := strings.Split(fields[3], ",")
	if len(tail) < 2 || len(tail) > 4 {
		return SpawnDef{}, false
	}
	class, err := strconv.ParseInt(tail[0], 10, 32)
	if err != nil {
		return SpawnDef{}, false
	}
	amount, err := atoi(tail[1])
	if err != nil {
		return SpawnDef{}, false
	}
	var d1, d2 int
	if len(tail) >= 3 {
		d1, err = atoi(tail[2])
		if err != nil {
			return SpawnDef{}, false
		}
	}
	if len(tail) == 4 {
		d2, err = atoi(tail[3])
		if err != nil {
			return SpawnDef{}, false
		}
	} else if len(tail) == 3 {
		d2 = d1
	}

	// The name may carry a `,extra` never happens (4 fields exactly), but a
	// trailing `::label` scope on the name is legal; keep the raw name.
	return SpawnDef{
		MapName: parts[0], X: x, Y: y, XSize: xs, YSize: ys,
		Class: int32(class), Name: fields[2], Amount: amount,
		Delay1: d1, Delay2: d2,
	}, true
}

// atoi is strconv.Atoi folded with the "negative or malformed = invalid"
// contract this scanner wants (positions, amounts, delays are non-negative).
func atoi(s string) (int, error) {
	if s == "" {
		return 0, strconv.ErrSyntax
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, strconv.ErrSyntax
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	return n, nil
}
