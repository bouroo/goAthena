package main

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// registrationRe matches an expanded packetdb_addpacket call.
//
// The preprocessor leaves the call intact with upstream's own macros already
// expanded, so the shape is stable:
//
//	packetdb_addpacket(0x0072,19,clif_parse_WantToConnection,2,6,10,14,18,0);
//	packetdb_addpacket(HEADER_CZ_CONTACTNPC,sizeof(PACKET_CZ_CONTACTNPC),...);
var registrationRe = regexp.MustCompile(`packetdb_addpacket\(\s*([^,]+?)\s*,\s*([^,]+?)\s*(?:,\s*([^,]+?)\s*)?((?:,[^,]+?)*)\s*\);`)

// sizeofRe matches a sizeof expression naming one wire struct.
var sizeofRe = regexp.MustCompile(`sizeof\s*\(\s*(?:struct\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\)`)

// packetDef is one resolved client-to-server packet: the opcode, the wire
// length upstream registers for it, the upstream parse handler, and the field
// offsets that handler reads.
//
// The field offsets are upstream's own (the trailing integers in
// parseable_packet calls); they are the byte positions of the interesting
// fields inside the frame.
//
// The name is the upstream packet's own name, taken from the struct the
// registration sizes (PACKET_CZ_CONTACTNPC). It is how a caller asks for
// the packet by name rather than by the number it happens to have.
type packetDef struct {
	opcode  uint16
	length  int
	handler string
	name    string
	offsets []int
}

// resolveRegistrations resolves the source's packet registrations for one
// variant, in the source's own order.
//
// The source registers packets in two tables included back to back
// (src/map/clif.cpp:25833-25834) into packetdb_addpacket, which overwrites
// the entry for an opcode registered twice (src/map/clif.cpp:25798).
// Reproducing that last-wins rule over the preprocessed registration order
// yields the same table the source builds at runtime.
func resolveRegistrations(compiler, rathenaPath string, v variant) ([]packetDef, error) {
	pre, err := preprocess(compiler, rathenaPath, v)
	if err != nil {
		return nil, err
	}

	// The source's opcode constants come from two places: the numeric literals
	// in clif_packetdb.hpp and the HEADER_* symbols in upstream's packet
	// headers. Both must be resolved to a number.
	opcodes, err := resolveOpcodes(compiler, rathenaPath, v)
	if err != nil {
		return nil, err
	}
	// The source's lengths are either a literal or sizeof(struct PACKET_*).
	sizes, err := structLayouts(compiler, rathenaPath, v)
	if err != nil {
		return nil, err
	}

	var out []packetDef
	for line := range strings.SplitSeq(pre, "\n") {
		call, ok := parseRegistration(line, opcodes, sizes)
		if !ok {
			continue
		}
		out = append(out, call)
	}
	return out, nil
}

// resolveTable resolves the source's packet table for one variant.
//
// The table is the registrations with the last registration of an opcode
// winning, keyed by opcode and sorted by it.
func resolveTable(compiler, rathenaPath string, v variant) ([]packetDef, error) {
	regs, err := resolveRegistrations(compiler, rathenaPath, v)
	if err != nil {
		return nil, err
	}

	byOpcode := map[uint16]packetDef{}
	for _, d := range regs {
		byOpcode[d.opcode] = d
	}

	out := make([]packetDef, 0, len(byOpcode))
	for _, d := range byOpcode {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].opcode < out[j].opcode })
	return out, nil
}

// registrationOrderFor returns the registrations in the source's own order.
//
// The order matters: the source registers some handlers under several opcodes
// across client versions, so the last registration of a handler is the one
// the client uses at a given PACKETVER.
func registrationOrderFor(compiler, rathenaPath string, v variant) ([]packetDef, error) {
	return resolveRegistrations(compiler, rathenaPath, v)
}

// parseRegistration parses one expanded packetdb_addpacket call, resolving its
// opcode and length expressions.
func parseRegistration(line string, opcodes map[string]int, sizes map[string]int) (packetDef, bool) {
	m := registrationRe.FindStringSubmatch(line)
	if m == nil {
		return packetDef{}, false
	}

	opcode, ok := resolveOpcode(strings.TrimSpace(m[1]), opcodes)
	if !ok {
		return packetDef{}, false
	}
	lengthExpr := strings.TrimSpace(m[2])
	length, ok := resolveLength(lengthExpr, sizes)
	if !ok {
		return packetDef{}, false
	}

	def := packetDef{opcode: opcode, length: length}
	// The length expression names the struct the packet carries
	// (sizeof(struct PACKET_CZ_CONTACTNPC)), and that name is how a caller
	// asks for the packet by name rather than by the number it has.
	if sm := sizeofRe.FindStringSubmatch(lengthExpr); sm != nil {
		def.name = sm[1]
	}
	handler := strings.TrimSpace(m[3])
	if handler != "" && handler != "nullptr" && handler != "0" {
		def.handler = handler
		// The trailing arguments are the field offsets, terminated by a zero
		// (upstream's packetdb_addpacket reads offsets until it sees one).
		for a := range strings.SplitSeq(m[4], ",") {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			v, err := strconv.Atoi(a)
			if err != nil {
				break
			}
			if v == 0 {
				break
			}
			def.offsets = append(def.offsets, v)
		}
	}
	return def, true
}

// resolveOpcode resolves a packet's opcode expression to its numeric value.
//
// The expression is either a literal (0x0072) or one of upstream's HEADER_*
// constants.
func resolveOpcode(expr string, opcodes map[string]int) (uint16, bool) {
	if v, err := parseInt(expr); err == nil {
		return narrowOpcode(v)
	}
	if v, ok := opcodes[expr]; ok {
		return narrowOpcode(v)
	}
	// Upstream registers a packet under the constant's full name, and the
	// generated table carries the name without the HEADER_ prefix, so the
	// prefixed form is looked up too.
	if v, ok := opcodes[strings.TrimPrefix(expr, "HEADER_")]; ok {
		return narrowOpcode(v)
	}
	return 0, false
}

// narrowOpcode narrows an opcode to the client's two-byte opcode.
//
// An opcode above the client's range cannot be sent by a client, so it is
// rejected rather than wrapped into a different packet.
func narrowOpcode(v int) (uint16, bool) {
	if v < 0 || v > math.MaxUint16 {
		return 0, false
	}
	return uint16(v), true
}

// resolveLength resolves a packet's length expression to its numeric value.
//
// The expression is either a literal or sizeof(struct PACKET_*), whose value
// upstream's own compiler computes.
func resolveLength(expr string, sizes map[string]int) (int, bool) {
	if v, err := parseInt(expr); err == nil {
		return v, true
	}
	if m := sizeofRe.FindStringSubmatch(expr); m != nil {
		if v, ok := sizes[m[1]]; ok {
			return v, true
		}
	}
	return 0, false
}

// preprocess runs the C preprocessor over the source's packet tables.
//
// Only preprocessing is needed (not compilation): the packet tables are macro
// invocations whose expansion is exactly what the source registers at runtime,
// and preprocessing avoids building the source's whole map server.
func preprocess(compiler, rathenaPath string, v variant) (string, error) {
	ws, err := newWorkspace("protocolgen")
	if err != nil {
		return "", err
	}
	defer ws.cleanup()

	// The two tables are included in the source's order; the shuffle table
	// re-binds opcodes for specific client versions and must come second.
	body := "#include \"config/packets.hpp\"\n" +
		"#include \"map/clif_packetdb.hpp\"\n" +
		"#include \"map/clif_shuffle.hpp\"\n"
	if err := ws.writeSource([]byte(body)); err != nil {
		return "", err
	}

	c := newCompiler(compiler, rathenaPath, ws.stubDir, v)
	out, err := c.preprocess(ws.srcPath)
	if err != nil {
		return "", fmt.Errorf("preprocessing upstream packet tables: %w", err)
	}
	return out, nil
}

// parseInt parses a C integer literal (decimal or hexadecimal).
func parseInt(s string) (int, error) {
	if v, err := strconv.ParseInt(s, 0, 64); err == nil {
		return int(v), nil
	}
	return 0, fmt.Errorf("not an integer: %q", s)
}
