# Refactor plan — `cmd/protocolgen` probe scaffolding

## Target

`cmd/protocolgen` (1293 LOC, 6 files) — the production code the most recent
commit touched, and the only part of that commit that is not test code.

## Smell

**Duplication with no canonical owner.** The generator asks the source's own
C++ compiler what the source resolves to, and the compiler invocation is
restated in four places:

| Site | Restates |
|---|---|
| `resolve.go:245-266` (`preprocess`) | the include-path list, the PACKETVER flags, the compiler exec |
| `opcodes.go:162-188` (`probeOpcodesFor`) | the same list, byte-identical to `resolve.go`'s |
| `sizes.go:138-165` (`compile`) | the same list, minus `-E`, plus `-o` |
| `layouts.go:138-213` (`probeFieldOffsets`) | the temp dir, the stub setup, the write, the compile, the run |

A second, smaller instance: `resolveTable` (`resolve.go:54`) and
`registrationOrderFor` (`resolve.go:95`) each run the same three-input
preamble — `preprocess`, `resolveOpcodes`, `structLayouts` — and differ only
in how they consume the registrations.

The cost is not the line count. The include-path list is a fact about the
source's tree layout; stated three times, a change to it (a new third-party
dependency path) fixes one copy and leaves two pointing at the wrong place,
and the generator then silently resolves the wrong thing. That is the defect
the `TWINS:` gate exists for.

## Constraints

- **Behavior-preserving.** The generator's resolved values are the server's
  input; they must not change.
- Public surface (`cmd/protocolgen`'s flags and output files) unchanged.
- Go 1.27.3 baseline; the repo's own gates decide done.

## Regression threshold

`scripts/verify-protocolgen.sh` regenerates both source packs' tables and
compares the resolved values to the committed ones.

- **Before:** all 10 files OK, exit 0. (committed at `d2992cd`)
- **After:** all 10 files OK, exit 0. Any FAIL — revert that step.
- Plus the repo's gates: `task fmt-check`, `task lint`, `task vet`,
  `task scan-sec`, `task scan-vuln`, unit + integration suites.

## Plan

1. One owner for the compiler invocation: a single function that builds the
   argument list, and one that runs the compiler. The three copies call it.
2. One owner for the probe workspace: temp dir + stub dir + probe source
   file, so the four sites stop restating it.
3. One owner for the three-input resolution preamble, so
   `resolveTable` and `registrationOrderFor` share it.

Each step is its own commit, green at every step.

## Non-goals

- Not changing what the generator resolves.
- Not changing the generated files' format.
- Not touching `pkg/ro/protocol` (the runtime side).
