package script

// Opcode is a VM operation code. It mirrors rAthena's c_op enum
// (rathena/src/map/script.hpp:222-265) but uses snake_case Go names and
// a compact 46-entry layout suitable for an Opcode uint8.
//
// The mapping is intentionally pragmatic, not byte-for-byte: rAthena
// distinguishes e.g. C_FUNC (builtin marker) from C_USERFUNC (user
// function marker); here both are subsumed under OpFunc with a name
// lookup deciding the kind at compile time. C_POS, C_NAME,
// C_USERFUNC_POS collapse to OpLabel with a name operand.
//
// Wire-level bytecode, when it is emitted by the compiler, will use a
// uint8 discriminant followed by the per-op operand layout described
// on each constant below.
type Opcode uint8

const (
	// OpEOF marks the end of the bytecode stream. Halts the VM.
	OpEOF Opcode = iota

	// OpExpr is a unary expression evaluation marker. Operand = none.
	OpExpr

	// OpExpr2 is a binary expression evaluation marker. Operand = none.
	OpExpr2

	// OpLAnd is logical AND short-circuit: pops 2, pushes bool. Operand = none.
	OpLAnd

	// OpLOr is logical OR short-circuit: pops 2, pushes bool. Operand = none.
	OpLOr

	// OpLEq is `==` equality. Pops 2, pushes bool. Operand = none.
	OpLEq

	// OpLNe is `!=` inequality. Pops 2, pushes bool. Operand = none.
	OpLNe

	// OpLGT is `>` greater-than. Operand = none.
	OpLGT

	// OpLLT is `<` less-than. Operand = none.
	OpLLT

	// OpLGE is `>=` greater-or-equal. Operand = none.
	OpLGE

	// OpLLE is `<=` less-or-equal. Operand = none.
	OpLLE

	// OpAdd is `+` arithmetic. Operand = none.
	OpAdd

	// OpSub is `-` arithmetic. Operand = none.
	OpSub

	// OpMul is `*` arithmetic. Operand = none.
	OpMul

	// OpDiv is `/` arithmetic. Operand = none.
	OpDiv

	// OpMod is `%` remainder. Operand = none.
	OpMod

	// OpNeg is unary `-`. Operand = none.
	OpNeg

	// OpNot is unary `!`. Operand = none.
	OpNot

	// OpAnd is bitwise `&`. Operand = none.
	OpAnd

	// OpOr is bitwise `|`. Operand = none.
	OpOr

	// OpXor is bitwise `^`. Operand = none.
	OpXor

	// OpBNot is unary bitwise `~`. Operand = none.
	OpBNot

	// OpShiftL is `<<`. Operand = none.
	OpShiftL

	// OpShiftR is `>>`. Operand = none.
	OpShiftR

	// OpAssign is `=`. Operand = none.
	OpAssign

	// OpAssignAdd is `+=`. Operand = none.
	OpAssignAdd

	// OpAssignSub is `-=`. Operand = none.
	OpAssignSub

	// OpAssignMul is `*=`. Operand = none.
	OpAssignMul

	// OpAssignDiv is `/=`. Operand = none.
	OpAssignDiv

	// OpAssignMod is `%=`. Operand = none.
	OpAssignMod

	// OpAssignShiftL is `<<=`. Operand = none.
	OpAssignShiftL

	// OpAssignShiftR is `>>=`. Operand = none.
	OpAssignShiftR

	// OpLabel marks a label definition / target. Operand = label name
	// (look up via CompiledScript.Labels).
	OpLabel

	// OpGoto is unconditional jump to label. Operand = label name.
	OpGoto

	// OpCallSub is a same-script label call (push return info + jump).
	// Operand = label name.
	OpCallSub

	// OpReturn returns from the current callsub/callfunc scope.
	// Operand = none.
	OpReturn

	// OpBinary marks a binary op boundary for argument-list emission
	// (`C_ARG` analogue in rAthena). Operand = none.
	//
	// Phase R0 (S1) also reuses OpBinary as the array-READ operator:
	// when the compiler emits an IndexExpr over an IdentExpr target, it
	// pushes the array name as a string and the index as an int, then
	// OpBinary pops both and resolves to the stored value via
	// ScopeStore.GetArray. The Str field of the instruction itself is
	// not used by this path.
	OpBinary

	// OpPush pushes a constant value onto the stack. The constant's
	// discriminant is implicit in the Instruction's Str/Operand fields.
	OpPush

	// OpVar is a variable reference (resolve by Str to a scope-backed
	// storage handle). Operand = Str field = variable name.
	OpVar

	// OpFunc invokes a built-in function. Operand = Str field = function
	// name (resolved via the BuiltinRegistry at compile time).
	OpFunc

	// OpName references a symbol by name (label, variable, function).
	// Operand = Str field = name. Used for argument-list name binding.
	OpName

	// OpEnd ends the current script activation. Operand = none.
	OpEnd

	// OpClose closes the player dialog. Operand = none.
	OpClose

	// OpInt is an int64 literal. Operand int32 field = value.
	OpInt

	// OpStr is a string literal. Operand Str field = decoded string.
	OpStr

	// OpLine is a line-number marker for stack-trace diagnostics.
	// Operand int32 field = 1-indexed source line.
	OpLine

	// OpIndexGet is the array-READ operator. Reserved for parity with
	// rAthena's C_GETINDEX / OpIndexGet split; the current compiler
	// emits OpBinary for reads and the VM handles both opcodes the
	// same way.
	//
	// Stack contract: pops idx (top) then name (string), pushes the
	// stored array element (zero Value when the array or index is
	// absent).
	OpIndexGet

	// OpIndexSet is the array-WRITE operator. Operand Str field = name.
	//
	// Stack contract: pops name (top), idx (second), value (third),
	// then stores value into ScopeStore.SetArray(name, idx).
	OpIndexSet
)

// String returns a mnemonic for debug disassembly.
func (o Opcode) String() string {
	if int(o) >= len(opcodeNames) {
		return opcodeName(uint8(o), "")
	}
	return opcodeNames[o]
}

func opcodeName(op uint8, def string) string {
	if int(op) < len(opcodeNames) {
		return opcodeNames[op]
	}
	if def != "" {
		return def
	}
	return "Op(?)"
}

var opcodeNames = [...]string{
	OpEOF:          opcodeStrEOF,
	OpExpr:         "EXPR",
	OpExpr2:        "EXPR2",
	OpLAnd:         "AND",
	OpLOr:          "OR",
	OpLEq:          "EQ",
	OpLNe:          "NE",
	OpLGT:          "GT",
	OpLLT:          "LT",
	OpLGE:          "GE",
	OpLLE:          "LE",
	OpAdd:          "ADD",
	OpSub:          "SUB",
	OpMul:          "MUL",
	OpDiv:          "DIV",
	OpMod:          "MOD",
	OpNeg:          "NEG",
	OpNot:          "LNOT",
	OpAnd:          "BAND",
	OpOr:           "BOR",
	OpXor:          "XOR",
	OpBNot:         "BNOT",
	OpShiftL:       "SHL",
	OpShiftR:       "SHR",
	OpAssign:       "ASSIGN",
	OpAssignAdd:    "ASSIGN_ADD",
	OpAssignSub:    "ASSIGN_SUB",
	OpAssignMul:    "ASSIGN_MUL",
	OpAssignDiv:    "ASSIGN_DIV",
	OpAssignMod:    "ASSIGN_MOD",
	OpAssignShiftL: "ASSIGN_SHL",
	OpAssignShiftR: "ASSIGN_SHR",
	OpLabel:        "LABEL",
	OpGoto:         "GOTO",
	OpCallSub:      "CALLSUB",
	OpReturn:       "RETURN",
	OpBinary:       "ARG",
	OpPush:         "PUSH",
	OpVar:          "VAR",
	OpFunc:         "FUNC",
	OpName:         "NAME",
	OpEnd:          "END",
	OpClose:        "CLOSE",
	OpInt:          "INT",
	OpStr:          "STR",
	OpLine:         "LINE",
	OpIndexGet:     "INDEX_GET",
	OpIndexSet:     "INDEX_SET",
}
