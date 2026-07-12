#!/usr/bin/env bash
# Reads FAIL_ON_UNTESTED / FAIL_ON_RISK and the review JSON body directly, and
# is also the action's one source for the risk-level/risk-score/
# untested-count/changed-symbols-count outputs (set even when the gate trips,
# via `>> $GITHUB_OUTPUT` before `exit 1` — see set_output below).
#
# Called by: action.yml step id=gate; gitlab/codemap-review.yml (unmodified —
# only $GITHUB_OUTPUT-writing has no GitLab consumer today since the CI
# template doesn't surface job outputs, but the script still runs there for
# its pass/fail exit code).
#
# codemap review's own process exit code is NEVER the failure signal here —
# see run-review.sh's header comment and the codemap-action README. This
# script is the one place that decides pass/fail for the whole Action, and it
# does so purely from the JSON fields:
#   - FAIL_ON_UNTESTED trips when (.untested_symbols // []) | length > 0
#   - FAIL_ON_RISK trips when .risk.level's ordinal is >= the threshold's
#     ordinal — via an EXPLICIT ordinal table, not lexical `>=` string
#     comparison, so risk.level "unknown" (a real enum value meaning "codemap
#     could not classify this diff's risk") can never coincidentally satisfy a
#     numeric-feeling threshold, and an ABSENT risk object (diff touched no
#     indexed symbols) never trips either gate.
set -euo pipefail

REVIEW_JSON="${1:-${REVIEW_JSON_PATH:-}}"
: "${FAIL_ON_UNTESTED:=false}"
: "${FAIL_ON_RISK:=}"

if [[ -z "$REVIEW_JSON" || ! -f "$REVIEW_JSON" ]]; then
  echo "::error::gate.sh needs the review JSON path (arg1 or REVIEW_JSON_PATH)" >&2
  exit 1
fi

set_output() {
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    echo "$1=$2" >> "$GITHUB_OUTPUT"
  fi
}

ordinal() {
  # Explicit table — deliberately not `[[ "$a" > "$b" ]]` lexical comparison.
  case "$1" in
    low) echo 1 ;;
    medium) echo 2 ;;
    high) echo 3 ;;
    *) echo -1 ;; # "unknown" or any unrecognized value: non-comparable, never trips a threshold
  esac
}

schema_version="$(jq -r '.schema_version // "absent"' "$REVIEW_JSON")"
if [[ "$schema_version" != "1" ]]; then
  echo "codemap-action: unrecognized schema_version '${schema_version}' — skipping gate checks (fail-soft; see render-comment.sh for the same fallback)"
  set_output "risk-level" "unknown"
  set_output "risk-score" "0"
  set_output "untested-count" "0"
  set_output "changed-symbols-count" "0"
  exit 0
fi

untested_count="$(jq -r '(.untested_symbols // []) | length' "$REVIEW_JSON")"
changed_symbols_count="$(jq -r '(.changed_symbols // []) | length' "$REVIEW_JSON")"
risk_present="$(jq -r 'if has("risk") then "true" else "false" end' "$REVIEW_JSON")"
if [[ "$risk_present" == "true" ]]; then
  risk_level="$(jq -r '.risk.level' "$REVIEW_JSON")"
  risk_score="$(jq -r '.risk.score' "$REVIEW_JSON")"
else
  risk_level="absent"
  risk_score="0"
fi

tripped=0
reasons=()

if [[ "$FAIL_ON_UNTESTED" == "true" && "$untested_count" -gt 0 ]]; then
  tripped=1
  reasons+=("fail-on-untested: ${untested_count} changed symbol(s) have no covering test")
fi

if [[ -n "$FAIL_ON_RISK" ]]; then
  threshold_ord="$(ordinal "$FAIL_ON_RISK")"
  level_ord="$(ordinal "$risk_level")"
  if [[ "$threshold_ord" -lt 0 ]]; then
    echo "::warning::fail-on-risk '${FAIL_ON_RISK}' is not one of low|medium|high — ignoring the risk gate"
  elif [[ "$level_ord" -ge 0 && "$level_ord" -ge "$threshold_ord" ]]; then
    tripped=1
    reasons+=("fail-on-risk: aggregate risk.level '${risk_level}' >= threshold '${FAIL_ON_RISK}'")
  fi
fi

set_output "risk-level" "$risk_level"
set_output "risk-score" "$risk_score"
set_output "untested-count" "$untested_count"
set_output "changed-symbols-count" "$changed_symbols_count"

if [[ "$tripped" -eq 1 ]]; then
  echo "codemap-action: gate FAILED"
  for r in "${reasons[@]}"; do
    echo "  - $r"
  done
  exit 1
fi

echo "codemap-action: gate passed (risk-level=${risk_level}, untested-count=${untested_count})"
exit 0
