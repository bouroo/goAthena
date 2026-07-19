package athenaconf

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// defaultMaxImportDepth caps recursive import: resolution to defend
// against pathological files that include themselves transitively.
const defaultMaxImportDepth = 32

// File represents one parsed .conf file. Keys preserves insertion order
// via Order so round-trip output is stable; the map is the indexed lookup
// form. Imports records raw import: paths as they appeared on disk so the
// caller can audit the import graph.
type File struct {
	Path    string
	Keys    map[string]Value
	Order   []string
	Imports []string
}

// Manifest records which source file each parsed key came from. ParseDir
// populates Sources (absolute paths, sorted) and KeyOrigin (key -> first
// file that defined it). ApplyToConfig populates Unmapped (keys present
// in the merged File that no typed Config field consumes).
type Manifest struct {
	Sources   []string
	KeyOrigin map[string]string
	Unmapped  []string
}

// Parser parses rAthena .conf text into a key/value File. rootDir is the
// base directory used to resolve import: paths; pass the rAthena checkout
// root (e.g. third_party/rathena).
type Parser struct {
	rootDir        string
	maxImportDepth int
}

// NewParser returns a Parser that resolves imports relative to rootDir.
// An empty rootDir means "use the parser process's working directory",
// which matches rAthena's own libconfig resolution.
func NewParser(rootDir string) *Parser {
	return &Parser{rootDir: rootDir, maxImportDepth: defaultMaxImportDepth}
}

// Parse reads one .conf file at path and returns its File. Imports are
// NOT followed; use ParseWithImports for that. Parse is the right entry
// point when the caller wants to inspect a single file in isolation.
func (p *Parser) Parse(path string) (*File, error) {
	//nolint:gosec // translator intentionally opens operator-supplied paths
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// Close error ignored: read-only file, error not actionable after Read.
	defer func() { _ = f.Close() }()

	abs, absErr := filepath.Abs(path)
	if absErr != nil {
		abs = path
	}

	out := &File{
		Path: abs,
		Keys: map[string]Value{},
	}
	if err := readLines(f, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ParseWithImports reads path and recursively follows import: directives.
// The returned File merges all imported files in depth-first order; later
// definitions override earlier ones for the same key, matching rAthena
// semantics (the last `key: value` seen wins). Cycles are reported as
// errors.
func (p *Parser) ParseWithImports(path string) (*File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	visited := map[string]bool{abs: true}
	return p.parseRecursive(abs, 0, visited)
}

func (p *Parser) parseRecursive(path string, depth int, visited map[string]bool) (*File, error) {
	if depth > p.maxImportDepth {
		return nil, fmt.Errorf("import depth exceeded %d at %s", p.maxImportDepth, path)
	}

	f, err := p.Parse(path)
	if err != nil {
		return nil, err
	}

	merged := &File{
		Path:  f.Path,
		Keys:  map[string]Value{},
		Order: []string{},
	}
	for k, v := range f.Keys {
		merged.Keys[k] = v
		merged.Order = append(merged.Order, k)
	}

	for _, imp := range f.Imports {
		impPath := p.resolveImport(path, imp)
		impAbs, absErr := filepath.Abs(impPath)
		if absErr != nil {
			impAbs = impPath
		}
		if visited[impAbs] {
			return nil, fmt.Errorf("import cycle detected: %s -> %s", path, impAbs)
		}
		visited[impAbs] = true

		sub, err := p.parseRecursive(impAbs, depth+1, visited)
		if err != nil {
			return nil, err
		}
		mergeInto(merged, sub)
		visited[impAbs] = false
	}

	return merged, nil
}

// resolveImport joins the import: path against the directory of the
// importing file, falling back to rootDir-relative when the importing
// file's directory has no such file. This mirrors rAthena's libconfig
// behaviour where `import: conf/x.conf` is resolved against the
// rAthena checkout root.
func (p *Parser) resolveImport(importerPath, importPath string) string {
	dir := filepath.Dir(importerPath)
	candidate := filepath.Join(dir, importPath)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	if p.rootDir != "" {
		rooted := filepath.Join(p.rootDir, importPath)
		if _, err2 := os.Stat(rooted); err2 == nil {
			return rooted
		}
	}
	return candidate
}

// mergeInto folds src into dst: src keys override dst keys; src keys that
// were not previously in dst are appended to dst.Order.
func mergeInto(dst, src *File) {
	for k, v := range src.Keys {
		if _, exists := dst.Keys[k]; !exists {
			dst.Order = append(dst.Order, k)
		}
		dst.Keys[k] = v
	}
	dst.Imports = append(dst.Imports, src.Imports...)
}

// ParseDir walks dir and parses every .conf file plus any *.txt file
// inside an import/ subdirectory (rAthena's import-tmpl convention).
// Returns one merged File plus a Manifest of which source file each key
// came from. Parse errors stop the walk.
func (p *Parser) ParseDir(dir string) (*File, *Manifest, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	var paths []string
	walkErr := filepath.WalkDir(absDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		ext := filepath.Ext(name)
		if ext == ".conf" {
			paths = append(paths, path)
			return nil
		}
		if ext == ".txt" {
			parent := filepath.Base(filepath.Dir(path))
			if parent == "import" {
				paths = append(paths, path)
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("walk %s: %w", absDir, walkErr)
	}
	sort.Strings(paths)

	merged := &File{Keys: map[string]Value{}}
	manifest := &Manifest{KeyOrigin: map[string]string{}}

	for _, path := range paths {
		f, perr := p.Parse(path)
		if perr != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", path, perr)
		}
		for k, v := range f.Keys {
			if _, exists := merged.Keys[k]; !exists {
				merged.Order = append(merged.Order, k)
			}
			merged.Keys[k] = v
			if _, already := manifest.KeyOrigin[k]; !already {
				manifest.KeyOrigin[k] = f.Path
			}
		}
		manifest.Sources = append(manifest.Sources, f.Path)
	}

	return merged, manifest, nil
}

// readLines tokenises one .conf file into out.Keys and out.Imports. The
// scanner tolerates CRLF, trims trailing whitespace, and strips both
// full-line `// ...` and inline `... // ...` comments.
func readLines(r io.Reader, out *File) error {
	br := stripBOM(r)
	sc := bufio.NewScanner(br)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	inBlockComment := false
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		cleaned := stripBlockComments(trimmed, &inBlockComment)
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			continue
		}
		if strings.HasPrefix(cleaned, "//") {
			continue
		}
		if strings.ContainsAny(cleaned, "{}()") {
			continue
		}
		key, value, hasValue, err := splitKeyValue(cleaned)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", out.Path, lineNo, err)
		}

		if key == "import" {
			out.Imports = append(out.Imports, value)
			continue
		}

		if !hasValue {
			out.Keys[key] = Value{Kind: KindString, Str: ""}
			out.Order = append(out.Order, key)
			continue
		}

		out.Keys[key] = parseScalar(value)
		out.Order = append(out.Order, key)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: scan: %w", out.Path, err)
	}
	return nil
}

// stripBlockComments removes /* ... */ sequences from line, handling the
// case where the comment spans multiple lines via the inBlock pointer.
// On return, *inBlock is true iff the line ended inside an unterminated
// block comment (i.e. the caller must pass the next line in too).
//
// Semantics mirror libconfig: anything between /* and the matching */ is
// dropped; whitespace is preserved around the dropped span so a trailing
// space is acceptable (TrimSpace later normalises it).
func stripBlockComments(line string, inBlock *bool) string {
	out := ""
	rest := line
	if *inBlock {
		// Currently inside a multi-line block comment; look for the
		// closing */ on this line.
		if _, after, ok := strings.Cut(rest, "*/"); ok {
			rest = after
			*inBlock = false
		} else {
			return ""
		}
	}
	for {
		start := strings.Index(rest, "/*")
		if start < 0 {
			out += rest
			return out
		}
		out += rest[:start]
		after := rest[start+2:]
		if _, tail, ok := strings.Cut(after, "*/"); ok {
			rest = tail
			continue
		}
		// Unterminated comment opens here; drop the rest of the line
		// and remember we are inside a block.
		*inBlock = true
		return out
	}
}

// splitKeyValue splits a single non-comment line into (key, value,
// hasValue). It strips an inline `// ...` suffix from the value before
// parsing. Empty value (`key:`) is reported as hasValue=true, value="".
func splitKeyValue(line string) (string, string, bool, error) {
	key, rest, found := strings.Cut(line, ":")
	if !found {
		return "", "", false, &parseError{msg: "missing ':' in line: " + line}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false, &parseError{msg: "empty key in line: " + line}
	}
	if i := strings.Index(rest, "//"); i >= 0 {
		rest = rest[:i]
	}
	value := strings.TrimSpace(rest)

	return key, value, true, nil
}

// parseScalar classifies a raw token into a Value. Booleans and ints are
// tried first; anything that does not match falls through to string.
func parseScalar(raw string) Value {
	if raw == "" {
		return Value{Kind: KindString, Str: ""}
	}
	if v, ok := boolValue(raw); ok {
		return v
	}
	if v, err := intValue(raw); err == nil {
		return v
	}
	if v, err := floatValue(raw); err == nil {
		return v
	}
	if q, ok := unquote(raw); ok {
		return Value{Kind: KindString, Str: q}
	}
	return Value{Kind: KindString, Str: raw}
}

// unquote strips a single pair of surrounding double quotes. rAthena
// does not support escape sequences inside strings, so a literal `"` in
// the middle of a string would already be malformed; we leave it alone.
func unquote(raw string) (string, bool) {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		return raw[1 : len(raw)-1], true
	}
	return raw, false
}

// stripBOM returns an io.Reader with a leading UTF-8 byte-order mark
// removed, if present. rAthena ships at least one file with a BOM; the
// parser must tolerate it.
func stripBOM(r io.Reader) io.Reader {
	br := bufio.NewReader(r)
	// Peek/Discard errors are non-fatal: a short read simply means the
	// stream has fewer than 3 bytes buffered, so there is no BOM present
	// and nothing to strip. rAthena ships a BOM in at least one conf
	// file (e.g. import-tmpl), so the check stays best-effort rather
	// than failing closed.
	b, _ := br.Peek(3)
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		_, _ = br.Discard(3)
	}
	return br
}
