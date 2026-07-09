#!/bin/sh
# Regenerate pkg/ro/crypto/obfuscation_keys.go from the rAthena submodule.
# The submodule (third_party/rathena) is uninitialized by default to keep
# clones light; the committed generated file is the source of truth. When the
# submodule header is absent this script is a no-op so `go generate ./...`
# succeeds in CI without initializing the submodule.
#
# To regenerate keys:
#   git submodule update --init --depth 1 third_party/rathena
#   go generate ./pkg/ro/crypto
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
header="$repo_root/third_party/rathena/src/map/clif_obfuscation.hpp"

if [ ! -f "$header" ]; then
  echo "gen-obfuscation-keys: third_party/rathena submodule not initialized; skipping (committed obfuscation_keys.go is authoritative)." >&2
  echo "  run: git submodule update --init --depth 1 third_party/rathena" >&2
  exit 0
fi

exec go run "$repo_root/cmd/genpacket" \
  -rathena "$repo_root/third_party/rathena" \
  -ref master \
  -o "$repo_root/pkg/ro/crypto/obfuscation_keys.go"
