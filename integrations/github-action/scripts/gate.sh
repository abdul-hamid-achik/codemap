#!/usr/bin/env bash
# Reads FAIL_ON_UNTESTED / FAIL_ON_RISK and the review JSON body directly, and
# is also the action's one source for the risk-level/risk-score/
# untested-count/changed-symbols-count/analysis-complete outputs (set even when
# the gate trips, via `>> $GITHUB_OUTPUT` before `exit 1` — see set_output
# below).
#
# Called by: action.yml step id=gate; gitlab/codemap-review.yml (unmodified —
# only $GITHUB_OUTPUT-writing has no GitLab consumer today since the CI
# template doesn't surface job outputs, but the script still runs there for
# its pass/fail exit code).
#
# codemap review supports native gate flags and exit 6, but run-review.sh
# deliberately invokes it without those flags so the Action can render/post
# and set outputs first. This final script decides pass/fail for the whole
# Action from the already-produced JSON fields:
#   - analysis_complete must be true whenever either policy gate is enabled
#   - FAIL_ON_UNTESTED trips when untested symbols exist OR mapped symbols'
#     call graph leaves test coverage unresolved/unknown
#   - FAIL_ON_RISK trips when .risk.level's ordinal is >= the threshold's
#     ordinal — via an EXPLICIT ordinal table, not lexical `>=` string
#     comparison, so risk.level "unknown" (a real enum value meaning "codemap
#     could not classify this diff's risk") can never coincidentally satisfy a
#     numeric-feeling threshold, and an ABSENT risk object (diff touched no
#     indexed symbols) never trips either gate.
set -euo pipefail

# Apply defaults only when a caller omitted a variable. An explicitly empty
# boolean is a configuration error and must survive through validation below.
: "${FAIL_ON_UNTESTED=false}"
: "${FAIL_ON_RISK=}"
: "${CODEMAP_PRECISE=true}"
: "${INSTALL_TS_LANGUAGE_SERVER=false}"
: "${INSTALL_PYRIGHT=false}"
: "${SKIP_COMMENT=false}"

validate_boolean() {
  local input_name="$1" value="$2"
  case "$value" in
    true | false) ;;
    *)
      echo "::error::${input_name} '${value}' is invalid; expected true or false" >&2
      return 1
      ;;
  esac
}

validate_inputs() {
  validate_boolean "fail-on-untested" "$FAIL_ON_UNTESTED"
  validate_boolean "precise" "$CODEMAP_PRECISE"
  validate_boolean "install-ts-language-server" "$INSTALL_TS_LANGUAGE_SERVER"
  validate_boolean "install-pyright" "$INSTALL_PYRIGHT"
  validate_boolean "skip-comment" "$SKIP_COMMENT"

  case "$FAIL_ON_RISK" in
    "" | low | medium | high) ;;
    *)
      echo "::error::fail-on-risk '${FAIL_ON_RISK}' is invalid; expected low, medium, high, or an empty value to disable the gate" >&2
      return 1
      ;;
  esac
}

# action.yml calls this mode before installing codemap so configuration
# mistakes fail in seconds rather than after an index and review. The normal
# invocation validates again so direct/GitLab callers get the same contract.
if [[ "${1:-}" == "--validate-inputs" ]]; then
  validate_inputs
  echo "codemap-action: action inputs are valid"
  exit 0
fi

validate_inputs

REVIEW_JSON="${1:-${REVIEW_JSON_PATH:-}}"
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
  set_output "risk-level" "unknown"
  set_output "risk-score" "0"
  set_output "untested-count" "0"
  set_output "changed-symbols-count" "0"
  set_output "analysis-complete" "unknown"

  if [[ "$FAIL_ON_UNTESTED" == "true" || -n "$FAIL_ON_RISK" ]]; then
    echo "::error::codemap review schema_version '${schema_version}' is unsupported (expected 1); configured gates cannot be evaluated safely" >&2
    exit 1
  fi

  echo "::warning::codemap review schema_version '${schema_version}' is unsupported (expected 1); no gates are enabled, so this reporting-only run remains nonblocking"
  exit 0
fi

untested_count="$(jq -r '(.untested_symbols // []) | length' "$REVIEW_JSON")"
changed_symbols_count="$(jq -r 'if (.total_symbols | type) == "number" then .total_symbols else ((.changed_symbols // []) | length) end' "$REVIEW_JSON")"
analysis_complete="$(jq -r 'if .analysis_complete == true then "true" elif .analysis_complete == false then "false" else "unknown" end' "$REVIEW_JSON")"
call_graph="$(jq -r '.call_graph // "unknown"' "$REVIEW_JSON")"
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
policy_gate_enabled=false
if [[ "$FAIL_ON_UNTESTED" == "true" || -n "$FAIL_ON_RISK" ]]; then
  policy_gate_enabled=true
fi

if [[ "$analysis_complete" != "true" ]]; then
  if [[ "$policy_gate_enabled" == "true" ]]; then
    tripped=1
    reasons+=("analysis-incomplete: analysis_complete is '${analysis_complete}'; configured gates cannot safely evaluate a partial review")
  else
    echo "::warning::codemap review analysis_complete is '${analysis_complete}'; no gates are enabled, so this reporting-only run remains nonblocking"
  fi
fi

if [[ "$FAIL_ON_UNTESTED" == "true" ]]; then
  if [[ "$untested_count" -gt 0 ]]; then
    tripped=1
    reasons+=("fail-on-untested: ${untested_count} changed symbol(s) have no covering test")
  elif [[ "$changed_symbols_count" -gt 0 && "$call_graph" != "resolved" && "$call_graph" != "name" ]]; then
    tripped=1
    reasons+=("fail-on-untested: test coverage is unresolved because call_graph is '${call_graph}'")
  fi
fi

if [[ -n "$FAIL_ON_RISK" ]]; then
  threshold_ord="$(ordinal "$FAIL_ON_RISK")"
  level_ord="$(ordinal "$risk_level")"
  if [[ "$level_ord" -ge 0 && "$level_ord" -ge "$threshold_ord" ]]; then
    tripped=1
    reasons+=("fail-on-risk: aggregate risk.level '${risk_level}' >= threshold '${FAIL_ON_RISK}'")
  fi
fi

set_output "risk-level" "$risk_level"
set_output "risk-score" "$risk_score"
set_output "untested-count" "$untested_count"
set_output "changed-symbols-count" "$changed_symbols_count"
set_output "analysis-complete" "$analysis_complete"

if [[ "$tripped" -eq 1 ]]; then
  echo "codemap-action: gate FAILED"
  for r in "${reasons[@]}"; do
    echo "  - $r"
  done
  exit 1
fi

echo "codemap-action: gate passed (risk-level=${risk_level}, untested-count=${untested_count}, analysis-complete=${analysis_complete})"
exit 0
