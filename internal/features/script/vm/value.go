package vm

// Value is a runtime value (int or string).
type Value struct {
	IsString bool
	Int      int64
	Str      string
}

// IntValue returns a new integer Value.
func IntValue(n int64) Value {
	return Value{Int: n}
}

// StrValue returns a new string Value.
func StrValue(s string) Value {
	return Value{IsString: true, Str: s}
}

// AsInt coerces a Value to int64. Strings parse as 0.
func (v Value) AsInt() int64 {
	if v.IsString {
		return 0
	}
	return v.Int
}

// AsStr coerces a Value to string. Integers return an empty string for
// simplicity; rAthena uses a conversion function, but the MVP stubs
// consume values directly.
func (v Value) AsStr() string {
	if v.IsString {
		return v.Str
	}
	return ""
}

// IsTruthy converts a Value to a boolean using rAthena's truthiness
// rules: 0 and empty strings are false; everything else is true.
func (v Value) IsTruthy() bool {
	if v.IsString {
		return v.Str != ""
	}
	return v.Int != 0
}
