// Package vending is the bounded context for vending.
//
// the player vending use-case service over the economy and inventory ports.
//
// At M0 this is an empty scaffold; domain/app/infra/di layers and value types
// land as this bounded context's milestone begins. Import boundaries are
// enforced by depguard (cross-module + intra-module) and internal/app/arch_test.
package vending
