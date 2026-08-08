package script

// Instruction is a single VM instruction. The Operand and Str fields
// carry the opcode-specific payload:
//
//   - Operand is the int payload for OpInt (literal), OpLine (line
//     number), and numeric label indices.
//   - Str is the string payload for OpStr (string literal), OpVar
//     (variable name), OpName (label/variable name), OpFunc (function
//     name), OpLabel/OpGoto/OpCallSub (label name).
//
// Both fields are present on every Instruction for simplicity; OpXXX
// constants that do not use a field leave it zero-valued.
//
// Pos is the source location the instruction was compiled from. The VM
// uses it to format stack traces; the parser uses it to attach errors
// during compilation.
type Instruction struct {
	Op      Opcode
	Operand int32
	Str     string
	Pos     Position
}

// String returns a disassembly-style line for debug printing.
func (i Instruction) String() string {
	//nolint:exhaustive // default branch formats all other opcodes via Opcode.String
	switch i.Op {
	case OpInt:
		return "INT " + itoa(int(i.Operand))
	case OpLine:
		return "LINE " + itoa(int(i.Operand))
	default:
		if i.Str != "" {
			return i.Op.String() + " " + i.Str
		}
		return i.Op.String()
	}
}

// CompiledScript is the bytecode for a single NPC script or named
// function script. Labels maps label names to the instruction index of
// the OpLabel that introduces them; the compiler backpatches
// OpGoto/OpCallSub/CFunc targets through this map.
type CompiledScript struct {
	Name         string
	Instructions []Instruction
	Labels       map[string]int
}

// NewCompiledScript returns an empty CompiledScript with an initialized
// Labels map. Use this rather than a struct literal to guarantee the
// map is non-nil so label inserts don't panic.
func NewCompiledScript(name string) *CompiledScript {
	return &CompiledScript{
		Name:   name,
		Labels: make(map[string]int),
	}
}

// LookupLabel returns the instruction index for a label and whether
// the label was found.
func (c *CompiledScript) LookupLabel(name string) (int, bool) {
	if c == nil {
		return 0, false
	}
	idx, ok := c.Labels[name]
	return idx, ok
}

// CompiledScriptSet is the immutable set of all compiled scripts at a
// snapshot in time. The script engine swaps an
// atomic.Pointer[CompiledScriptSet] to hot-reload new versions without
// interrupting in-flight VMs (per design decision D-003).
type CompiledScriptSet struct {
	Scripts map[string]*CompiledScript
	Funcs   map[string]*CompiledScript
	Warps   []WarpDef
	Shops   []ShopDef
}

// NewCompiledScriptSet returns an empty set with all maps initialized.
func NewCompiledScriptSet() *CompiledScriptSet {
	return &CompiledScriptSet{
		Scripts: make(map[string]*CompiledScript),
		Funcs:   make(map[string]*CompiledScript),
	}
}

// WarpKey identifies a warp portal by its source tile.
type WarpKey struct {
	MapName  string
	TriggerX int
	TriggerY int
}

// WarpDef defines a warp portal: clicking the source tile teleports the
// player to DestMap/DestX/DestY.
type WarpDef struct {
	MapName  string
	X        int
	Y        int
	TriggerX int
	TriggerY int
	DestMap  string
	DestX    int
	DestY    int
}

// Key returns the (map, x, y) tuple identifying the source tile.
func (w WarpDef) Key() WarpKey {
	return WarpKey{MapName: w.MapName, TriggerX: w.TriggerX, TriggerY: w.TriggerY}
}

// ShopDef defines a shop NPC: the dialog routes into a merchant UI
// listing Items.
type ShopDef struct {
	Name    string
	MapName string
	X       int
	Y       int
	Items   []ShopItem
}

// ShopItem is a single inventory slot offered by a ShopDef.
type ShopItem struct {
	ItemID int32
	Price  int32
}
