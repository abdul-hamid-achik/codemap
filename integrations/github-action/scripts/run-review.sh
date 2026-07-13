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

if [[ "$(git rev-parse --is-shallow-repository 2>/dev/null || echo true)" == "true" ]]; then
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
# toolchain, unresolved call_graph for LSP languages without their server) —
# a non-zero exit here would only happen on a genuine operational failure
# (e.g. corrupt DB), and review() below still produces a graceful, honest
# report even over a partially-indexed project, so this is intentionally not
# fatal; run-review continues and lets the render step surface any
# resulting resolution/call_graph caveat instead of hard-failing the job.
if ! codemap index "${index_args[@]}"; then
  echo "::warning::codemap index exited non-zero; continuing — codemap review degrades honestly over a partial or unresolved index and will surface that in its output"
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
  # {"ok":false,...} envelope is the ONE signal this script treats as a real
  # operational failure — unrelated to the risk/untested gate, which is
  # applied later from the success JSON (see file header + README).
  echo "::error::codemap review failed: ${msg} (code=${code})${hint:+ — hint: ${hint}}" >&2
  exit 1
fi

echo "REVIEW_JSON_PATH=${out}" >> "${GITHUB_ENV:?}"
echo "review-json-path=${out}" >> "${GITHUB_OUTPUT:?}"
echo "codemap-action: wrote review JSON to ${out} (native gate flags intentionally omitted; Action gate runs last)"
