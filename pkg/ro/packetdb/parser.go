package packetdb

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Entry is a single packet definition parsed from clif_packetdb.hpp.
//
// Each entry carries the version gate that controls whether it is included
// in a flattened *packet.DB for a given PACKETVER. Entries outside any
// version gate have MinVersion == 0 (always included). Entries inside a
// gate carry the gate's effective MinVersion (the threshold of the active
// branch's predicate) so the flattener can apply per-entry filtering if a
// future caller asks for it; ForPacketVer currently consults the parsed
// predicate directly because the source uses disjunctions.
type Entry struct {
	ID         uint16 // command ID as it appears on the wire (e.g. 0x0064)
	Name       string // derived from the hex ID (e.g. "0x0064")
	Length     int    // fixed on-wire length, or packet.VariableLength (-1)
	MinVersion int    // minimum PACKETVER (0 = always active; set from the enclosing #if when present)
	Predicate  string // raw predicate for the enclosing branch, "" if always active
}

// frame is one level of the parse-time #if/#elif/#else/#endif branch
// stack. We only need to remember the predicate string for the currently
// selected branch so the registry flattener can evaluate it later; the
// rule that a later #elif replaces the active predicate is captured by
// the parser overwriting stack[len-1].predicate.
type frame struct {
	predicate string
}

// ParseStats counts entries and skip decisions encountered while parsing.
// It is reported alongside the entry list so callers can confirm that no
// entries were silently lost and so the integration test can record
// baseline numbers.
type ParseStats struct {
	// Entries is the number of numeric direct-form entries successfully
	// parsed into Entry records.
	Entries int
	// Symbolic is the number of packet/parseable_packet lines skipped
	// because they used HEADER_*, sizeof(...), or *Type identifiers in
	// the command position. Deferred to N1.1.
	Symbolic int
	// IfBlocks is the number of #if blocks processed (excluding #ifndef
	// preamble guards). Each block contributes one MinVersion gate to the
	// entries inside it.
	IfBlocks int
}

// ParseFile reads rAthena's clif_packetdb.hpp from path and returns the
// list of direct-numeric packet definitions together with parse
// statistics. The parser is permissive: a line it does not understand is
// skipped, never treated as fatal. Unsupported forms are counted by
// ParseStats.Symbolic.
func ParseFile(path string) ([]Entry, ParseStats, error) {
	f, err := os.Open(path) //nolint:gosec // path is operator-controlled (third_party/rathena checked-in copy)
	if err != nil {
		return nil, ParseStats{}, fmt.Errorf("packetdb: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return parse(io.Reader(f))
}

// parseString is a convenience for unit tests. The linter sees it as
// unused because the only callers live in *_test.go files that are
// conditionally compiled under the "unit" build tag, which golangci-lint
// does not analyze by default.
func parseString(src string) ([]Entry, ParseStats, error) { //nolint:unused // exercised by unit-tagged tests
	return parse(strings.NewReader(src))
}

func parse(r io.Reader) ([]Entry, ParseStats, error) {
	var entries []Entry
	var st ParseStats
	// branchStack tracks every active #if / #ifdef / #ifndef frame so
	// that #endif pops in order and nested predicates accumulate. The
	// stack records the *enclosing predicate string*; the registry
	// flattener evaluates each entry's Predicate against the target
	// PACKETVER (parse-time version evaluation against an unknown
	// version is meaningless for the source's >= predicates).
	var stack []frame
	// ifOpenRe matches an opening conditional: #if, #ifdef, or #ifndef.
	// The optional directive is captured so the handler can distinguish
	// preamble (#ifndef) from gate (#if / #ifdef). The trailing word
	// boundary prevents false matches on unrelated directives like
	// #include or #define.
	ifOpenRe := regexp.MustCompile(`^\s*#\s*if(?:def|ndef)?\b(.*)$`)

	sc := bufio.NewScanner(r)
	// rAthena's clif_packetdb.hpp has long lines (the parseable_packet
	// calls with trailing offsets), so grow the scanner buffer.
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)

	for sc.Scan() {
		raw := sc.Text()
		// Drop everything to the right of "//" (rAthena uses C++-style
		// // comments, not /* */). Be careful: we do not want to split a
		// predicate expression. The "//" token never appears inside the
		// rAthena predicates we handle, so a naive split is safe and
		// matches the corpus-style strip we use elsewhere.
		if i := strings.Index(raw, "//"); i >= 0 {
			raw = raw[:i]
		}
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}

		if handled, err := handleDirective(trimmed, raw, ifOpenRe, &stack, &st); handled {
			if err != nil {
				return nil, st, err
			}
			continue
		}
		if isBranchKeyword(trimmed) {
			if err := applyBranchKeyword(trimmed, &stack); err != nil {
				return nil, st, err
			}
			continue
		}
		// Packet definition line.
		entry, kind, ok := parsePacketLine(trimmed)
		if !ok {
			continue
		}
		if kind == packetKindSymbolic {
			st.Symbolic++
			continue
		}
		entry.Predicate = activePredicate(stack)
		if entry.Predicate != "" {
			entry.MinVersion = minVersionFor(entry.Predicate)
		}
		entries = append(entries, entry)
		st.Entries++
	}
	if err := sc.Err(); err != nil {
		return nil, st, fmt.Errorf("packetdb: read: %w", err)
	}
	if len(stack) != 0 {
		return nil, st, fmt.Errorf("packetdb: %d unclosed #if block(s)", len(stack))
	}
	return entries, st, nil
}

// handleDirective recognizes an #if / #ifdef / #ifndef line. Returns
// handled=true when the line was a directive (whether or not it also
// errored). The raw line is preserved so error messages can quote the
// original source.
func handleDirective(trimmed, raw string, ifOpenRe *regexp.Regexp, stack *[]frame, st *ParseStats) (bool, error) {
	m := ifOpenRe.FindStringSubmatch(trimmed)
	if m == nil {
		return false, nil
	}
	rest := strings.TrimSpace(m[1])
	// Distinguish the three forms by the keyword that follows "#if".
	switch {
	case strings.HasPrefix(trimmed, "#ifndef"):
		// Preamble guard (e.g. #ifndef CLIF_PACKETDB_HPP). Push a frame
		// so the matching #endif at EOF pops correctly; do NOT count
		// toward IfBlocks because the preamble is not a packet gate.
		*stack = append(*stack, frame{predicate: ""})
	case strings.HasPrefix(trimmed, "#ifdef"):
		// #ifdef is not used by rAthena's clif_packetdb.hpp for packet
		// gating — treat the block as always-active (no predicate to
		// evaluate) and count it as a regular #if block so the registry
		// sees the gate. Operators who introduce #ifdef gates will get a
		// block whose predicate always matches.
		*stack = append(*stack, frame{predicate: ""})
		st.IfBlocks++
	default:
		// Plain #if.
		if err := ValidatePredicate(rest); err != nil {
			return true, fmt.Errorf("packetdb: line: %q: %w", raw, err)
		}
		st.IfBlocks++
		*stack = append(*stack, frame{predicate: rest})
	}
	return true, nil
}

// isBranchKeyword reports whether trimmed starts with one of the
// branch-control keywords (#elif, #else, #endif). The actual handling
// is in applyBranchKeyword so the cyclomatic complexity of parse stays
// bounded.
func isBranchKeyword(trimmed string) bool {
	switch {
	case strings.HasPrefix(trimmed, "#elif"),
		strings.HasPrefix(trimmed, "#else"),
		strings.HasPrefix(trimmed, "#endif"):
		return true
	}
	return false
}

// applyBranchKeyword applies an #elif / #else / #endif to the branch
// stack, returning an error if the keyword is not well-nested.
func applyBranchKeyword(trimmed string, stack *[]frame) error {
	switch {
	case strings.HasPrefix(trimmed, "#elif"):
		if len(*stack) == 0 {
			return errors.New("packetdb: #elif without matching #if")
		}
		tail := strings.TrimSpace(trimmed[len("#elif"):])
		if err := ValidatePredicate(tail); err != nil {
			return fmt.Errorf("packetdb: #elif %q: %w", tail, err)
		}
		// Record the elif predicate as the active candidate on the top
		// frame. The flattener evaluates it at ForPacketVer time. We
		// keep the LAST #elif predicate seen on the top frame because
		// it is the latest candidate branch.
		(*stack)[len(*stack)-1].predicate = tail
	case strings.HasPrefix(trimmed, "#else"):
		if len(*stack) == 0 {
			return errors.New("packetdb: #else without matching #if")
		}
		(*stack)[len(*stack)-1].predicate = "true /*else*/"
	case strings.HasPrefix(trimmed, "#endif"):
		if len(*stack) == 0 {
			return errors.New("packetdb: #endif without matching #if")
		}
		*stack = (*stack)[:len(*stack)-1]
	}
	return nil
}

// packetLineRe matches a direct-numeric packet/parseable_packet line and
// captures the command ID, the length, and (for parseable_packet) the
// remaining offset arguments. Trailing whitespace and comments are
// stripped by the caller.
var packetLineRe = regexp.MustCompile(
	`^(?:` +
		`packet\(\s*(0x[0-9A-Fa-f]+)\s*,\s*(-?[0-9]+)\s*\)` +
		`|` +
		`parseable_packet\(\s*(0x[0-9A-Fa-f]+)\s*,\s*(-?[0-9]+)\s*,[^)]*\)` +
		`)\s*;?\s*$`,
)

// packetKind distinguishes the parse outcomes for a packet-shaped line.
type packetKind int

const (
	packetKindNone packetKind = iota
	packetKindDirect
	packetKindSymbolic
)

// parsePacketLine extracts a direct-numeric packet entry from a single
// (already comment-stripped, trimmed) line. The boolean return is true
// when the line is a packet/parseable_packet definition of *any* kind,
// even if it cannot be resolved to numeric IDs (kind == packetKindSymbolic).
//
// Forms accepted:
//   - packet(0xNNNN,LEN);
//   - parseable_packet(0xNNNN,LEN,clif_parse_*,...);
//
// Any other form (HEADER_*, sizeof(...), *Type) is classified symbolic
// and reported by ParseStats.
func parsePacketLine(line string) (Entry, packetKind, bool) {
	m := packetLineRe.FindStringSubmatch(line)
	if m != nil {
		var hexStr, lenStr string
		switch {
		case m[1] != "":
			hexStr, lenStr = m[1], m[2]
		case m[3] != "":
			hexStr, lenStr = m[3], m[4]
		}
		id64, err := strconv.ParseUint(hexStr, 0, 16)
		if err != nil {
			return Entry{}, packetKindNone, true // matched the regex but hex is malformed
		}
		length, err := strconv.Atoi(lenStr)
		if err != nil {
			return Entry{}, packetKindNone, true
		}
		entry := Entry{
			ID:   uint16(id64),
			Name: fmt.Sprintf("0x%04x", id64),
		}
		if length == -1 {
			entry.Length = -1
		} else {
			entry.Length = length
		}
		return entry, packetKindDirect, true
	}
	// Packet-shaped but not direct-numeric: must be symbolic.
	if strings.HasPrefix(line, "packet(") || strings.HasPrefix(line, "parseable_packet(") {
		return Entry{}, packetKindSymbolic, true
	}
	return Entry{}, packetKindNone, false
}

// activePredicate returns the conjunction of predicates on the current
// branch stack. It is the predicate string we record on each entry — it
// is the AND of every enclosing gate's currently selected branch. If
// the stack is empty (entry is outside any #if), the result is ""
// meaning "always active".
func activePredicate(stack []frame) string {
	var parts []string
	for _, f := range stack {
		if f.predicate != "" {
			parts = append(parts, f.predicate)
		}
	}
	return strings.Join(parts, " && ")
}

// minVersionFor returns the minimum integer version such that the
// predicate would be true. This is the simplest gate threshold: when
// the predicate is "PACKETVER >= X", that is X. For disjunctions we
// return the minimum threshold across the disjuncts (the version at
// which ANY of them starts being true). Callers that need exact
// disjunction semantics should use EvalPredicate directly.
func minVersionFor(predicate string) int {
	if predicate == "" {
		return 0
	}
	threshold := 1 << 31
	first := true
	gte := regexp.MustCompile(`>=\s*(-?[0-9]+)`)
	for _, m := range gte.FindAllStringSubmatch(predicate, -1) {
		v, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if first || v < threshold {
			threshold = v
			first = false
		}
	}
	if first {
		// Pure defined(...) or unsupported form; conservatively 0
		// (always included).
		return 0
	}
	return threshold
}
