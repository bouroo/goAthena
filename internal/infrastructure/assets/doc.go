// Package assets provides the GRF archive decoder, LRU asset cache,
// and EUC-KR<->UTF-8 text conversion for game data files.
//
// The package is consumed by zone-service to load map data (.rsw, .gnd, .gat)
// and NPC scripts. It is safe for use across multiple goroutines via the
// thread-safe Cache; the GRF reader itself is single-goroutine by design.
package assets
