// Package world is the bounded context for world.
//
// in-world entity lifecycle, AOI, tick loop, spawn, combat authority, and the agones adapter.
//
// At M0 this is an empty scaffold; domain/app/infra/di layers and value types
// land as this bounded context's milestone begins. Import boundaries are
// enforced by depguard (cross-module + intra-module) and internal/app/arch_test.
package world
