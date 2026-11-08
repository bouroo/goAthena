//go:build unit

package protocol_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/protocol"
)

// The generator's correctness rests on reproducing upstream's own
// resolution: upstream registers its two packet tables in order and the last
// registration of an opcode wins. This test re-derives the table from
// upstream's preprocessed output with an independent implementation of that
// rule and compares it with the committed table.
//
// It is the check that would catch a generator change that silently altered
// the resolution.

// symbolRe matches an upstream packet header that upstream declares as an
// enumerator rather than a constant.
var symbolRe = regexp.MustCompile(`(?m)^\s*([a-z0-9_]+Type)\s*=\s*(-?(?:0[xX][0-9a-fA-F]+|\d+))\s*,`)

// headerSymbolRe matches an upstream packet header that upstream declares as
// a constant.
var headerSymbolRe = regexp.MustCompile(`const\s+int16\s+HEADER_([A-Za-z0-9_]+)\s*=\s*(-?(?:0[xX][0-9a-fA-F]+|\d+))`)

// registrationRe matches an expanded packetdb_addpacket call.
var registrationRe = regexp.MustCompile(`packetdb_addpacket\(\s*([^,]+?)\s*,\s*([^,]+?)\s*(?:,\s*([^,]+?)\s*)?((?:,[^,]+?)*)\s*\);`)

// sizeofRe matches a sizeof expression naming one upstream struct.
var sizeofRe = regexp.MustCompile(`sizeof\s*\(\s*(?:struct\s+)?([A-Za-z_0-9]+)\s*\)`)

// TestTableMatchesUpstreamResolution re-derives the resolved packet table
// from upstream's own headers and compares it with the generated one.
//
// The test needs a C++ compiler and the rAthena submodule; it skips without
// them rather than failing, so an ordinary checkout still runs the suite.
func TestTableMatchesUpstreamResolution(t *testing.T) {
	t.Parallel()

	root := rathenaRoot(t)
	compiler := os.Getenv("CXX")
	if compiler == "" {
		compiler = "c++"
	}
	if _, err := exec.LookPath(compiler); err != nil {
		t.Skipf("no C++ compiler (%s): %v", compiler, err)
	}

	pre := preprocessUpstream(t, compiler, root)
	if pre == "" {
		t.Skip("upstream packet tables did not preprocess")
	}

	set, ok := protocol.Resolve(protocol.SourceUpstream, protocol.VariantRE)
	if !ok {
		t.Fatal("the upstream source pack did not resolve")
	}

	sizes := map[string]int{}
	for name, size := range set.Layouts() {
		sizes[name] = size
	}

	// Independent re-derivation of upstream's last-wins rule.
	//
	// Upstream registers some packets under a symbolic opcode, such as
	// inventorylistnormalType, so the re-derivation resolves those from
	// upstream's own header rather than skipping them.
	symbols := upstreamOpcodeSymbols(t, compiler, root)
	want := map[uint16][2]any{}
	for _, m := range registrationRe.FindAllStringSubmatch(pre, -1) {
		opcode, err := strconv.ParseUint(m[1], 0, 16)
		if err != nil {
			v, ok := symbols[strings.TrimSpace(m[1])]
			if !ok {
				continue
			}
			opcode = uint64(v)
		}
		length, ok := resolveLength(m[2], sizes)
		if !ok {
			continue
		}
		handler := strings.TrimSpace(m[3])
		if handler == "nullptr" {
			handler = ""
		}
		want[uint16(opcode)] = [2]any{length, handler}
	}

	if len(want) == 0 {
		t.Fatal("re-derivation produced no opcodes")
	}

	got := set.Table()
	if got.Len() != len(want) {
		t.Errorf("generated table has %d opcodes, upstream's resolution has %d", got.Len(), len(want))
	}
	for opcode, expected := range want {
		p, ok := got.Lookup(opcode)
		if !ok {
			t.Errorf("opcode 0x%04x missing from the generated table", opcode)
			continue
		}
		if p.Length != expected[0] {
			t.Errorf("opcode 0x%04x: length = %d, upstream's resolution has %d", opcode, p.Length, expected[0])
		}
		if p.Handler != expected[1] {
			t.Errorf("opcode 0x%04x: handler = %q, upstream's resolution has %q", opcode, p.Handler, expected[1])
		}
	}
}

// upstreamOpcodeSymbols resolves upstream's symbolic packet opcodes.
//
// Upstream's enum packet_headers (src/map/packets_struct.hpp) holds the
// opcodes that have no HEADER_ constant. The preprocessed header gives the
// value the compiler selected for this client family.
func upstreamOpcodeSymbols(t *testing.T, compiler, root string) map[string]int {
	t.Helper()

	dir := t.TempDir()
	stubDir := filepath.Join(dir, "stub")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatalf("create stub dir: %v", err)
	}
	mysqlStub := "#pragma once\n" +
		"typedef struct st_mysql MYSQL;\n" +
		"typedef struct st_mysql_stmt MYSQL_STMT;\n" +
		"typedef unsigned long long my_ulonglong;\n" +
		"typedef struct st_mysql_bind { unsigned long *length; int *is_null; void *buffer; } MYSQL_BIND;\n"
	if err := os.WriteFile(filepath.Join(stubDir, "mysql.h"), []byte(mysqlStub), 0o600); err != nil {
		t.Fatalf("write mysql stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stubDir, "ryml_std.hpp"), []byte("#pragma once\n"), 0o600); err != nil {
		t.Fatalf("write ryml stub: %v", err)
	}

	src := filepath.Join(dir, "probe.cpp")
	body := "#include \"config/packets.hpp\"\n" +
		"#include \"map/packets.hpp\"\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	cmd := exec.Command(compiler, "-std=c++17", "-E",
		"-DPACKETVER=20211103",
		"-I"+filepath.Join(dir, "stub"),
		"-I"+filepath.Join(root, "src"),
		"-I"+filepath.Join(root, "3rdparty"),
		"-I"+filepath.Join(root, "3rdparty", "rapidyaml", "src"),
		"-I"+filepath.Join(root, "3rdparty", "rapidyaml", "ext", "c4core", "src"),
		"-I"+filepath.Join(root, "3rdparty", "libconfig"),
		src,
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("preprocess upstream headers: %v", err)
	}

	symbols := map[string]int{}
	for _, m := range symbolRe.FindAllStringSubmatch(string(out), -1) {
		value, err := strconv.ParseInt(m[2], 0, 32)
		if err != nil {
			continue
		}
		symbols[m[1]] = int(value)
	}
	// Upstream registers a packet under the constant's full name, so the
	// HEADER_ constants are resolved under that name too.
	for _, m := range headerSymbolRe.FindAllStringSubmatch(string(out), -1) {
		value, err := strconv.ParseInt(m[2], 0, 32)
		if err != nil {
			continue
		}
		symbols["HEADER_"+m[1]] = int(value)
	}
	if len(symbols) == 0 {
		t.Fatal("upstream opcode symbols did not preprocess")
	}
	return symbols
}

// resolveLength resolves a length expression to its numeric value.
func resolveLength(expr string, sizes map[string]int) (int, bool) {
	expr = strings.TrimSpace(expr)
	if v, err := strconv.Atoi(expr); err == nil {
		return v, true
	}
	if m := sizeofRe.FindStringSubmatch(expr); m != nil {
		if v, ok := sizes[m[1]]; ok {
			return v, true
		}
	}
	return 0, false
}

// preprocessUpstream preprocesses upstream's packet tables.
func preprocessUpstream(t *testing.T, compiler, root string) string {
	t.Helper()

	dir := t.TempDir()
	stubDir := filepath.Join(dir, "stub")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatalf("create stub dir: %v", err)
	}
	mysqlStub := "#pragma once\n" +
		"typedef struct st_mysql MYSQL;\n" +
		"typedef struct st_mysql_stmt MYSQL_STMT;\n" +
		"typedef unsigned long long my_ulonglong;\n" +
		"typedef struct st_mysql_bind { unsigned long *length; int *is_null; void *buffer; } MYSQL_BIND;\n"
	if err := os.WriteFile(filepath.Join(stubDir, "mysql.h"), []byte(mysqlStub), 0o600); err != nil {
		t.Fatalf("write mysql stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stubDir, "ryml_std.hpp"), []byte("#pragma once\n"), 0o600); err != nil {
		t.Fatalf("write ryml stub: %v", err)
	}

	src := filepath.Join(dir, "probe.cpp")
	body := "#include \"config/packets.hpp\"\n" +
		"#include \"map/clif_packetdb.hpp\"\n" +
		"#include \"map/clif_shuffle.hpp\"\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	cmd := exec.Command(compiler, "-std=c++17", "-E",
		"-DPACKETVER=20211103",
		"-I"+filepath.Join(dir, "stub"),
		"-I"+filepath.Join(root, "src"),
		"-I"+filepath.Join(root, "3rdparty"),
		"-I"+filepath.Join(root, "3rdparty", "rapidyaml", "src"),
		"-I"+filepath.Join(root, "3rdparty", "rapidyaml", "ext", "c4core", "src"),
		"-I"+filepath.Join(root, "3rdparty", "libconfig"),
		src,
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("preprocess upstream tables: %v", err)
	}
	return string(out)
}
