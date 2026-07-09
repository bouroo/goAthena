# Vendored rAthena headers

This directory vendors the subset of rAthena C/C++ headers that goAthena's
build-time code generators consume, so `go generate ./...` is hermetic and
does not depend on a network fetch (which rate-limits on shared CI runners).

## Contents

- `src/map/clif_obfuscation.hpp` — consumed by `cmd/genpacket` to produce
  `pkg/ro/crypto/obfuscation_keys.go`.

## License

These files are from the [rAthena](https://github.com/rathena/rathena) project,
Copyright (c) rAthena Dev Teams, licensed under the GNU GPL v3 (the header
files carry the notice at their top). goAthena is also GPL-3.0, so vendoring
is license-compatible. See `../LICENSE`.

## Updating

1. Copy the new version of the header(s) from a current rAthena checkout.
2. Run `go generate ./pkg/ro/crypto` and commit the regenerated
   `obfuscation_keys.go` alongside the updated header if the keys changed.
3. Keep the `-ref <git-ref>` in the `go:generate` directive aligned with the
   rAthena ref the headers were copied from (currently `master`).