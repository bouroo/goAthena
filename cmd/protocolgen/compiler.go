package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// compiler is the C++ compiler the generator asks about the source.
//
// The generator does not reimplement upstream's preprocessor logic: it asks
// upstream's compiler what upstream's own declarations resolve to. Every
// probe therefore compiles against the source's tree, and the include paths
// and the PACKETVER selection are the same for all of them.
type compiler struct {
	// bin is the C++ compiler to run.
	bin string
	// rathenaPath is the source checkout's root.
	rathenaPath string
	// stubDir holds the minimal third-party headers upstream's map headers
	// include but that carry no wire-layout meaning.
	stubDir string
	// variant is the client family being resolved.
	variant variant
}

// newCompiler prepares a compiler for one source checkout and client family.
func newCompiler(bin, rathenaPath, stubDir string, v variant) *compiler {
	return &compiler{bin: bin, rathenaPath: rathenaPath, stubDir: stubDir, variant: v}
}

// args builds the compiler's argument list for one translation unit.
//
// The include paths are the source's own tree layout, and the PACKETVER
// defines select the client family. Both are facts about the source, so
// every probe shares them. The flags come before the translation unit,
// because the compiler reads the mode flags before it reads the input.
func (c *compiler) args(flags []string, srcPath string, extra ...string) []string {
	args := []string{
		"-std=c++17",
		"-DPACKETVER=" + strconv.Itoa(c.variant.packetver),
	}
	if c.variant.zero {
		args = append(args, "-DPACKETVER_ZERO=1")
	}
	args = append(args, flags...)
	args = append(args,
		"-I"+c.stubDir,
		"-I"+filepath.Join(c.rathenaPath, "src"),
		"-I"+filepath.Join(c.rathenaPath, "3rdparty"),
		"-I"+filepath.Join(c.rathenaPath, "3rdparty", "rapidyaml", "src"),
		"-I"+filepath.Join(c.rathenaPath, "3rdparty", "rapidyaml", "ext", "c4core", "src"),
		"-I"+filepath.Join(c.rathenaPath, "3rdparty", "libconfig"),
		srcPath,
	)
	return append(args, extra...)
}

// preprocess runs the C preprocessor over one translation unit and returns
// its expanded output.
//
// Only preprocessing is needed (not compilation) for the packet tables: they
// are macro invocations whose expansion is exactly what the source registers
// at runtime, and preprocessing avoids building the source's whole map
// server.
func (c *compiler) preprocess(srcPath string) (string, error) {
	cmd := exec.Command(c.bin, c.args([]string{"-E"}, srcPath)...) // #nosec G204 G702 -- the compiler and the paths are the caller's own
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("preprocessing %s: %w\n%s", srcPath, err, stderr.String())
	}
	return stdout.String(), nil
}

// compile builds one probe translation unit into an executable.
func (c *compiler) compile(srcPath, binPath string) error {
	cmd := exec.Command(c.bin, c.args(nil, srcPath, "-o", binPath)...) // #nosec G204 G702 -- the compiler and the paths are the caller's own
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("compiling probe: %w\n%s", err, stderr.String())
	}
	return nil
}

// runProbe executes a compiled probe and returns its output.
func runProbe(binPath string) ([]byte, error) {
	// The probe is the binary we just compiled into our own temp dir.
	out, err := exec.Command(binPath).Output() // #nosec G204 -- the probe is the binary we just compiled into our own temp dir
	if err != nil {
		return nil, fmt.Errorf("running probe: %w", err)
	}
	return out, nil
}

// workspace is a temporary directory holding one probe's sources, its stub
// headers, and the binary it compiles into.
type workspace struct {
	// dir is the workspace root.
	dir string
	// stubDir holds the stub headers.
	stubDir string
	// srcPath is the probe translation unit.
	srcPath string
	// binPath is the compiled probe.
	binPath string
}

// newWorkspace creates a workspace with the stub headers in place.
func newWorkspace(pattern string) (*workspace, error) {
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	stubDir := filepath.Join(dir, "stub")
	if err := os.MkdirAll(stubDir, 0o750); err != nil {
		return nil, fmt.Errorf("output dir: %w", err)
	}
	if err := writeStubs(stubDir); err != nil {
		return nil, fmt.Errorf("output dir: %w", err)
	}
	return &workspace{
		dir:     dir,
		stubDir: stubDir,
		srcPath: filepath.Join(dir, "probe.cpp"),
		binPath: filepath.Join(dir, "probe"),
	}, nil
}

// cleanup removes the workspace.
func (w *workspace) cleanup() {
	_ = os.RemoveAll(w.dir)
}

// writeSource writes the probe translation unit.
func (w *workspace) writeSource(body []byte) error {
	if err := os.WriteFile(w.srcPath, body, 0o600); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

// headerIncludes returns the includes a probe needs before the header set it
// inspects.
//
// packets_struct.hpp is not self-contained: it relies on the DEFINE_PACKET_ID
// macro that map/packets.hpp defines before it includes it. Including
// map/packets.hpp brings both.
func headerIncludes(header string) string {
	var src bytes.Buffer
	fmt.Fprintf(&src, "#include \"config/packets.hpp\"\n")
	if header == "map/packets_struct.hpp" {
		fmt.Fprintf(&src, "#include \"map/packets.hpp\"\n")
	} else {
		fmt.Fprintf(&src, "#include \"%s\"\n", header)
	}
	return src.String()
}
