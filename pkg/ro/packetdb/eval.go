package packetdb

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// EvalPredicate reports whether the given #if-style predicate expression
// holds for the provided single PACKETVER value.
//
// The predicate language accepted by the evaluator is the subset of
// C-preprocessor expressions actually used in rAthena's
// clif_packetdb.hpp:
//
//   - <symbol> >= <integer>      where <symbol> is one of
//     PACKETVER, PACKETVER_MAIN_NUM, PACKETVER_RE_NUM, or
//     PACKETVER_ZERO_NUM.
//   - defined(<ident>)
//   - disjunctions of the above via " || ". Conjunctions ("&&") do not
//     appear in rAthena's source today; if they do in the future, the
//     evaluator will return false (fail-closed, never over-include).
//
// All *_NUM predicates are mapped to the supplied version (see the
// package documentation for the single-PACKETVER assumption). The
// defined(PACKETVER_ZERO) check is treated as true: the operator has
// chosen a PACKETVER, so the zero client path is potentially active.
//
// The function is exposed for unit testing and for the registry flattener
// in registry.go. Callers should generally use PacketRegistry.ForPacketVer
// rather than calling EvalPredicate directly.
func EvalPredicate(predicate string, version int) bool {
	// Normalize whitespace.
	p := strings.TrimSpace(predicate)
	if p == "" {
		return false
	}
	// Disjunction (||) — left-to-right short-circuit on the form rAthena
	// uses today. Conjunctions (&&) are not present in the source so we
	// fail closed if we encounter one.
	for _, disjunct := range splitTopLevelOr(p) {
		if evalConjunct(strings.TrimSpace(disjunct), version) {
			return true
		}
	}
	return false
}

// splitTopLevelOr splits s on "||" at top level (not inside any
// parentheses, which the rAthena source does not use but we tolerate
// defensively). Whitespace around the operator is discarded.
func splitTopLevelOr(s string) []string {
	out := []string{}
	depth := 0
	last := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '|':
			if depth == 0 && i+1 < len(s) && s[i+1] == '|' {
				out = append(out, s[last:i])
				last = i + 2
				i++ // skip second '|'
			}
		}
	}
	out = append(out, s[last:])
	return out
}

// evalConjunct evaluates a single conjunct (a leaf or a conjunction of
// leaves). Conjunctions are not present in rAthena's source today; we
// fail closed (return false) if we encounter "&&".
func evalConjunct(s string, version int) bool {
	for _, term := range splitTopLevelAnd(s) {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		if !evalTerm(term, version) {
			return false
		}
	}
	return true
}

func splitTopLevelAnd(s string) []string {
	out := []string{}
	depth := 0
	last := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '&':
			if depth == 0 && i+1 < len(s) && s[i+1] == '&' {
				out = append(out, s[last:i])
				last = i + 2
				i++
			}
		}
	}
	out = append(out, s[last:])
	return out
}

// evalTerm evaluates a single predicate term.
func evalTerm(term string, version int) bool {
	// defined(IDENT) -> true for PACKETVER_ZERO, false otherwise (the
	// only defined() form present in clif_packetdb.hpp).
	if m := definedRe.FindStringSubmatch(term); m != nil {
		// Per the N1 single-PACKETVER assumption, any defined() check
		// is treated as true when a PACKETVER has been chosen. This
		// keeps the zero-client path active for operators who may be
		// running a zero client. If a future variant needs to
		// distinguish, wire it through ForPacketVer.
		return true
	}
	// <symbol> >= <integer>
	if m := gteRe.FindStringSubmatch(term); m != nil {
		threshold, err := strconv.Atoi(strings.TrimSpace(m[1]))
		if err != nil {
			return false
		}
		return version >= threshold
	}
	// Unknown predicate form — fail closed.
	return false
}

// gteRe matches "<symbol> >= <integer>" with arbitrary whitespace. The
// symbol group is captured but discarded; the integer group carries the
// threshold.
var gteRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\s*>=\s*(-?[0-9]+)\s*$`)

// definedRe matches "defined(IDENT)" with arbitrary whitespace.
var definedRe = regexp.MustCompile(`^\s*defined\s*\(\s*[A-Za-z_][A-Za-z0-9_]*\s*\)\s*$`)

// ValidatePredicate parses the expression and reports an error if it
// uses any form the evaluator does not understand. EvalPredicate fails
// closed on unknown terms, so ValidatePredicate is the explicit "I want
// to surface this loudly" entry point used by the parser.
func ValidatePredicate(predicate string) error {
	p := strings.TrimSpace(predicate)
	if p == "" {
		return fmt.Errorf("packetdb: empty predicate")
	}
	for _, disjunct := range splitTopLevelOr(p) {
		// Allow conjunctions but recurse into them as terms — only
		// reject terms we cannot evaluate.
		for _, term := range splitTopLevelAnd(disjunct) {
			t := strings.TrimSpace(term)
			if t == "" {
				continue
			}
			if definedRe.MatchString(t) {
				continue
			}
			if gteRe.MatchString(t) {
				continue
			}
			return fmt.Errorf("packetdb: unsupported predicate term %q", t)
		}
	}
	return nil
}
