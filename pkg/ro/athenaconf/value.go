package athenaconf

import "strconv"

// Kind enumerates the concrete types a parsed .conf value can carry.
// The parser picks the narrowest Kind that round-trips the raw text.
type Kind int

const (
	// KindString is the default kind for unquoted values that do not
	// parse as int, float, or bool.
	KindString Kind = iota
	// KindInt tags values whose raw text parsed as a base-10 int64.
	KindInt
	// KindFloat tags values whose raw text parsed as a float64.
	KindFloat
	// KindBool tags rAthena yes/no/true/false/on/off literals.
	KindBool
)

// String renders Kind for error messages.
func (k Kind) String() string {
	switch k {
	case KindString:
		return "string"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindBool:
		return "bool"
	default:
		return "unknown"
	}
}

// Value is a typed scalar parsed from one `key: value` line. Exactly one
// of Str / Int / Flt / Bool is meaningful; the others are zero. Kind tells
// the caller which one to read.
type Value struct {
	Kind Kind
	Str  string
	Int  int64
	Flt  float64
	Bool bool
}

// AsString returns the value rendered as a string suitable for YAML
// emission. Numbers and bools are formatted canonically; strings are
// returned verbatim (caller decides on quoting).
func (v Value) AsString() string {
	switch v.Kind {
	case KindInt:
		return strconv.FormatInt(v.Int, 10)
	case KindFloat:
		return strconv.FormatFloat(v.Flt, 'g', -1, 64)
	case KindBool:
		if v.Bool {
			return "true"
		}
		return "false"
	default:
		return v.Str
	}
}

// intValue returns a Value tagged as KindInt, or an error if the raw text
// cannot be parsed as an int64.
func intValue(raw string) (Value, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return Value{}, strconvSyntaxError(raw, "int")
	}
	return Value{Kind: KindInt, Int: n, Str: raw}, nil
}

// floatValue returns a Value tagged as KindFloat, falling back to KindInt
// when the text is an integer literal (rAthena accepts "100" anywhere a
// rate is expected).
func floatValue(raw string) (Value, error) {
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return Value{Kind: KindFloat, Flt: f, Str: raw}, nil
	}
	return Value{}, strconvSyntaxError(raw, "float")
}

// boolValue interprets a rAthena boolean literal. Recognised tokens are
// yes/no/true/false/on/off, case-insensitive. Anything else returns an
// error so the caller can fall through to string parsing.
func boolValue(raw string) (Value, bool) {
	switch raw {
	case "yes", "Yes", "YES", "true", "True", "TRUE", "on", "On", "ON":
		return Value{Kind: KindBool, Bool: true, Str: raw}, true
	case "no", "No", "NO", "false", "False", "FALSE", "off", "Off", "OFF":
		return Value{Kind: KindBool, Bool: false, Str: raw}, true
	}
	return Value{}, false
}

// strconvSyntaxError produces an error whose message matches the shape
// produced by strconv.ParseInt/ParseFloat. Keeps the parser's error output
// uniform without reaching into strconv internals.
func strconvSyntaxError(raw, want string) error {
	return &parseError{msg: "invalid " + want + " value: " + raw}
}

type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }
