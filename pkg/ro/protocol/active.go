package protocol

import (
	"fmt"
	"sync"
)

// The resolved protocol set the server speaks.
//
// The set is installed once at boot, before any listener binds, and read
// from then on. The codecs are package-level functions, so the set is the
// one piece of global state the protocol layer carries; it is written once
// and only read afterwards.
var (
	// activeMu guards active.
	activeMu sync.RWMutex
	// active is the resolved set the server speaks.
	active *Set
)

// Install makes a source pack and a client family the server's protocol.
//
// The server calls it once at boot, before it binds a listener, so every
// codec reads the same set.
//
// It panics when the source is one the server does not know, because a
// misconfigured source must fail the start rather than every request.
func Install(source Source, variant Variant) *Set {
	set, ok := Resolve(source, variant)
	if !ok {
		panic(fmt.Sprintf("protocol: unknown source %q", source))
	}
	activeMu.Lock()
	defer activeMu.Unlock()
	active = set
	return set
}

// Active returns the resolved set the server speaks.
//
// It panics when no set is installed, because a codec that runs before the
// boot installed one has no protocol to speak.
func Active() *Set {
	activeMu.RLock()
	defer activeMu.RUnlock()
	if active == nil {
		panic("protocol: no protocol set is installed")
	}
	return active
}

// ActiveOpcodes returns the opcodes the server dispatches on, resolved from
// the installed set.
func ActiveOpcodes() Opcodes { return Active().Opcodes() }
