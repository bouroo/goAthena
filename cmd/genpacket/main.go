// Command genpacket reads rathena/src/map/clif_obfuscation.hpp and emits a
// Go source file containing the per-PACKETVER obfuscation key triplets.
//
// Usage:
//
//	go run ./cmd/genpacket [-rathena PATH] [-ref REF] [-o OUTPUT]
//
// Defaults: -rathena $RATHENA_PATH (local override; empty = fetch from GitHub);
// -ref master (git ref for GitHub fetch);
// -o pkg/ro/crypto/obfuscation_keys.go (relative to CWD). The generator
// mimics a tiny subset of the C preprocessor: it finds the table inside the
// PACKET_OBFUSCATION(_WARN) guard, matches #elif PACKETVER conditions, and
// extracts the three uint32 args from each packet_keys(...) invocation.
//
// Source: https://github.com/rathena/rathena (branch master).
// Generated file is committed alongside the package so it does not require
// a local rAthena checkout at build time. Regenerate with `go generate ./pkg/ro/crypto`.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	headerPath     = "src/map/clif_obfuscation.hpp"
	rathenaRawBase = "https://raw.githubusercontent.com/rathena/rathena"
	rathenaRepo    = "https://github.com/rathena/rathena"
	defaultRef     = "master"
	httpTimeout    = 30 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genpacket:", err)
		os.Exit(1)
	}
}

func run() error {
	rathenaPath := flag.String("rathena", defaultRathenaPath(), "path to the rAthena source checkout (local override; empty = fetch from GitHub)")
	ref := flag.String("ref", defaultRef, "git ref (branch/tag/sha) used when fetching from GitHub")
	output := flag.String("o", defaultOutputPath(), "output Go source file")
	flag.Parse()

	source, err := fetchSource(*rathenaPath, *ref, headerPath)
	if err != nil {
		return err
	}

	table, cutoff, err := parse(string(source))
	if err != nil {
		return fmt.Errorf("parse %s: %w", headerPath, err)
	}

	generated, err := render(table, cutoff, *ref)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	if err := os.WriteFile(*output, []byte(generated), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", *output, err)
	}

	fmt.Printf("wrote %d key entries (cutoff %d) to %s\n", len(table), cutoff, *output)
	return nil
}

func defaultRathenaPath() string {
	return os.Getenv("RATHENA_PATH")
}

// fetchSource reads the rathena header from a local checkout when path is
// non-empty, or from the canonical GitHub raw URL otherwise.
func fetchSource(rathenaPath, ref, headerPath string) ([]byte, error) {
	if rathenaPath != "" {
		full := filepath.Join(rathenaPath, headerPath)
		//nolint:gosec // G304: path is supplied by the developer via -rathena flag or RATHENA_PATH env; it is intentionally developer-controlled.
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read rathena header %s: %w", full, err)
		}
		return data, nil
	}

	url := fmt.Sprintf("%s/%s/%s", rathenaRawBase, ref, headerPath)
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch rathena header from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch rathena header from %s: unexpected status %s", url, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body from %s: %w", url, err)
	}
	return body, nil
}

func defaultOutputPath() string {
	if v := os.Getenv("GENPACKET_OUTPUT"); v != "" {
		return v
	}
	return "pkg/ro/crypto/obfuscation_keys.go"
}

// Preprocessor condition constants — only the forms rAthena actually emits
// in clif_obfuscation.hpp are supported.
const (
	cmpEqual = "=="
	cmpGT    = ">"
)

// packetKeysRe matches the packet_keys(a,b,c); macro call with three hex
// unsigned 32-bit literals.
var packetKeysRe = regexp.MustCompile(`packet_keys\s*\(\s*(0x[0-9A-Fa-f]+)\s*,\s*(0x[0-9A-Fa-f]+)\s*,\s*(0x[0-9A-Fa-f]+)\s*\)\s*;`)

// packetVerRe captures the leading `#if/#elif PACKETVER <op> N` clause on a
// line; additional `|| PACKETVER <op> M` clauses are parsed separately.
var packetVerRe = regexp.MustCompile(`^#\s*(?:elif|if)\s+PACKETVER\s+([<>=!]+)\s+([0-9]+)`)

// packetVerTailRe matches an extra `|| PACKETVER <op> N` clause.
var packetVerTailRe = regexp.MustCompile(`\|\|\s*PACKETVER\s+([<>=!]+)\s+([0-9]+)`)

func parse(source string) (map[int][3]uint32, int, error) {
	table := make(map[int][3]uint32)
	var cutoff int

	scanner := bufio.NewScanner(strings.NewReader(source))
	var pending packetVerCondition

	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		newCutoff, err := processLine(trimmed, table, &pending, cutoff)
		if err != nil {
			return nil, 0, err
		}
		cutoff = newCutoff
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("scan: %w", err)
	}
	if cutoff == 0 {
		return nil, 0, fmt.Errorf("no off-mode sentinel (PACKETVER > N (0,0,0)) found")
	}
	return table, cutoff, nil
}

// processLine consumes a single source line, updating table/pending/cutoff
// and returning the next cutoff value.
func processLine(trimmed string, table map[int][3]uint32, pending *packetVerCondition, cutoff int) (int, error) {
	if idx := packetVerRe.FindStringIndex(trimmed); idx != nil {
		cond, err := parsePacketVerCondition(trimmed, idx)
		if err != nil {
			return cutoff, err
		}
		*pending = cond
		return cutoff, nil
	}

	keys, ok := matchPacketKeys(trimmed)
	if !ok {
		return cutoff, nil
	}
	if pending.op == "" {
		// packet_keys call without a preceding PACKETVER condition (e.g. the
		// custom-keys PACKET_OBFUSCATION_KEY{n} block); skip — we cannot
		// resolve it to a real date.
		return cutoff, nil
	}

	newCutoff, err := applyPending(table, *pending, keys, cutoff)
	if err != nil {
		return cutoff, err
	}
	*pending = packetVerCondition{}
	return newCutoff, nil
}

func parsePacketVerCondition(trimmed string, idx []int) (packetVerCondition, error) {
	m := packetVerRe.FindStringSubmatch(trimmed)
	cond := packetVerCondition{op: m[1]}
	value, err := strconv.Atoi(m[2])
	if err != nil {
		return cond, fmt.Errorf("invalid PACKETVER literal %q on line: %s", m[2], trimmed)
	}
	cond.value = value
	if cond.op == cmpEqual {
		cond.versions = []int{value}
	}

	rest := trimmed[idx[1]:]
	for {
		tail := strings.TrimSpace(rest)
		m2 := packetVerTailRe.FindStringSubmatch(tail)
		if m2 == nil || m2[1] != cmpEqual {
			break
		}
		v, err := strconv.Atoi(m2[2])
		if err != nil {
			return cond, fmt.Errorf("invalid PACKETVER literal %q on line: %s", m2[2], trimmed)
		}
		cond.versions = append(cond.versions, v)
		rest = tail[len(m2[0]):]
	}
	return cond, nil
}

func applyPending(table map[int][3]uint32, pending packetVerCondition, keys [3]uint32, cutoff int) (int, error) {
	switch {
	case pending.op == cmpGT && len(pending.versions) == 0:
		if keys != [3]uint32{} {
			return cutoff, fmt.Errorf("off-mode sentinel must be (0,0,0); got %v for PACKETVER > %d",
				keys, pending.value)
		}
		if cutoff != 0 {
			return cutoff, fmt.Errorf("multiple off-mode sentinels found: %d and %d", cutoff, pending.value)
		}
		return pending.value, nil
	case pending.op == cmpEqual:
		for _, v := range pending.versions {
			if _, exists := table[v]; exists {
				return cutoff, fmt.Errorf("duplicate entry for PACKETVER %d", v)
			}
			table[v] = keys
		}
		return cutoff, nil
	default:
		return cutoff, fmt.Errorf("unsupported PACKETVER comparison %q", pending.op)
	}
}

// packetVerCondition captures one PACKETVER condition line.
type packetVerCondition struct {
	op       string // "" when unset
	value    int
	versions []int
}

func matchPacketKeys(line string) ([3]uint32, bool) {
	m := packetKeysRe.FindStringSubmatch(line)
	if m == nil {
		return [3]uint32{}, false
	}
	var keys [3]uint32
	for i := range 3 {
		v, err := strconv.ParseUint(m[i+1], 0, 32)
		if err != nil {
			return [3]uint32{}, false
		}
		keys[i] = uint32(v)
	}
	return keys, true
}

func render(table map[int][3]uint32, cutoff int, ref string) (string, error) {
	versions := make([]int, 0, len(table))
	for v := range table {
		versions = append(versions, v)
	}
	sort.Ints(versions)

	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by cmd/genpacket; DO NOT EDIT.\n//\n")
	fmt.Fprintf(&b, "// Source: %s/blob/%s/src/map/clif_obfuscation.hpp\n", rathenaRepo, ref)
	fmt.Fprintf(&b, "// Generated from PACKETVER key triplets.\n")
	b.WriteString("package crypto\n\n")
	b.WriteString(`// obfuscationKeys maps a PACKETVER date (e.g. 20130807) to the
// (key0, key1, key2) triplet for the map-server packet-id LCG.
//
// Reference: rathena/src/map/clif.cpp (clif_parse, ~line 25700-25780).
var obfuscationKeys = map[int][3]uint32{
`)
	for _, v := range versions {
		keys := table[v]
		fmt.Fprintf(&b, "\t%d: {0x%08X, 0x%08X, 0x%08X},\n", v, keys[0], keys[1], keys[2])
	}
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "// obfuscationCutoff is the last PACKETVER that uses packet-id\n")
	fmt.Fprintf(&b, "// obfuscation. PACKETVER strictly greater than this value maps to\n")
	fmt.Fprintf(&b, "// (0,0,0) — the cipher is the identity transform.\n")
	fmt.Fprintf(&b, "const obfuscationCutoff = %d\n", cutoff)

	return b.String(), nil
}
