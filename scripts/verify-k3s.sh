#!/bin/sh
# Validate the k3s manifests against the real Kubernetes API schema.
#
# The built-in schema catalog covers the core APIs but not Agones, so the
# Fleet CRD's schema is extracted from the Agones release manifest and used
# alongside it. Without that, the Fleets are skipped — and the Fleets carry
# the shard's contract with the transit MapDirectory, so they are the ones
# most worth checking.
#
# Usage: scripts/verify-k3s.sh [overlay]
#   overlay: a directory name under deploy/k3s/overlays (default: dev)
#
# Exits non-zero if any resource fails validation, and if kustomize cannot
# build the overlay at all.

set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"

overlay="${1:-dev}"
dir="deploy/k3s/overlays/$overlay"
if [ ! -d "$dir" ]; then
	echo "no such overlay: $dir" >&2
	exit 2
fi

# kubeconform: the schema validator. kustomize: the overlay builder.
# Both are installed through the Go proxy, which works where the release
# CDN does not.
for tool in "github.com/yannh/kubeconform/cmd/kubeconform@v0.8.0" \
	"sigs.k8s.io/kustomize/kustomize/v5@v5.8.3"; do
	# The module path's last segment is not always the binary's name:
	# kustomize's is "v5". Take the binary's name from the path and
	# strip a leading "v" only from a version segment.
	case "$tool" in
	*/cmd/*) name="${tool%%/cmd/*}"; name="${name##*/}" ;;
	*/v[0-9]*) name="${tool%%/v[0-9]*}"; name="${name##*/}" ;;
	*) name="${tool%%@*}"; name="${name##*/}" ;;
	esac
	bin="$(go env GOPATH)/bin/$name"
	if [ ! -x "$bin" ]; then
		echo "▶ installing $name"
		GOFLAGS=-mod=mod go install "$tool"
	fi
done
kubeconform="$(go env GOPATH)/bin/kubeconform"
kustomize="$(go env GOPATH)/bin/kustomize"

schema_dir="$(mktemp -d)"
# kubeconform selects files by extension, so the built manifest needs a
# .yaml name — a bare mktemp file reads as "no resources found".
out_dir="$(mktemp -d)"
out="$out_dir/manifest.yaml"
trap 'rm -rf "$schema_dir" "$out_dir"' EXIT

# The Agones CRD set, rendered (the Helm template alone is not valid YAML).
crd_url="https://raw.githubusercontent.com/googleforgames/agones/main/install/yaml/install.yaml"
echo "▶ fetching the Agones CRD schema"
have_agones=0
if curl -fsSL --max-time 180 "$crd_url" -o "$schema_dir/install.yaml"; then
	for kind in Fleet FleetAutoscaler GameServer; do
		# The file name must match kubeconform's template exactly.
		# The template expands {{.ResourceKind}} to the kind in LOWERCASE
		# — "fleet", not "Fleet" — so the name is fleet_v1.json. A name
		# that does not match is a silent miss: kubeconform reports
		# "could not find schema" and the resource fails. The case is
		# invisible on macOS, whose filesystem is case-insensitive; on
		# Linux it decides whether the check works at all.
		if go run ./scripts/crdschema "$schema_dir/install.yaml" "$kind" \
			"$schema_dir/$(printf '%s' "$kind" | tr '[:upper:]' '[:lower:]')_v1.json"; then
			have_agones=1
		fi
	done
fi
if [ "$have_agones" = 1 ]; then
	# The local schema comes FIRST. kubeconform tries the schema locations
	# in order, and the "default" catalog is a remote URL — for the Agones
	# kinds it 404s, and a network failure on that fetch aborts the
	# resource instead of falling through to the next location. With the
	# local schema first, the Fleets never reach the network at all.
	schema_flag="-schema-location $schema_dir/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json -schema-location default"
else
	echo "note: the Agones CRD schemas are unavailable; the Fleets will be skipped" >&2
	schema_flag=""
fi

# The schema must actually be found. kubeconform reports a resource whose
# schema it could not locate as an ERROR rather than an INVALID one, and
# the reason is buried in the summary — so a naming slip reads as "the
# manifests are broken" rather than "the schema was never loaded". The
# Fleets and the GameServer are the resources this check exists for;
# skipping them would make the check vacuous.
for kind in Fleet FleetAutoscaler GameServer; do
	lower="$(printf '%s' "$kind" | tr '[:upper:]' '[:lower:]')"
	if [ ! -f "$schema_dir/${lower}_v1.json" ]; then
		echo "the $kind schema was not extracted into $schema_dir" >&2
		exit 1
	fi
done

echo "▶ kustomize build $dir"
kustomize build "$dir" > "$out"

echo "▶ kubeconform -strict"
if [ -n "$schema_flag" ]; then
	# shellcheck disable=SC2086 # schema_flag is a deliberate word split.
	"$kubeconform" -strict -summary $schema_flag "$out"
else
	"$kubeconform" -strict -summary \
		-schema-location default \
		-ignore-missing-schemas \
		"$out"
fi
echo "k3s manifests valid: $dir"
