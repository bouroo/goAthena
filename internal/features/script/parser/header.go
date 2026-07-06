package parser

import (
	"fmt"
	"strconv"

	"github.com/bouroo/goAthena/pkg/ro/script"
)

// detectHeaderLine scans the source bytes for the first non-comment,
// non-blank line and returns it. Returns hasHeader=false for inputs
// that start with `{` (body-only) or for empty/comment-only inputs.
func detectHeaderLine(src []byte) (line string, hasHeader bool) {
	i := skipCommentsAndBlank(src, 0)
	if i < 0 || i >= len(src) {
		return "", false
	}
	start := i
	for i < len(src) && src[i] != '\n' {
		i++
	}
	line = string(src[start:i])
	if !containsTab(line) {
		return "", false
	}
	return line, true
}

// skipCommentsAndBlank advances past leading whitespace, line comments,
// and block comments. It returns -1 if the input ends inside a block
// comment or only contains comments/whitespace.
func skipCommentsAndBlank(src []byte, i int) int {
	for i < len(src) {
		for i < len(src) && isSpace(src[i]) {
			i++
		}
		if i >= len(src) {
			return -1
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			end := skipBlockComment(src, i)
			if end < 0 {
				return -1
			}
			i = end
			continue
		}
		return i
	}
	return -1
}

// isSpace reports whether c is an ASCII whitespace byte.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// skipBlockComment returns the index just after a `*/` terminator, or
// -1 if the terminator is missing.
func skipBlockComment(src []byte, i int) int {
	i += 2
	for i+1 < len(src) && (src[i] != '*' || src[i+1] != '/') {
		i++
	}
	if i+1 >= len(src) {
		return -1
	}
	return i + 2
}

// containsTab is a tiny helper to avoid pulling in strings for the
// single byte search.
func containsTab(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' {
			return true
		}
	}
	return false
}

// parseHeaderLine converts a tab-separated NPC header line into an
// NPCHeader.
func parseHeaderLine(line string) (*script.NPCHeader, error) {
	fields := splitTabs(line)
	if len(fields) < 3 {
		return nil, &HeaderError{Msg: "NPC header needs at least 3 tab-separated fields: " + line}
	}

	pos := script.Position{Line: 1, Column: 1}

	// Floating `function script Name { ... }` definitions are tab-separated
	// but only have 4 fields; field 0 is `function` and field 1 is `script`.
	if fields[0] == keywordFunction || fields[0] == "-" {
		return buildFloatingHeader(fields, pos)
	}
	return buildMapHeader(fields, pos)
}

// buildMapHeader parses an NPC header that includes map coordinates.
func buildMapHeader(fields []string, pos script.Position) (*script.NPCHeader, error) {
	typ := firstWordLower(fields[1])
	mapParts := splitCommas(fields[0])
	if len(mapParts) == 1 {
		// Header-only map definition (mapflag, monster, warp, shop,
		// duplicate). Field 0 is just a map name; coordinates are implicit.
		if len(fields) < 3 {
			return nil, &HeaderError{Msg: "NPC header needs at least 3 tab-separated fields: " + fields[0]}
		}
		header := script.NewNPCHeader(mapParts[0], 0, 0, 0, "", "", 0, 0, 0, "", pos)
		applyNameAndType(header, fields[2], typ)
		if len(fields) >= 4 {
			header.SpriteName = trimTrailingPunct(stripTrailingBrace(stripTrailingCommaBrace(fields[3])))
		}
		return header, nil
	}
	if len(mapParts) != 4 {
		return nil, &HeaderError{Msg: "NPC header field 0 must be `map,x,y,dir`; got `" + fields[0] + "`"}
	}
	x, y, facing, err := parseCoords(mapParts[1], mapParts[2], mapParts[3])
	if err != nil {
		return nil, &HeaderError{Msg: err.Error()}
	}
	header := script.NewNPCHeader(mapParts[0], x, y, facing, "", "", 0, 0, 0, "", pos)
	applyNameAndType(header, fields[2], typ)
	if len(fields) >= 4 {
		spriteField := trimTrailingPunct(stripTrailingBrace(stripTrailingCommaBrace(fields[3])))
		if spriteField != "" {
			spriteParts := splitCommas(spriteField)
			header.SpriteID = parseSpriteID(spriteParts[0])
			if len(spriteParts) >= 3 {
				header.TriggerX, _ = strconv.Atoi(spriteParts[1])
				header.TriggerY, _ = strconv.Atoi(spriteParts[2])
			}
		}
	}
	return header, nil
}

// buildFloatingHeader parses a `-	script	Name	{...}` floating header.
func buildFloatingHeader(fields []string, pos script.Position) (*script.NPCHeader, error) {
	header := script.NewNPCHeader("-", 0, 0, 0, "", "", 0, 0, 0, "", pos)
	applyNameAndType(header, fields[2], fields[1])
	return header, nil
}

// parseCoords parses three comma-separated coordinate strings.
func parseCoords(xs, ys, facings string) (x, y, facing int, err error) {
	if x, err = strconv.Atoi(xs); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid x coordinate %q: %w", xs, err)
	}
	if y, err = strconv.Atoi(ys); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid y coordinate %q: %w", ys, err)
	}
	if facing, err = strconv.Atoi(facings); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid facing %q: %w", facings, err)
	}
	return x, y, facing, nil
}

// applyNameAndType fills the Type and Name/SpriteName fields of a header.
func applyNameAndType(h *script.NPCHeader, nameField, typeField string) {
	h.Type = firstWordLower(typeField)
	if idx := indexDoubleColon(nameField); idx >= 0 {
		h.Name = nameField[:idx]
		h.SpriteName = nameField[idx+2:]
		return
	}
	h.Name = nameField
}

func parseSpriteID(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func firstWordLower(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '(' {
			return asciiLower(s[:i])
		}
	}
	return asciiLower(s)
}

// splitTabs splits s on '\t'. Avoids importing strings just for one call.
func splitTabs(s string) []string {
	var out []string
	last := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' {
			out = append(out, s[last:i])
			last = i + 1
		}
	}
	out = append(out, s[last:])
	return out
}

// splitCommas splits s on ','.
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

// asciiLower lowercases ASCII letters in s.
func asciiLower(s string) string {
	out := []byte(s)
	for i := range out {
		if out[i] >= 'A' && out[i] <= 'Z' {
			out[i] += 32
		}
	}
	return string(out)
}

func indexDoubleColon(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == ':' && s[i+1] == ':' {
			return i
		}
	}
	return -1
}

func stripTrailingCommaBrace(s string) string {
	if len(s) >= 2 && s[len(s)-2] == ',' && s[len(s)-1] == '{' {
		return s[:len(s)-2]
	}
	return s
}

func stripTrailingBrace(s string) string {
	if len(s) >= 1 && s[len(s)-1] == '{' {
		return s[:len(s)-1]
	}
	return s
}

func trimTrailingPunct(s string) string {
	end := len(s)
	for end > 0 {
		c := s[end-1]
		if c == ' ' || c == '\t' || c == '\r' {
			end--
			continue
		}
		break
	}
	return s[:end]
}

// HeaderError is returned for header-shape errors.
type HeaderError struct{ Msg string }

// Error implements the error interface.
func (e *HeaderError) Error() string { return "parse error: " + e.Msg }
