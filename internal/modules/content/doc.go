// Package content is the NPC-script bounded context. It bridges the script VM
// (pkg/ro/script) to the world module: loading and compiling the NPC script
// corpus at boot, publishing NPC entities into the world's registry and AOI
// grid, and serving the four dialog-handler opcodes (CZ_CONTACTNPC,
// CZ_REQNEXTSCRIPT, CZ_CHOOSEMENU, CZ_CLOSEDIALOG) that drive the
// goroutine-per-dialog VM execution.
//
// M11c ships the dialog subset (mes/next/close/percentheal) with a curated
// minimal corpus; M11d adds menu/select/warp builtins and the warper NPC.
package content
