// Package gateway is the bounded context for gateway.
//
// ingress: packet codec, table-driven dispatch, and broadcast render.
//
// At M0 this is an empty scaffold; domain/app/infra/di layers and value types
// land as this bounded context's milestone begins. Import boundaries are
// enforced by depguard (cross-module + intra-module) and internal/app/arch_test.
package gateway
