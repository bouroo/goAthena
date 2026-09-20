#!/usr/bin/env bash
# Shared helpers for the repo-local hook gate (.githook/*).
#
# The hooks are reached through a global `core.hooksPath` dispatcher
# (~/.git-hooks/_run-local-hook) that execs `<repo>/.githook/<hook>` when it
# exists and is executable. That makes the gate opt-in per repository and
# versioned with the code, unlike a hand-placed .git/hooks script.
#
# `task` is the single source of truth for the gate commands, so the hooks
# cannot drift from L1/L2. The hooks run the *check* variants (fmt-check, not
# fmt) so a hook never silently rewrites the worktree.

set -euo pipefail

gate_red() { printf '\033[31m%s\033[0m\n' "$*" >&2; }
gate_green() { printf '\033[32m%s\033[0m\n' "$*" >&2; }
gate_yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }

# Fast bail-out switch: GATE_SKIP=1 git commit ... / GATE_SKIP=lint git push ...
gate_skipped() { [ "${GATE_SKIP:-}" = "1" ]; }

gate_skip_reason() {
	if [ -n "${GATE_SKIP:-}" ] && [ "${GATE_SKIP}" != "1" ]; then
		case ",${GATE_SKIP}," in *",$1,"*) return 0 ;; esac
	fi
	return 1
}

# Locate the repo root even when the hook is invoked from a subdirectory.
gate_root() { git rev-parse --show-toplevel; }

# Run a task target with a labelled banner. Degrades to a skip (not a failure)
# when the toolchain is absent, so a machine without the dev tools can still
# commit — CI remains the authority.
gate_run() {
	local label="$1"
	shift
	if ! command -v task >/dev/null 2>&1; then
		gate_yellow "SKIP $label — 'task' not on PATH (go install github.com/go-task/task/v3/cmd/task@latest)"
		return 0
	fi
	printf '\033[36m▶ %s\033[0m\n' "$label" >&2
	if ! "$@"; then
		gate_red "✖ $label failed"
		return 1
	fi
	gate_green "✔ $label"
}

# Is this a file whose change should trigger the Go gate? (Hook-side mirror of
# the CI path filter.)
gate_is_relevant_path() {
	grep -qE '(\.(go|mod|sum|sql|ya?ml)$)|(^Taskfile)|(^\.golangci)|(^Containerfile)|(^compose\.yml)|(^\.github/)|(^\.githook/)'
}

# Change set for a pre-commit (staged) run.
gate_staged_files() { git diff --cached --name-only --diff-filter=ACMR; }

# Change set for a pre-push (pushed range) run: one line "name-only" of every
# file in the pushed range across all refs. Returns non-zero with no output
# when the push carries no build-relevant file.
gate_pushed_files() {
	local zero="0000000000000000000000000000000000000000"
	local _local_ref local_sha _remote_ref remote_sha base
	while read -r _local_ref local_sha _remote_ref remote_sha; do
		[ -z "${local_sha:-}" ] && continue
		# Branch deletion: nothing to verify.
		[ "$local_sha" = "$zero" ] && continue
		if [ "$remote_sha" = "$zero" ] || [ -z "${remote_sha:-}" ]; then
			# New remote branch: no base to diff against, so be conservative.
			printf '.\n'
			return 0
		fi
		base="$(git merge-base "$remote_sha" "$local_sha" 2>/dev/null || printf '%s' "$remote_sha")"
		git diff --name-only "$base" "$local_sha"
	done
}
