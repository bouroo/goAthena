// Command protocolgen generates the protocol tables from a rAthena
// checkout's own C headers.
//
// The generator does not reimplement upstream's preprocessor logic: it asks
// upstream's compiler. For each client variant it preprocesses upstream's
// packet tables with that variant's PACKETVER selection, then applies
// upstream's own registration rule (last registration wins) to produce the
// resolved table the server dispatches on.
//
// This matters because rAthena's wire protocol is not a flat list of opcodes.
// Every opcode, every struct layout, and every field width is guarded by
// preprocessor conditionals over PACKETVER_MAIN_NUM, PACKETVER_RE_NUM, and
// PACKETVER_ZERO_NUM, and upstream resolves them at build time by including
// two tables in order:
//
//	clif_packetdb.hpp   (src/map/clif.cpp:25833)
//	clif_shuffle.hpp    (src/map/clif.cpp:25834)
//
// both feeding packetdb_addpacket, which overwrites on duplicate opcodes
// (src/map/clif.cpp:25798). The shuffle table re-binds opcodes per client
// version, so it is not optional — it is where most version differences live.
//
// # The sources
//
// The server speaks the protocol of a *source*: a checkout of a rAthena
// fork. The Thai fork (rAthena-Thailand/rathenaThailand) is the primary
// target; upstream (rathena/rathena) is the compatibility set. The two
// forks' packet tables differ by one packet and their wire layouts are
// identical, but the difference is real, so each source gets its own
// generated package and the server selects one at boot.
//
// Usage:
//
//	protocolgen -source <name> -rathena <path-to-checkout> -out <dir>
//
// The checkout is a repository submodule under third_party. The generated
// files are committed, so an ordinary build never needs a C++ compiler;
// only regenerating them does.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	source := flag.String("source", "thailand", "name of the source pack: thailand or upstream")
	rathena := flag.String("rathena", "", "path to the source's checkout (defaults to the submodule)")
	out := flag.String("out", "pkg/ro/protocol/generated", "directory to write the generated Go files into")
	flag.Parse()

	path := *rathena
	if path == "" {
		path = filepath.Join("third_party", *source)
	}

	if err := run(*source, path, *out); err != nil {
		fmt.Fprintln(os.Stderr, "protocolgen:", err)
		os.Exit(1)
	}
}

// sources are the source packs the generator knows.
//
// A source is a rAthena fork the server can speak. The Thai fork is the
// primary target: it targets the main client family at PACKETVER 20250604.
// Upstream is the compatibility set: it targets the RE client family at
// PACKETVER 20211103.
var sources = []source{
	{
		name:     "thailand",
		pkg:      "thailand",
		checkout: filepath.Join("third_party", "rathenaThailand"),
		// The Thai fork's own PACKETVER (src/config/packets.hpp). It is
		// 20250604, which upstream's own selection rules (the same rules,
		// because the fork carries them) resolve to the main client
		// family.
		variants: []variant{
			{name: "Main", packetver: 20250604},
			{name: "RE", packetver: 20211103},
		},
	},
	{
		name:     "upstream",
		pkg:      "upstream",
		checkout: filepath.Join("third_party", "rathena"),
		// Upstream master's own PACKETVER (src/config/packets.hpp):
		// 20211103, which resolves to the RE client family.
		//
		// The main family's date is upstream's own paired date: upstream
		// writes its gates as "PACKETVER_MAIN_NUM >= 20220330 ||
		// PACKETVER_RE_NUM >= 20211103" (src/common/packets.hpp:51), so
		// 20211103 and 20220330 are the two sides of the same protocol
		// generation.
		variants: []variant{
			{name: "RE", packetver: 20211103},
			{name: "Main", packetver: 20220330},
		},
	},
}

// source is one source pack: a fork the server can speak.
type source struct {
	// name is the source's name, as the config names it.
	name string
	// pkg is the Go package suffix the generated files carry.
	pkg string
	// checkout is the default path to the source's checkout.
	checkout string
	// variants are the client families to resolve for this source.
	variants []variant
}

func run(sourceName, rathenaPath, outDir string) error {
	src, ok := findSource(sourceName)
	if !ok {
		return fmt.Errorf("unknown source %q", sourceName)
	}

	checkout := rathenaPath
	if checkout == "" {
		checkout = src.checkout
	}
	sourceDir := filepath.Join(checkout, "src")
	if _, err := os.Stat(filepath.Join(sourceDir, "map", "packets.hpp")); err != nil {
		return fmt.Errorf("%s checkout not found at %s: %w", src.name, checkout, err)
	}
	compiler := os.Getenv("CXX")
	if compiler == "" {
		compiler = "c++"
	}

	outDir = filepath.Join(outDir, src.pkg)
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return fmt.Errorf("output dir: %w", err)
	}
	for _, v := range src.variants {
		table, err := resolveTable(compiler, checkout, sourceDir, v)
		if err != nil {
			return fmt.Errorf("variant %s: %w", v.name, err)
		}
		if len(table) == 0 {
			return fmt.Errorf("variant %s: resolved an empty packet table", v.name)
		}
		ordered, err := registrationOrderFor(compiler, checkout, sourceDir, v)
		if err != nil {
			return fmt.Errorf("variant %s: %w", v.name, err)
		}
		if err := writeTable(outDir, src, v, table, ordered); err != nil {
			return err
		}
	}

	// The source's wire struct layouts, resolved by the source's own
	// compiler. The login and char servers frame their packets from these
	// sizes.
	for _, v := range src.variants {
		layouts, err := structLayouts(compiler, checkout, sourceDir, v)
		if err != nil {
			return fmt.Errorf("variant %s: %w", v.name, err)
		}
		offsets, err := fieldOffsets(compiler, checkout, sourceDir, v)
		if err != nil {
			return fmt.Errorf("variant %s: %w", v.name, err)
		}
		if err := writeLayouts(outDir, src, v, layouts, offsets); err != nil {
			return err
		}
	}
	return writeOpcodes(outDir, src, compiler, checkout, sourceDir)
}

// findSource returns the source pack with the given name.
func findSource(name string) (source, bool) {
	for _, s := range sources {
		if s.name == name {
			return s, true
		}
	}
	return source{}, false
}
