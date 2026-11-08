#!/bin/sh
# Regenerate the protocol tables from each source pack and compare them to
# the committed ones.
#
# The committed tables are the server's input; the generator is how they are
# produced. A refactor of the generator is only behavior-preserving if it
# still resolves the same values, so this compares the resolved values (the
# map entries), not the raw bytes: the generator emits unaligned map literals
# and gofmt aligns the committed ones.
#
# Usage: scripts/verify-protocolgen.sh [out-dir]
# Exit 0 when every generated file matches the committed one.

set -eu

out="${1:-$(mktemp -d)}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"

# The generator needs a C++ compiler; it asks the source's own compiler what
# the source resolves to.
compiler="${CXX:-}"
if [ -z "$compiler" ]; then
	for c in clang++ g++; do
		if command -v "$c" >/dev/null 2>&1; then
			compiler="$c"
			break
		fi
	done
fi
if [ -z "$compiler" ]; then
	echo "no C++ compiler found; set CXX" >&2
	exit 2
fi

mkdir -p "$out"
go build -o "$out/protocolgen" ./cmd/protocolgen

status=0
for pair in "thailand:third_party/rathenaThailand" "upstream:third_party/rathena"; do
	src="${pair%%:*}"
	path="${pair##*:}"
	if [ ! -d "$path" ]; then
		echo "SKIP $src: $path not checked out"
		continue
	fi
	"$out/protocolgen" -source "$src" -rathena "$path" -out "$out" >/dev/null

	for f in opcodes_gen.go layouts_main_gen.go layouts_re_gen.go packets_main_gen.go packets_re_gen.go; do
		gen="$out/$src/$f"
		want="pkg/ro/protocol/generated/$src/$f"
		if [ ! -f "$gen" ]; then
			echo "FAIL $src/$f: not generated"
			status=1
			continue
		fi
		# Compare the resolved entries, formatting-insensitive.
		a="$(gofmt "$gen" | grep -oE '"[^"]+":[[:space:]]*[0-9]+' | tr -d ' \t' | sort)"
		b="$(gofmt "$want" | grep -oE '"[^"]+":[[:space:]]*[0-9]+' | tr -d ' \t' | sort)"
		if [ "$a" = "$b" ]; then
			echo "OK   $src/$f"
		else
			echo "FAIL $src/$f: resolved values differ from the committed table"
			printf '%s\n' "$b" > "$out/want.txt"
			printf '%s\n' "$a" > "$out/got.txt"
			diff "$out/want.txt" "$out/got.txt" | head -20 || true
			status=1
		fi
	done
done

exit "$status"
