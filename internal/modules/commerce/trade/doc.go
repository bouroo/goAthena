// Package trade is the bounded context for trade.
//
// the player-to-player trade state machine over the economy and inventory ports.
//
// At M0 this is an empty scaffold; domain/app/infra/di layers and value types
// land as this bounded context's milestone begins. Import boundaries are
// enforced by depguard (cross-module + intra-module) and internal/app/arch_test.
package trade
