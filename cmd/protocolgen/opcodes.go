package main

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// headerConstRe matches a resolved upstream packet-header constant in the
// preprocessed output, e.g. "const int16 HEADER_ZC_PC_PURCHASE_RESULT = 0xca;".
var headerConstRe = regexp.MustCompile(`const\s+int16\s+HEADER_([A-Za-z0-9_]+)\s*=\s*(-?(?:0[xX][0-9a-fA-F]+|\d+))`)

// enumHeaderRe matches a resolved upstream packet header that upstream
// declares as an enumerator rather than a constant.
//
// Upstream's enums (src/map/packets_struct.hpp) hold the opcodes that have
// no HEADER_ constant: the packet-id enums (the "<name>Type" constants) and
// the rodex, guild and party id enums.
var enumHeaderRe = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+)\s*=\s*(-?(?:0[xX][0-9a-fA-F]+|\d+))\s*,`)

// headerFiles are the upstream header sets that declare packet-header
// constants.
//
// The map and common sets share the include guard PACKETS_HPP, so including
// both in one translation unit silently drops the second. Upstream keeps them
// apart for the same reason; so does the generator.
var headerFiles = []string{
	"map/packets.hpp",
	"common/packets.hpp",
	// The id enums (the "<name>Type = 0x..." constants) live here. They are
	// the opcodes upstream declares without a HEADER_ constant, and several
	// of them are selected per client family.
	"map/packets_struct.hpp",
}

// opcode is one resolved upstream packet header: its upstream symbol name and
// the opcode id the client uses for the selected variant.
type opcode struct {
	name  string
	value int
}

// resolveOpcodes resolves every upstream packet-header constant for one
// variant.
//
// Upstream's map and common header sets share the PACKETS_HPP include guard,
// so each set is preprocessed in its own translation unit and the results are
// merged here.
func resolveOpcodes(compiler, rathenaPath, srcDir string, v variant) (map[string]int, error) {
	literals, err := literalOpcodes(rathenaPath)
	if err != nil {
		return nil, err
	}

	stubDir, err := os.MkdirTemp("", "protocolgen-stub")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(stubDir) //nolint:errcheck // temp dir cleanup
	if err := writeStubs(stubDir); err != nil {
		return nil, err
	}

	out := map[string]int{}
	for _, header := range headerFiles {
		ops, err := probeOpcodesFor(compiler, rathenaPath, srcDir, stubDir, v, header)
		if err != nil {
			return nil, err
		}
		for _, o := range ops {
			out[o.name] = o.value
		}
	}
	// The literal opcodes: the packets upstream writes without a header
	// constant. They are the same in every client family, so they do not
	// vary with the variant.
	maps.Copy(out, literals)
	return out, nil
}

// literalOpcodeRe matches the opcode upstream writes into a frame's first
// word: "WFIFOW(fd,0) = 0x209;".
var literalOpcodeRe = regexp.MustCompile(`WFIFOW\(fd,0\)\s*=\s*(0x[0-9a-fA-F]+)\s*;`)

// literalPacketRe matches the packet name in the doc comment above such a
// write: "/// 0209 <result>.W ... (ZC_ADD_FRIENDS_LIST)".
var literalPacketRe = regexp.MustCompile(`\(([A-Z][A-Z_0-9]+)\)`)

// literalOpcodes returns the opcodes upstream writes as literals, by name.
//
// The packets upstream declares without a header constant — the friends and
// party packets, for instance — are written straight into the frame with the
// opcode as a literal. The literal is the opcode, and it is the same in
// every client family.
func literalOpcodes(rathenaPath string) (map[string]int, error) {
	out := map[string]int{}
	src := filepath.Join(rathenaPath, "src")
	// The walk reads the caller's own source tree, which the caller
	// checked out; the paths are not attacker-controlled.
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".cpp") {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // the walk reads the caller's own source tree
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			m := literalOpcodeRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			value, err := parseInt(m[1])
			if err != nil {
				continue
			}
			// The name is in the doc comment above the write.
			for j := i - 1; j >= 0 && j >= i-40; j-- {
				c := strings.TrimSpace(lines[j])
				if !strings.HasPrefix(c, "//") {
					continue
				}
				if pm := literalPacketRe.FindStringSubmatch(c); pm != nil {
					out[pm[1]] = value
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the source's literal opcodes: %w", err)
	}
	return out, nil
}

// probeOpcodesFor preprocesses one upstream header set and extracts the
// packet-header constants from the preprocessed output.
func probeOpcodesFor(compiler, rathenaPath, _, stubDir string, v variant, header string) ([]opcode, error) {
	dir, err := os.MkdirTemp("", "protocolgen-op")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir) //nolint:errcheck // temp dir cleanup

	srcPath := filepath.Join(dir, "probe.cpp")
	body := fmt.Sprintf("#include \"config/packets.hpp\"\n#include \"%s\"\n", header)
	if err := os.WriteFile(srcPath, []byte(body), 0o600); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	args := []string{
		"-std=c++17",
		"-E",
		"-DPACKETVER=" + strconv.Itoa(v.packetver),
	}
	if v.zero {
		args = append(args, "-DPACKETVER_ZERO=1")
	}
	args = append(args,
		"-I"+stubDir,
		"-I"+filepath.Join(rathenaPath, "src"),
		"-I"+filepath.Join(rathenaPath, "3rdparty"),
		"-I"+filepath.Join(rathenaPath, "3rdparty", "rapidyaml", "src"),
		"-I"+filepath.Join(rathenaPath, "3rdparty", "rapidyaml", "ext", "c4core", "src"),
		"-I"+filepath.Join(rathenaPath, "3rdparty", "libconfig"),
		srcPath,
	)

	// The compiler and the paths are the caller's own: the generator runs
	// the user's compiler over the user's source tree to ask it what the
	// source resolves to.
	cmd := exec.Command(compiler, args...) // #nosec G204 G702 -- the compiler and the paths are the caller's own
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("preprocessing %s: %w\n%s", header, err, stderr.String())
	}

	var ops []opcode
	for _, m := range headerConstRe.FindAllStringSubmatch(stdout.String(), -1) {
		val, err := parseInt(m[2])
		if err != nil {
			continue
		}
		ops = append(ops, opcode{name: m[1], value: val})
	}
	// Upstream's enum packet_headers holds the opcodes that have no
	// HEADER_ constant.
	for _, m := range enumHeaderRe.FindAllStringSubmatch(stdout.String(), -1) {
		val, err := parseInt(m[2])
		if err != nil {
			continue
		}
		ops = append(ops, opcode{name: m[1], value: val})
	}
	return ops, nil
}

// writeOpcodes emits the generated Go file holding the resolved packet-header
// constants for every variant.
func writeOpcodes(outDir string, src source, compiler, rathenaPath, srcDir string) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by cmd/protocolgen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", src.pkg)

	for _, v := range src.variants {
		ops, err := resolveOpcodes(compiler, rathenaPath, srcDir, v)
		if err != nil {
			return fmt.Errorf("variant %s: %w", v.name, err)
		}
		// A few packets are registered under an opcode the source declares
		// as an enumerator rather than a header constant (useItemAckType is
		// one), so the header constant's name is not the name a caller
		// knows the packet by. The struct the registration sizes is: it is
		// the source's own name for the packet, so the opcode is recorded
		// under it too.
		pkts, err := resolveTable(compiler, rathenaPath, srcDir, v)
		if err != nil {
			return fmt.Errorf("variant %s: %w", v.name, err)
		}
		for _, p := range pkts {
			if p.name == "" {
				continue
			}
			if _, ok := ops[p.name]; !ok {
				ops[p.name] = int(p.opcode)
			}
		}
		fmt.Fprintf(&b, "// %sOpcodes holds the resolved packet-header opcode ids for the %s client\n", v.name, v.name)
		fmt.Fprintf(&b, "// family (PACKETVER %d), as resolved by the source's own compiler.\n", v.packetver)
		fmt.Fprintf(&b, "var %sOpcodes = map[string]int{\n", v.name)
		for _, o := range sortedOpcodes(ops) {
			fmt.Fprintf(&b, "\t%q: %d,\n", o.name, o.value)
		}
		fmt.Fprintf(&b, "}\n\n")
	}

	if err := os.WriteFile(filepath.Join(outDir, "opcodes_gen.go"), b.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}
