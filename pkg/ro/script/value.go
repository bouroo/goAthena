package script

import "fmt"

// ValueKind discriminates the dynamic type of a script Value. rAthena's
// script language is loosely typed: a variable holds either an integer or
// a string (the `$` suffix on a name marks the string cell). Float is
// rare but defined for forward compatibility. The VM never auto-coerces
// across kinds except where a builtin explicitly does (e.g. string concat).
type ValueKind uint8

const (
	// KindNil is the zero value: an unset variable or an absent result.
	KindNil ValueKind = iota
	// KindInt is a 64-bit integer — the overwhelmingly common case.
	KindInt
	// KindStr is a UTF-8 string (variable cells suffixed `$`).
	KindStr
	// KindFloat is a 64-bit float; reserved, few builtins produce it.
	KindFloat
)

// Value is a runtime script value. Only the field matching Kind is
// meaningful; the others stay zero. Value is immutable in practice —
// assignment replaces the whole cell, not a field of it.
type Value struct {
	Kind ValueKind
	Int  int64
	Str  string
	Flt  float64
}

// IntVal returns an integer Value.
func IntVal(n int64) Value { return Value{Kind: KindInt, Int: n} }

// StrVal returns a string Value.
func StrVal(s string) Value { return Value{Kind: KindStr, Str: s} }

// FloatVal returns a float Value.
func FloatVal(f float64) Value { return Value{Kind: KindFloat, Flt: f} }

// NilVal is the absent/zero value, returned for unset variables and void calls.
func NilVal() Value { return Value{Kind: KindNil} }

// IsZero reports whether v is the falsy value for a conditional. rAthena
// treats 0, the empty string, and nil as false; any nonzero int, non-empty
// string, or any float (including 0.0, matching rAthena's float branch
// which is truthy unless it is the integer-zero representation) as true.
// Scripts overwhelmingly branch on integer results, so the int branch is
// the hot path.
func (v Value) IsZero() bool {
	switch v.Kind {
	case KindInt:
		return v.Int == 0
	case KindStr:
		return v.Str == ""
	case KindFloat:
		return v.Flt == 0
	default:
		return true
	}
}

// String returns the script-visible textual form. Integers render in
// decimal; strings render bare; nil renders as the empty string (rAthena
// prints an unset variable as "" when concatenated). Used by mes/concat
// and for debug.
func (v Value) String() string {
	switch v.Kind {
	case KindInt:
		return itoa(int(v.Int))
	case KindStr:
		return v.Str
	case KindFloat:
		return fmt.Sprintf("%g", v.Flt)
	default:
		return ""
	}
}

// asInt coerces v to int64 for arithmetic/comparison. A string coerces to
// its leading decimal prefix, or 0 if none parses — matching rAthena's
// loose numeric coercion. This is the fallback path; the common path is an
// already-int Value.
func (v Value) asInt() int64 {
	switch v.Kind {
	case KindInt:
		return v.Int
	case KindFloat:
		return int64(v.Flt)
	case KindStr:
		return parseIntPrefix(v.Str)
	default:
		return 0
	}
}

// parseIntPrefix reads an optional leading sign and as many decimal digits
// as appear at the start of s, returning the value (0 for a non-numeric
// prefix). It is the numeric-coercion fallback for string Values and is
// deliberately permissive — rAthena treats "12abc" as 12, "abc" as 0.
func parseIntPrefix(s string) int64 {
	if s == "" {
		return 0
	}
	neg := false
	i := 0
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		i = 1
		if i >= len(s) {
			return 0
		}
	}
	var n int64
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		return -n
	}
	return n
}
