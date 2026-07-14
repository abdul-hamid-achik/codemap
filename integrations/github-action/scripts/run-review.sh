#!/usr/bin/env bash
# init + index + review, writing the JSON report to $RUNNER_TEMP and exposing
# its path as a step output (review-json-path) and $GITHUB_ENV
# (REVIEW_JSON_PATH) for the render/gate steps.
#
# Called by: action.yml step id=review; gitlab/codemap-review.yml (curled
# down and invoked unmodified).
#
# IMPORTANT: codemap review has native --fail-on-risk/--fail-on-untested
# gates that print the normal success report and then exit 6. This Action
# deliberately passes neither flag: it needs that JSON to render/post and set
# outputs before its final gate.sh step decides pass/fail. A structured
# `{"ok":false,...}` envelope is still an operational failure here.
set -euo pipefail

: "${CODEMAP_PRECISE:=true}"
: "${CODEMAP_DEPTH:=3}"
: "${CODEMAP_BASE_SHA:=}"
: "${GITHUB_WORKSPACE:=$(pwd)}"
: "${RUNNER_TEMP:=/tmp}"

cd "$GITHUB_WORKSPACE"

command -v codemap >/dev/null 2>&1 || {
  echo "::error::codemap not found on PATH — install-codemap.sh should have run first" >&2
  exit 1
}

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "::error::GITHUB_WORKSPACE is not a git repository; check out the repository before running codemap review" >&2
  exit 1
fi

if [[ "$(git rev-parse --is-shallow-repository)" == "true" ]]; then
  echo "::error::this checkout is shallow. 'codemap review --since <base-sha>' needs the merge-base history — add 'fetch-depth: 0' to your actions/checkout step (see this action's README)." >&2
  exit 1
fi

if [[ -z "$CODEMAP_BASE_SHA" ]]; then
  echo "::error::no base SHA resolved (inputs.base-sha was empty — this usually means the workflow ran on an event that isn't pull_request/push, so github.event.pull_request.base.sha and github.event.before are both unset). Pass base-sha explicitly for other triggers." >&2
  exit 1
fi

echo "codemap-action: codemap init"
codemap init

index_args=(--no-embed --no-tips)
if [[ "$CODEMAP_PRECISE" == "true" ]]; then
  index_args+=(--precise)
fi

echo "codemap-action: codemap index ${index_args[*]}"
# codemap index degrades honestly per-language (name-based Go without a go
# toolchain, unresolved call_graph for LSP languages without their server)
# while returning zero. A non-zero exit is therefore an operational failure,
# not an accuracy caveat: continuing could turn a partial/stale index into a
# false-green review.
set +e
codemap index "${index_args[@]}"
index_exit=$?
set -e
if [[ "$index_exit" -ne 0 ]]; then
  echo "::error::codemap index failed (exit ${index_exit}); refusing to review a potentially partial or stale index" >&2
  exit 1
fi

out="${RUNNER_TEMP}/codemap-review.json"
echo "codemap-action: codemap review --since ${CODEMAP_BASE_SHA} --depth ${CODEMAP_DEPTH} --json"
set +e
codemap review --since "$CODEMAP_BASE_SHA" --depth "$CODEMAP_DEPTH" --json > "$out"
review_exit=$?
set -e

if [[ ! -s "$out" ]]; then
  echo "::error::codemap review produced no output (process exit ${review_exit})" >&2
  exit 1
fi

if jq -e '.ok == false' "$out" >/dev/null 2>&1; then
  code="$(jq -r '.code // "unknown"' "$out")"
  msg="$(jq -r '.error // "unknown error"' "$out")"
  hint="$(jq -r '.hint // empty' "$out")"
  # cmd/codemap/errors.go exit taxonomy: 0 answered / 1 operational /
  # 2 not_found / 3 index_missing / 4 index_corrupt / 5 not_a_repo. Exit 6 is
  # gate_failed, but cannot occur here because this invocation supplies no
  # native gate flags. This
  # {"ok":false,...} envelope is an operational failure — unrelated to the
  # risk/untested gate, which is applied later from the success JSON (see file
  # header + README). A nonzero index exit is rejected earlier for the same
  # false-green reason.
  echo "::error::codemap review failed: ${msg} (code=${code})${hint:+ — hint: ${hint}}" >&2
  exit 1
fi

# No native gate flags are supplied above, so every nonzero review exit is an
# operational failure even if stdout happens to contain a valid-looking v1
# object. Accepting it would let a crashed/aborted analysis masquerade as a
# successful report.
if [[ "$review_exit" -ne 0 ]]; then
  report_note="$(jq -r '.note // empty' "$out" 2>/dev/null || true)"
  echo "::error::codemap review exited nonzero (${review_exit}); refusing its output as authoritative${report_note:+ — report note: ${report_note}}. Raw stdout remains at ${out}" >&2
  exit 1
fi

echo "REVIEW_JSON_PATH=${out}" >> "${GITHUB_ENV:?}"
echo "review-json-path=${out}" >> "${GITHUB_OUTPUT:?}"
echo "codemap-action: wrote review JSON to ${out} (native gate flags intentionally omitted; Action gate runs last)"
