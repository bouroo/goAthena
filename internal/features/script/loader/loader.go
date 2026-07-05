package loader

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bouroo/goAthena/internal/features/script/parser"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// LoadResult holds the result of loading a single NPC definition.
type LoadResult struct {
	Header   *script.NPCHeader
	Body     []script.Stmt
	Source   string // raw source text
	File     string // source file path
	Line     int    // line number in source file
	ParseErr error  // non-nil if parsing failed
}

// LoadFile reads a single .txt file and extracts all NPC definitions.
func LoadFile(path string) ([]LoadResult, error) {
	return LoadFiles([]string{path})
}

// LoadDir reads all .txt files from a directory (recursively).
func LoadDir(dir string) ([]LoadResult, error) {
	paths, err := collectTxtFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("collect script files: %w", err)
	}
	return LoadFiles(paths)
}

// LoadFiles reads multiple .txt files.
func LoadFiles(paths []string) ([]LoadResult, error) {
	var all []LoadResult
	for _, p := range paths {
		results, err := loadSingleFile(p)
		if err != nil {
			return nil, err
		}
		all = append(all, results...)
	}
	return all, nil
}

func collectTxtFiles(dir string) ([]string, error) {
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".txt" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk script dir: %w", err)
	}
	return paths, nil
}

func loadSingleFile(path string) ([]LoadResult, error) {
	cleanPath := filepath.Clean(path)
	src, err := os.ReadFile(cleanPath) // #nosec G304 - path is from internal config
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cleanPath, err)
	}

	normalized := normalizeLineEndings(string(src))
	segments := extractSegments(normalized)

	results := make([]LoadResult, 0, len(segments))
	for _, seg := range segments {
		res := parseSegment(path, normalized, seg)
		results = append(results, res)
	}
	return results, nil
}

func normalizeLineEndings(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// segment describes one raw chunk of source that may be an NPC definition.
type segment struct {
	startLine int
	endLine   int
	startOff  int
	endOff    int
	header    string
	source    string
}

func extractSegments(src string) []segment {
	var segs []segment
	i := 0
	for i < len(src) {
		i = skipWhitespaceAndComments(src, i)
		if i >= len(src) {
			break
		}

		lineStart := i
		lineEnd := nextLineEnd(src, i)
		line := src[lineStart:lineEnd]
		lineNo := 1 + strings.Count(src[:lineStart], "\n")

		if !isDefinitionLine(line) {
			i = lineEnd + 1
			continue
		}

		s, ok := extractSegment(src, lineStart, lineEnd, lineNo)
		if !ok {
			i = lineEnd + 1
			continue
		}
		segs = append(segs, s)
		i = s.endOff

		// After a normal NPC segment, rAthena allows bare floating function
		// definitions (`function Name { ... }`) until the next header line.
		// Continue scanning for those and emit them as additional segments.
		for i < len(src) {
			i = skipWhitespaceAndComments(src, i)
			if i >= len(src) {
				break
			}
			nextLineStart := i
			nextLineEnd := nextLineEnd(src, i)
			nextLine := src[nextLineStart:nextLineEnd]
			if !isBareFunctionLine(strings.TrimSpace(nextLine)) {
				break
			}
			fnSeg, ok := extractSegment(src, nextLineStart, nextLineEnd, 1+strings.Count(src[:nextLineStart], "\n"))
			if !ok {
				break
			}
			segs = append(segs, fnSeg)
			i = fnSeg.endOff
		}
	}
	return segs
}

func isDefinitionLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Reject pure comment lines.
	if strings.HasPrefix(trimmed, "//") {
		return false
	}
	// Bare floating function definitions inside another script body look
	// like `function Name { ... }`. They are space-delimited, not tab-
	// delimited, so detect them before the tab-based checks.
	if isBareFunctionLine(trimmed) {
		return true
	}
	fields := strings.Split(trimmed, "\t")
	if len(fields) < 3 {
		return false
	}
	// Column 1 is the type keyword for most definitions.
	typ := strings.ToLower(strings.TrimSpace(fields[1]))
	switch typ {
	case "script", "warp", "shop", "monster", "duplicate", "mapflag":
		return true
	}
	return false
}

// isBareFunctionLine reports whether the line is a floating function
// definition not preceded by an NPC header, e.g. `function Name {`.
func isBareFunctionLine(trimmed string) bool {
	fields := strings.Fields(trimmed)
	if len(fields) < 2 || strings.ToLower(fields[0]) != "function" {
		return false
	}
	// Must be `function <ident>` and the rest of the line starts the body
	// (`{` follows the name, possibly on the next line). The previous
	// `function script Name { ... }` form is tab-separated and handled by
	// the tab-based path.
	if strings.Contains(trimmed, "\t") {
		return false
	}
	return true
}

func extractSegment(src string, lineStart, lineEnd, lineNo int) (segment, bool) {
	bodyStart, ok := findBodyStart(src, lineStart, lineEnd)
	if !ok {
		// Header-only types (warp, shop, monster, mapflag, duplicate).
		return segment{
			startLine: lineNo,
			endLine:   lineNo,
			startOff:  lineStart,
			endOff:    lineEnd + 1,
			header:    strings.TrimRight(src[lineStart:lineEnd], " \t\r"),
			source:    src[lineStart:lineEnd],
		}, true
	}

	bodyEnd, ok := findMatchingBrace(src, bodyStart)
	if !ok {
		return segment{}, false
	}

	line := strings.TrimRight(src[lineStart:lineEnd], " \t\r")
	// Bare floating function definitions (`function Name { ... }`) have
	// no tab-separated header. Synthesize a floating header (`-	script	Name`)
	// and keep the full function body as the segment source so the parser
	// sees a complete function script.
	if isBareFunctionLine(strings.TrimSpace(line)) {
		fields := strings.Fields(line)
		name := fields[1]
		hdr := "-\tscript\t" + name
		return segment{
			startLine: lineNo,
			endLine:   1 + strings.Count(src[:bodyEnd], "\n"),
			startOff:  lineStart,
			endOff:    bodyEnd + 1,
			header:    hdr,
			source:    "function\tscript\t" + name + "\t" + src[bodyStart:bodyEnd],
		}, true
	}

	seg := segment{
		startLine: lineNo,
		endLine:   1 + strings.Count(src[:bodyEnd], "\n"),
		startOff:  lineStart,
		endOff:    bodyEnd + 1,
		// Strip the trailing `,{` from the header so the parser's header
		// logic sees a clean tab-separated header and can parse the body
		// from the remaining source on its own.
		header: strings.TrimRight(stripTrailingBraceComma(src[lineStart:bodyStart]), " \t\r"),
		source: src[lineStart:bodyEnd],
	}
	return seg, true
}

func findBodyStart(src string, lineStart, lineEnd int) (int, bool) {
	// The body opening brace may appear before the end of the header line
	// (e.g. `4_F_KAFRA1,{`) or on the following line. Search the rest of the
	// header line first, then continue scanning forward.
	for i := lineStart; i < len(src); i++ {
		if i == lineEnd {
			// End of header line: continue scanning following whitespace.
			for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r' || src[i] == '\n') {
				i++
			}
			if i >= len(src) {
				return 0, false
			}
			if src[i] == '{' {
				return i, true
			}
			return 0, false
		}
		if src[i] == '{' {
			return i, true
		}
	}
	return 0, false
}

func findMatchingBrace(src string, bodyStart int) (int, bool) {
	depth := 1
	inString := false
	escape := false
	for i := bodyStart + 1; i < len(src); i++ {
		c := src[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == '{' {
			depth++
			continue
		}
		if c == '}' {
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func parseSegment(path, src string, seg segment) LoadResult {
	res := LoadResult{
		Source: seg.source,
		File:   path,
		Line:   seg.startLine,
	}

	hdr, err := parserParseHeader(seg.header)
	if err != nil {
		res.ParseErr = fmt.Errorf("header parse at %s:%d: %w", path, seg.startLine, err)
		return res
	}
	res.Header = hdr

	typ := strings.ToLower(hdr.Type)
	if typ == "warp" || typ == "shop" || typ == "monster" || typ == "mapflag" || typ == "duplicate" {
		return res
	}

	bodySource := extractBodySource(seg.source)
	if strings.TrimSpace(bodySource) == "" {
		return res
	}
	bodySource = "{" + bodySource + "}\n"

	tokens, err := script.Lex([]byte(bodySource))
	if err != nil {
		res.ParseErr = fmt.Errorf("lex at %s:%d: %w", path, seg.startLine, err)
		return res
	}

	p := parser.New(tokens)
	body, err := p.ParseBody()
	if err != nil {
		res.ParseErr = fmt.Errorf("parse at %s:%d: %w", path, seg.startLine, err)
		return res
	}
	res.Body = body
	return res
}

// parserParseHeader wraps the unexported parser header parsing.
func parserParseHeader(line string) (*script.NPCHeader, error) {
	// Reuse ParseFile on a synthetic source so we don't duplicate
	// header-parsing logic. We craft a tiny source whose first non-blank
	// line is the header followed by an empty body. For header-only
	// definitions (mapflag, monster, duplicate) the header line may not
	// have a body at all; append a dummy body so ParseFile has something
	// to consume. If the header already ends with `,{` we only need a
	// closing brace; otherwise we need a full `{ }` body so the parser
	// sees an opening brace.
	var synthetic string
	if strings.HasSuffix(strings.TrimRight(line, " \t\r"), "{") {
		synthetic = line + "\n}\n"
	} else {
		synthetic = line + "\n{\n}\n"
	}
	tokens, err := script.Lex([]byte(synthetic))
	if err != nil {
		return nil, fmt.Errorf("lex synthetic header: %w", err)
	}
	p := parser.NewWithSource([]byte(synthetic), tokens)
	f, err := p.ParseFile()
	if err != nil {
		return nil, fmt.Errorf("parse synthetic header: %w", err)
	}
	return f.Header(), nil
}

func extractBodySource(source string) string {
	start := strings.Index(source, "{")
	if start < 0 {
		return ""
	}
	depth := 1
	inString := false
	escape := false
	for i := start + 1; i < len(source); i++ {
		c := source[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == '{' {
			depth++
			continue
		}
		if c == '}' {
			depth--
			if depth == 0 {
				return source[start+1 : i]
			}
		}
	}
	return source[start+1:]
}

func skipWhitespaceAndComments(src string, i int) int {
	for i < len(src) {
		c := src[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		if c == '/' && i+1 < len(src) {
			next := src[i+1]
			if next == '/' {
				i = nextLineEnd(src, i) + 1
				continue
			}
			if next == '*' {
				i = skipBlockComment(src, i)
				continue
			}
		}
		return i
	}
	return i
}

func nextLineEnd(src string, i int) int {
	for i < len(src) && src[i] != '\n' {
		i++
	}
	return i
}

func skipBlockComment(src string, i int) int {
	i += 2
	for i+1 < len(src) {
		if src[i] == '*' && src[i+1] == '/' {
			return i + 2
		}
		i++
	}
	return len(src)
}

// ParseShopItems parses the shop item list from the 4th header field.
// Format: spriteID,item1:price1,item2:price2,...
func ParseShopItems(spriteField string) (spriteID int, items []script.ShopItem, err error) {
	parts := splitCommas(spriteField)
	if len(parts) == 0 {
		return 0, nil, nil
	}
	spriteID, _ = strconv.Atoi(parts[0])
	for i := 1; i < len(parts); i++ {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue
		}
		itemPart, pricePart, ok := strings.Cut(p, ":")
		if !ok {
			return 0, nil, fmt.Errorf("shop item %q missing price separator", p)
		}
		itemID, parseErr := strconv.Atoi(strings.TrimSpace(itemPart))
		if parseErr != nil {
			return 0, nil, fmt.Errorf("shop item id %q: %w", itemPart, parseErr)
		}
		price, parseErr := strconv.Atoi(strings.TrimSpace(pricePart))
		if parseErr != nil {
			return 0, nil, fmt.Errorf("shop price %q: %w", pricePart, parseErr)
		}
		items = append(items, script.ShopItem{ItemID: safeInt32(itemID), Price: safeInt32(price)})
	}
	return spriteID, items, nil
}

// ParseWarpDest parses the warp destination from the 4th header field.
// Format: spriteID,destMap,destX,destY
func ParseWarpDest(spriteField string) (spriteID int, destMap string, destX, destY int, err error) {
	parts := splitCommas(spriteField)
	if len(parts) >= 1 {
		spriteID, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 4 {
		destMap = parts[1]
		destX, _ = strconv.Atoi(parts[2])
		destY, _ = strconv.Atoi(parts[3])
	}
	return spriteID, destMap, destX, destY, nil
}

func safeInt32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

func stripTrailingBraceComma(s string) string {
	for {
		// Remove trailing whitespace.
		s = strings.TrimRight(s, " \t\r")
		if strings.HasSuffix(s, "{") {
			s = s[:len(s)-1]
			continue
		}
		if strings.HasSuffix(s, ",") {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

func splitCommas(s string) []string {
	var out []string
	last := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[last:i])
			last = i + 1
		}
	}
	out = append(out, s[last:])
	return out
}
