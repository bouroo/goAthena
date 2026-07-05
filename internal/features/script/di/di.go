// Package di wires the script engine into the zone service's DI container.
package di

import "github.com/samber/do/v2"

// Register wires the script engine (lexer, parser, VM, hot-reload).
// TODO(DEL-04): implement.
func Register(c do.Injector) error {
	return nil
}
