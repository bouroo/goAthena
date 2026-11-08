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

// Upstream declares a few structs that are not wire packets but are still
// wire-visible: CHARACTER_INFO (the character list entry the char server
// sends) is the important one. The probe therefore covers every struct
// upstream declares, not just the PACKET_* ones.
var anyStructNameRe = regexp.MustCompile(`(?m)^\s*struct\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)

// nestedStructRe matches a struct declared inside another declaration.
//
// Upstream declares s_packet_info inside a class body (src/common/
// packets.hpp), where the name is only visible in that scope; the probe
// cannot name it, and upstream never takes its size on the wire either.
var nestedStructRe = regexp.MustCompile(`(?m)^\tstruct\s+`)

// commentedBlockRe matches a C-style block comment.
var commentedBlockRe = regexp.MustCompile(`(?s)/\*.*?\*/`)

// probeLayouts compiles a probe that prints the size of every wire struct
// declared in one upstream header set.
func probeLayouts(compiler, rathenaPath, srcDir string, v variant, header string) (map[string]int, error) {
	names, err := structNames(compiler, rathenaPath, srcDir, v, header)
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "protocolgen-size")
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
	fmt.Fprintf(&src, "#include <cstdio>\n")
	fmt.Fprintf(&src, "#include <type_traits>\n")
	fmt.Fprintf(&src, "template<typename T, typename = void> struct is_complete : std::false_type {};\n")
	fmt.Fprintf(&src, "template<typename T> struct is_complete<T, std::void_t<decltype(sizeof(T))>> : std::true_type {};\n")
	fmt.Fprintf(&src, "template<typename T> void probe(const char* name){\n")
	// The C format string is escaped for Go's Fprintf: %s is the C
	// conversion, %% is a literal percent.
	fmt.Fprintf(&src, "  if constexpr (is_complete<T>::value) { printf(\"Z\\t%%s\\t%%zu\\n\", name, sizeof(T)); }\n")
	fmt.Fprintf(&src, "}\n")
	fmt.Fprintf(&src, "int main(){\n")
	for _, n := range names {
		// Some structs are only forward-declared, so sizeof does not
		// apply to them. The probe is a template, so the sizeof is only
		// instantiated for the complete types.
		fmt.Fprintf(&src, "  probe<struct %s>(\"%s\");\n", n, n)
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
		return nil, fmt.Errorf("running size probe: %w", err)
	}
	return parseSizes(out), nil
}

// structNames returns the wire struct names declared in one upstream header.
func structNames(_, _, srcDir string, _ variant, header string) ([]string, error) {
	// The struct names come from the preprocessed header, so the
	// preprocessor's own selection decides which structs exist for this
	// client family. Upstream guards several structs behind
	// `#if PACKETVER_...` blocks that exclude them from older clients; a
	// text scan would see them and the compiler would reject their size.
	data, err := os.ReadFile(filepath.Join(srcDir, header))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", header, err)
	}
	seen := map[string]bool{}
	for _, m := range anyStructNameRe.FindAllStringSubmatch(nestedStructRe.ReplaceAllString(commentedBlockRe.ReplaceAllString(string(data), ""), ""), -1) {
		seen[m[1]] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// parseSizes reads the size probe's "Z\t<name>\t<size>" lines.
func parseSizes(out []byte) map[string]int {
	sizes := map[string]int{}
	for line := range strings.SplitSeq(string(out), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) != 3 || parts[0] != "Z" {
			continue
		}
		n, err := strconv.Atoi(parts[2])
		if err != nil {
			continue
		}
		sizes[parts[1]] = n
	}
	return sizes
}

// compile builds one probe translation unit.
func compile(compiler, rathenaPath, stubDir string, v variant, srcPath, binPath string) error {
	args := []string{
		"-std=c++17",
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
		srcPath, "-o", binPath,
	)

	// The compiler and the paths are the caller's own: the generator runs
	// the user's compiler over the user's source tree to ask it what the
	// source resolves to.
	cmd := exec.Command(compiler, args...) // #nosec G204 G702 -- the compiler and the paths are the caller's own
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("compiling probe: %w\n%s", err, stderr.String())
	}
	return nil
}
