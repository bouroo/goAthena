package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// structLayouts resolves the on-wire size of every wire struct for one
// variant.
//
// The size is computed by upstream's own compiler from upstream's own
// declarations, so it accounts for the packed attribute and for the
// preprocessor-selected field widths (uint16 vs uint32 item ids, and so on).
//
// Upstream's map and common header sets share the include guard PACKETS_HPP,
// so they are probed in separate translation units and merged.
func structLayouts(compiler, rathenaPath, srcDir string, v variant) (map[string]int, error) {
	layouts := map[string]int{}
	for _, header := range layoutHeaders {
		got, err := probeLayouts(compiler, rathenaPath, srcDir, v, header)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", header, err)
		}
		for k, n := range got {
			layouts[k] = n
		}
	}
	return layouts, nil
}

// layoutHeaders are the upstream header sets that declare wire structs.
var layoutHeaders = []string{
	"map/packets.hpp",
	"common/packets.hpp",
	// The item-list and item-info structs are declared here, and the
	// server encodes them field by field. The header needs the
	// DEFINE_PACKET_ID macro that map/packets.hpp defines, so the probe
	// includes that header first.
	"map/packets_struct.hpp",
}

// fieldLayouts returns the resolved byte offsets of a struct's fields for one
// variant.
//
// The offsets come from upstream's own compiler, so they account for the
// packed attribute and for the preprocessor-selected field widths. A struct
// whose fields upstream selects conditionally gets the offsets that
// selection produces.
func fieldLayouts(compiler, rathenaPath, srcDir string, v variant, header, structName string) (map[string]int, error) {
	fields, err := structFields(srcDir, header, structName)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		// Upstream selects a struct's fields conditionally, so the naive scan
		// can miss them. Fall back to the compiler's own view.
		return probeFieldOffsets(compiler, rathenaPath, srcDir, v, header, structName, nil)
	}
	return probeFieldOffsets(compiler, rathenaPath, srcDir, v, header, structName, fields)
}

// structFieldRe matches a field declaration inside a struct body.
var structFieldRe = regexp.MustCompile(`(?m)^\s*(?:const\s+)?(?:struct\s+[A-Za-z_][A-Za-z0-9_]*|[A-Za-z_][A-Za-z0-9_]*)\s+([A-Za-z_][A-Za-z0-9_]*)\s*(\[[^\]]*\])?\s*;`)

// anonymousStructRe matches an anonymous struct member.
//
// Upstream's item structs carry their flags in an anonymous struct
// (src/map/packets_struct.hpp: NORMALITEM_INFO), whose name the server
// still needs.
var anonymousStructRe = regexp.MustCompile(`(?m)^\s*\}\s*([A-Za-z_][A-Za-z0-9_]*)\s*;`)

// structBitFieldRe matches a bitfield member, which the plain field
// pattern misses because of the trailing bit width.
//
// Upstream's item structs carry their identified and damaged flags as
// bitfields (src/map/packets_struct.hpp: NORMALITEM_INFO), and the server
// has to write them at the right offset.
var structBitFieldRe = regexp.MustCompile(`(?m)^\s*[A-Za-z_][A-Za-z0-9_]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*:\s*[0-9]+\s*;`)

// structFields returns the field names declared in one upstream struct.
func structFields(srcDir, header, structName string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(srcDir, header))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", header, err)
	}
	src := commentedBlockRe.ReplaceAllString(string(data), "")

	start := strings.Index(src, "struct "+structName)
	if start < 0 {
		return nil, nil
	}
	open := strings.Index(src[start:], "{")
	if open < 0 {
		return nil, nil
	}
	body := src[start+open:]
	// The body ends at the matching closing brace.
	depth := 0
	end := 0
	for i, r := range body {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end > 0 {
			break
		}
	}

	var fields []string
	for _, m := range structFieldRe.FindAllStringSubmatch(body[:end], -1) {
		fields = append(fields, m[1])
	}
	for _, m := range structBitFieldRe.FindAllStringSubmatch(body[:end], -1) {
		fields = append(fields, m[1])
	}
	// The anonymous struct that carries the flags is a member too, and
	// the compiler knows its offset.
	for _, m := range anonymousStructRe.FindAllStringSubmatch(body[:end], -1) {
		fields = append(fields, m[1])
	}
	return fields, nil
}

// probeFieldOffsets compiles a probe that prints the offset of each field of
// one upstream struct.
func probeFieldOffsets(compiler, rathenaPath, _ string, v variant, header, structName string, fields []string) (map[string]int, error) {
	dir, err := os.MkdirTemp("", "protocolgen-off")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir) //nolint:errcheck // temp dir cleanup

	stubDir := filepath.Join(dir, "stub")
	if err := os.MkdirAll(stubDir, 0o750); err != nil {
		return nil, fmt.Errorf("output dir: %w", err)
	}
	if err := writeStubs(stubDir); err != nil {
		return nil, fmt.Errorf("output dir: %w", err)
	}

	var src bytes.Buffer
	fmt.Fprintf(&src, "#include \"config/packets.hpp\"\n")
	// packets_struct.hpp is not self-contained: it relies on the
	// DEFINE_PACKET_ID macro that map/packets.hpp defines before it
	// includes it. Including map/packets.hpp brings both.
	if header == "map/packets_struct.hpp" {
		fmt.Fprintf(&src, "#include \"map/packets.hpp\"\n")
	} else {
		fmt.Fprintf(&src, "#include \"%s\"\n", header)
	}
	fmt.Fprintf(&src, "#include <cstdio>\n#include <cstddef>\n#include <type_traits>\n")
	// Upstream selects a struct's fields conditionally, so the raw text
	// scan sees fields this client family does not have. The compiler
	// decides: a field the preprocessor dropped is not a member, and the
	// probe reports only the ones that are.
	for i, f := range fields {
		// A member pointer type is only formed when the member exists, so
		// the trait is false for a field the preprocessor dropped.
		fmt.Fprintf(&src, "template<typename S, typename = void> struct has%d : std::false_type {};\n", i)
		fmt.Fprintf(&src, "template<typename S> struct has%d<S, std::void_t<decltype(&S::%s)>> : std::true_type {};\n", i, f)
		// The check lives in a template, so the discarded branch of the
		// if constexpr is never instantiated for a struct without the
		// field.
		fmt.Fprintf(&src, "template<typename S> void probe%d(const char* name){\n", i)
		fmt.Fprintf(&src, "  if constexpr (has%d<S>::value) { printf(\"F\\t%%s\\t%%zu\\n\", name, offsetof(S, %s)); }\n", i, f)
		fmt.Fprintf(&src, "}\n")
	}
	fmt.Fprintf(&src, "int main(){\n")
	for i, f := range fields {
		fmt.Fprintf(&src, "  probe%d<struct %s>(\"%s\");\n", i, structName, f)
	}
	fmt.Fprintf(&src, "  return 0;\n}\n")

	srcPath := filepath.Join(dir, "probe.cpp")
	if err := os.WriteFile(srcPath, src.Bytes(), 0o600); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}
	binPath := filepath.Join(dir, "probe")
	if err := compile(compiler, rathenaPath, stubDir, v, srcPath, binPath); err != nil {
		return nil, err
	}

	out, err := exec.Command(binPath).Output() // #nosec G204 -- the probe is the binary we just compiled into our own temp dir
	if err != nil {
		return nil, fmt.Errorf("running offset probe: %w", err)
	}

	offsets := map[string]int{}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) != 3 || parts[0] != "F" {
			continue
		}
		n, err := strconv.Atoi(parts[2])
		if err != nil {
			continue
		}
		offsets[parts[1]] = n
	}
	return offsets, nil
}

// writeLayouts emits the generated Go file holding the resolved wire-struct
// sizes for one variant, plus the resolved field offsets of the structs the
// server encodes field by field.
func writeLayouts(outDir string, src source, v variant, layouts map[string]int, offsets map[string]map[string]int) error {
	names := make([]string, 0, len(layouts))
	for n := range layouts {
		names = append(names, n)
	}
	sort.Strings(names)

	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by cmd/protocolgen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", src.pkg)
	fmt.Fprintf(&b, "// %sLayouts holds the resolved on-wire size of every wire struct for\n", v.name)
	fmt.Fprintf(&b, "// the %s client family (PACKETVER %d), as computed by the source's own\n", v.name, v.packetver)
	fmt.Fprintf(&b, "// compiler from the source's headers.\nvar %sLayouts = map[string]int{\n", v.name)
	for _, n := range names {
		fmt.Fprintf(&b, "\t%q: %d,\n", n, layouts[n])
	}
	fmt.Fprintf(&b, "}\n\n")

	structNames := make([]string, 0, len(offsets))
	for n := range offsets {
		structNames = append(structNames, n)
	}
	sort.Strings(structNames)
	for _, sn := range structNames {
		fields := offsets[sn]
		fieldNames := make([]string, 0, len(fields))
		for f := range fields {
			fieldNames = append(fieldNames, f)
		}
		sort.Strings(fieldNames)
		fmt.Fprintf(&b, "// %s%sOffsets holds the resolved byte offset of every field of the\n", v.name, sn)
		fmt.Fprintf(&b, "// %s struct, as computed by the source's own compiler.\n", sn)
		fmt.Fprintf(&b, "var %s%sOffsets = map[string]int{\n", v.name, sn)
		for _, f := range fieldNames {
			fmt.Fprintf(&b, "\t%q: %d,\n", f, fields[f])
		}
		fmt.Fprintf(&b, "}\n\n")
	}

	name := filepath.Join(outDir, fmt.Sprintf("layouts_%s_gen.go", strings.ToLower(v.name)))
	if err := os.WriteFile(name, b.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

// offsetStructs are the upstream structs whose fields the server writes
// individually, and whose offsets therefore must be resolved rather than
// hand-computed.
//
// CHARACTER_INFO is the important one: upstream compiles it with
// preprocessor-selected field widths (at the RE client family the hp and sp
// fields are int64, not int32/int16), so its layout differs per client
// family.
var offsetStructs = []struct {
	name   string
	header string
}{
	{name: "CHARACTER_INFO", header: "common/packets.hpp"},
	// The item-info structs carry preprocessor-selected field widths (at
	// the RE client family the item id is a uint32 and the wear state a
	// uint32, not the uint16 of older clients), so their offsets differ
	// per client family too.
	{name: "NORMALITEM_INFO", header: "map/packets_struct.hpp"},
	{name: "EQUIPITEM_INFO", header: "map/packets_struct.hpp"},
	{name: "EQUIPSLOTINFO", header: "map/packets_struct.hpp"},
	{name: "PACKET_ZC_ITEM_PICKUP_ACK", header: "map/packets_struct.hpp"},
}

// fieldOffsets resolves the byte offsets of the fields of every struct the
// server encodes field by field, for one variant.
func fieldOffsets(compiler, rathenaPath, srcDir string, v variant) (map[string]map[string]int, error) {
	out := map[string]map[string]int{}
	for _, s := range offsetStructs {
		offsets, err := fieldLayouts(compiler, rathenaPath, srcDir, v, s.header, s.name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.name, err)
		}
		if len(offsets) == 0 {
			return nil, fmt.Errorf("%s: no field offsets resolved", s.name)
		}
		out[s.name] = offsets
	}
	return out, nil
}
