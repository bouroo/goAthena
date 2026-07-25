// Package transit is the bounded context for transit.
//
// the cross-map handshake over the NATS TransitMessenger port.
//
// At M0 this is an empty scaffold; domain/app/infra/di layers and value types
// land as this bounded context's milestone begins. Import boundaries are
// enforced by depguard (cross-module + intra-module) and internal/app/arch_test.
package transit
