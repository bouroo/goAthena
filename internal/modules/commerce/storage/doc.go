// Package storage is the bounded context for storage.
//
// the warehouse and anti-dupe lock manager over the economy and inventory ports.
//
// At M0 this is an empty scaffold; domain/app/infra/di layers and value types
// land as this bounded context's milestone begins. Import boundaries are
// enforced by depguard (cross-module + intra-module) and internal/app/arch_test.
package storage
