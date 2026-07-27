package domain

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoHandler means no handler is registered for the opcode in the
// connection's current role table. Logged at warn and tolerated — an
// unimplemented opcode must not kill the session.
var ErrNoHandler = errors.New("no handler registered for opcode")

// PacketHandler serves one decoded inbound packet. It is a function type so a
// handler closes over its dependencies (repositories, registries) at
// registration time; a method value with this signature satisfies it too.
type PacketHandler func(ctx context.Context, conn Conn, frame Frame) error

// PacketHandlerTable maps an opcode to its handler for one role.
type PacketHandlerTable map[uint16]PacketHandler

// Dispatcher routes decoded frames to handlers via three role-keyed opcode
// tables. The tables are populated once at boot and never mutated afterwards,
// so the hot path is an unsynchronized map lookup. The fields are unexported
// to make "register after construction" unrepresentable — this is the
// lock-free invariant the table-driven design depends on.
type Dispatcher struct {
	login PacketHandlerTable
	char  PacketHandlerTable
	mapT  PacketHandlerTable
}

// NewDispatcher builds an immutable dispatcher from the three role tables. A
// nil table is treated as empty (every opcode in that role yields ErrNoHandler
// until handlers land). The maps are retained by reference; callers must not
// mutate them after hand-off.
func NewDispatcher(login, char, mapT PacketHandlerTable) *Dispatcher {
	return &Dispatcher{
		login: login,
		char:  char,
		mapT:  mapT,
	}
}

// table returns the handler map for a role. RoleLogin and any zero/unset role
// resolve to the login table — the conservative default for a fresh connection
// that has not yet completed the login handshake.
func (d *Dispatcher) table(r Role) PacketHandlerTable {
	if r == RoleChar {
		return d.char
	}
	if r == RoleMap {
		return d.mapT
	}
	return d.login
}

// Dispatch resolves and invokes the handler for (conn.Role(), cmd). It returns
// ErrNoHandler (wrapped with the opcode and role) when no handler is
// registered, so the caller can log and continue rather than disconnect.
func (d *Dispatcher) Dispatch(ctx context.Context, conn Conn, cmd uint16, frame []byte) error {
	h, ok := d.table(conn.Role())[cmd]
	if !ok {
		return fmt.Errorf("%w: cmd 0x%04x (%s)", ErrNoHandler, cmd, conn.Role())
	}
	return h(ctx, conn, Frame{Cmd: cmd, Raw: frame})
}
