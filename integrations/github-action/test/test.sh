#!/usr/bin/env bash
# Plain-bash test harness for codemap-action's scripts (bats-core isn't
# assumed to be installed; this runs anywhere bash+jq do). Exercises
# render-comment.sh and gate.sh against every fixture in testdata/, gate.sh's
# outputs (risk-level/risk-score/untested-count/changed-symbols-count/
# analysis-complete), input
# validation, and ordinal table explicitly; run-review.sh's operational
# failure boundaries; the composite Action's validate-first/gate-last sequencing,
# write-summary.sh's $GITHUB_STEP_SUMMARY fallback path,
# resolve-version.sh's archive-name construction for linux/amd64,
# darwin/arm64, and windows/amd64 (+ the windows/arm64 rejection), and
# install-codemap.sh's checksum verification (both the real happy path,
# network permitting, and a mocked mismatch).
#
# Run: ./test/test.sh   (or: task action:test)
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPTS="${ROOT_DIR}/scripts"
TESTDATA="${ROOT_DIR}/testdata"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0
skip=0

ok() { pass=$((pass + 1)); echo "  ok - $1"; }
bad() { fail=$((fail + 1)); echo "  FAIL - $1"; }
skipped() { skip=$((skip + 1)); echo "  skip - $1"; }

assert_contains() {
  local haystack="$1" needle="$2" desc="$3"
  if grep -qF -- "$needle" <<<"$haystack"; then
    ok "$desc"
  else
    bad "$desc (expected to find: ${needle@Q})"
  fi
}

assert_not_contains() {
  local haystack="$1" needle="$2" desc="$3"
  if grep -qF -- "$needle" <<<"$haystack"; then
    bad "$desc (did NOT expect to find: ${needle@Q})"
  else
    ok "$desc"
  fi
}

assert_eq() {
  local actual="$1" expected="$2" desc="$3"
  if [[ "$actual" == "$expected" ]]; then
    ok "$desc"
  else
    bad "$desc (expected '${expected}', got '${actual}')"
  fi
}

render() {
  # render(fixture [, max_bytes]) -> echoes the rendered markdown body to stdout
  local fixture="$1" max_bytes="${2:-60000}"
  local out_dir="${WORK}/render-${fixture}-${max_bytes}"
  mkdir -p "$out_dir"
  RUNNER_TEMP="$out_dir" CODEMAP_ACTION_VERSION="test" GITHUB_SHA="testsha" \
    CODEMAP_COMMENT_MAX_BYTES="$max_bytes" \
    bash "${SCRIPTS}/render-comment.sh" "${TESTDATA}/${fixture}.json" >/dev/null 2>"${out_dir}/stderr"
  cat "${out_dir}/codemap-review-comment.md" 2>/dev/null
}

gate() {
  # gate(fixture, fail_on_untested, fail_on_risk) -> sets $GATE_EXIT/$GATE_OUT/$GATE_LOG
  local fixture="$1" fu="$2" fr="$3"
  local out="${WORK}/gate-output-$$-${RANDOM}"
  local log="${WORK}/gate-log-$$-${RANDOM}"
  : > "$out"
  GITHUB_OUTPUT="$out" FAIL_ON_UNTESTED="$fu" FAIL_ON_RISK="$fr" \
    bash "${SCRIPTS}/gate.sh" "${TESTDATA}/${fixture}.json" >"$log" 2>&1
  GATE_EXIT=$?
  GATE_OUT="$(cat "$out")"
  GATE_LOG="$(cat "$log")"
}

validate_gate_inputs() {
  # validate_gate_inputs(fail_on_untested, fail_on_risk [, precise, ts_lsp, pyright, skip_comment])
  local fu="$1" fr="$2" precise="${3-true}" ts_lsp="${4-false}" pyright="${5-false}" skip_comment="${6-false}"
  VALIDATE_LOG="$(FAIL_ON_UNTESTED="$fu" FAIL_ON_RISK="$fr" CODEMAP_PRECISE="$precise" \
    INSTALL_TS_LANGUAGE_SERVER="$ts_lsp" INSTALL_PYRIGHT="$pyright" SKIP_COMMENT="$skip_comment" \
    bash "${SCRIPTS}/gate.sh" --validate-inputs 2>&1)"
  VALIDATE_EXIT=$?
}

validate_unset_defaults() {
  VALIDATE_LOG="$(env -u FAIL_ON_UNTESTED -u FAIL_ON_RISK -u CODEMAP_PRECISE \
    -u INSTALL_TS_LANGUAGE_SERVER -u INSTALL_PYRIGHT -u SKIP_COMMENT \
    bash "${SCRIPTS}/gate.sh" --validate-inputs 2>&1)"
  VALIDATE_EXIT=$?
}

run_review() {
  # run_review(index_exit, inside_work_tree [, review_exit, fixture]) -> sets $RUN_REVIEW_*
  local index_exit="$1" inside_work_tree="$2" review_exit="${3-0}" fixture="${4-golden-contract}"
  local case_dir="${WORK}/run-review-${index_exit}-${inside_work_tree}-${review_exit}-${RANDOM}"
  local mock_bin="${case_dir}/bin"
  mkdir -p "$mock_bin" "${case_dir}/workspace" "${case_dir}/tmp"

  cat > "${mock_bin}/git" <<'MOCKGIT'
#!/usr/bin/env bash
if [[ "$1" == "rev-parse" && "$2" == "--is-inside-work-tree" ]]; then
  if [[ "$MOCK_INSIDE_WORK_TREE" == "true" ]]; then
    echo true
    exit 0
  fi
  exit 128
fi
if [[ "$1" == "rev-parse" && "$2" == "--is-shallow-repository" ]]; then
  echo false
  exit 0
fi
exit 1
MOCKGIT

  cat > "${mock_bin}/codemap" <<'MOCKCODEMAP'
#!/usr/bin/env bash
echo "$*" >> "$MOCK_CODEMAP_LOG"
case "$1" in
  init) exit 0 ;;
  index) exit "$MOCK_INDEX_EXIT" ;;
  review)
    cat "$MOCK_REVIEW_JSON"
    exit "$MOCK_REVIEW_EXIT"
    ;;
esac
exit 1
MOCKCODEMAP
  chmod +x "${mock_bin}/git" "${mock_bin}/codemap"

  local env_file="${case_dir}/github-env"
  local output_file="${case_dir}/github-output"
  local log_file="${case_dir}/codemap.log"
  local stderr_file="${case_dir}/stderr"
  : > "$env_file"
  : > "$output_file"
  : > "$log_file"

  PATH="${mock_bin}:${PATH}" \
    MOCK_INDEX_EXIT="$index_exit" \
    MOCK_REVIEW_EXIT="$review_exit" \
    MOCK_INSIDE_WORK_TREE="$inside_work_tree" \
    MOCK_CODEMAP_LOG="$log_file" \
    MOCK_REVIEW_JSON="${TESTDATA}/${fixture}.json" \
    GITHUB_WORKSPACE="${case_dir}/workspace" \
    RUNNER_TEMP="${case_dir}/tmp" \
    GITHUB_ENV="$env_file" \
    GITHUB_OUTPUT="$output_file" \
    CODEMAP_BASE_SHA="base-sha" \
    bash "${SCRIPTS}/run-review.sh" >/dev/null 2>"$stderr_file"
  RUN_REVIEW_EXIT=$?
  RUN_REVIEW_LOG="$(cat "$log_file")"
  RUN_REVIEW_STDERR="$(cat "$stderr_file")"
  RUN_REVIEW_OUTPUTS="$(cat "$output_file")"
}

echo "== render-comment.sh: every fixture renders without crashing and stays under the byte cap =="
for f in "${TESTDATA}"/*.json; do
  name="$(basename "$f" .json)"
  body="$(render "$name")"
  if [[ -n "$body" ]]; then
    ok "renders: ${name}"
  else
    bad "renders: ${name} (empty output)"
  fi
  size=$(printf '%s' "$body" | wc -c | tr -d ' ')
  if [[ "$size" -lt 65536 ]]; then
    ok "${name}: ${size} bytes stays under GitHub's 65536-byte comment hard cap"
  else
    bad "${name}: ${size} bytes EXCEEDS GitHub's 65536-byte comment hard cap"
  fi
  # every non-schema-mismatch render carries the sticky marker
  assert_contains "$body" '<!-- codemap-review-action:marker -->' "${name}: carries the hidden sticky marker"
done

echo
echo "== render-comment.sh: content assertions per fixture =="

body="$(render golden-contract)"
assert_contains "$body" '⚪ `unknown`' "golden-contract: renders its contract-level unknown risk"
assert_contains "$body" 'call graph resolution is `name`' "golden-contract: call_graph=name surfaces as a caveat"
assert_contains "$body" 'the index is **stale**' "golden-contract: stale:true surfaces as a caveat"
assert_contains "$body" '**⚠️ Incomplete analysis.**' "golden-contract: analysis_complete=false is prominent"
assert_contains "$body" 'analyzed `1` of `1` mapped symbol(s); `0` were truncated' "golden-contract: completeness counts render literally"
assert_not_contains "$body" 'every changed symbol has at least one covering test' "golden-contract: incomplete analysis never makes the all-covered claim"
assert_contains "$body" 'none reported in the analyzed subset' "golden-contract: empty untested list is scoped to the analyzed subset"

body="$(render complete-low-risk)"
assert_contains "$body" '🟢 `low`' "complete-low-risk: complete low risk still renders normally"
assert_contains "$body" 'every changed symbol has at least one covering test' "complete-low-risk: complete analysis may make the all-covered claim"
assert_not_contains "$body" 'Incomplete analysis' "complete-low-risk: no incomplete-analysis warning"

body="$(render complete-name-based)"
assert_contains "$body" 'medium-confidence candidate' "complete-name-based: name-derived coverage is explicitly candidate evidence"
assert_contains "$body" '`go/types`' "complete-name-based: precise guidance points to Go's actual resolver"
assert_not_contains "$body" 'every changed symbol has at least one covering test' "complete-name-based: name matching never makes an absolute all-covered claim"
assert_not_contains "$body" 'treat blast radius/untested findings as a lower bound' "complete-name-based: name fan-out is not mislabeled as a lower bound"

body="$(render partial-analysis)"
assert_contains "$body" '**⚠️ Incomplete analysis.**' "partial-analysis: renders a prominent warning"
assert_contains "$body" 'analyzed `1` of `3` mapped symbol(s); `1` were truncated' "partial-analysis: renders total/analyzed/truncated counts"
assert_contains "$body" 'reports `1` partial error(s)' "partial-analysis: surfaces the bounded partial-error count"
assert_not_contains "$body" 'every changed symbol has at least one covering test' "partial-analysis: never claims complete test coverage"

body="$(render legacy-v1-no-completeness)"
assert_contains "$body" '**⚠️ Analysis completeness unknown.**' "legacy v1: absent completeness metadata is prominent"
assert_not_contains "$body" 'every changed symbol has at least one covering test' "legacy v1: absent completeness never makes the all-covered claim"

body="$(render real-since-untested-high-risk)"
assert_contains "$body" '🔴 `high`' "real fixture: renders risk high"
assert_contains "$body" '⚠️ Untested' "real fixture: renders the untested callout"
assert_contains "$body" 'app.Orphan' "real fixture: names the untested symbol"

body="$(render risk-absent)"
assert_contains "$body" 'not computed' "risk-absent: renders the no-risk-computed line"
assert_not_contains "$body" '🔴' "risk-absent: no risk emoji rendered"

body="$(render deletion-analysis)"
assert_contains "$body" 'deletes 2 file(s)' "deletion-analysis: caveat mentions file count"
assert_contains "$body" 'complete: false' "deletion-analysis: caveat surfaces incomplete deletion evidence"

body="$(render hotspots-present)"
assert_contains "$body" '**Hotspots**' "hotspots-present: renders the hotspots section"
assert_contains "$body" 'CoreDispatch' "hotspots-present: names the hotspot symbol"

body="$(render call-graph-unresolved)"
assert_contains "$body" 'call graph resolution is `unresolved`' "call-graph-unresolved: surfaces the unresolved caveat"
assert_contains "$body" '⚪ `unknown`' "call-graph-unresolved: risk.level unknown renders with the neutral emoji"
assert_contains "$body" '**Untested**: unknown' "call-graph-unresolved: empty untested list is not presented as confirmed coverage"
assert_not_contains "$body" 'every changed symbol has at least one covering test' "call-graph-unresolved: never makes a false all-covered claim"

body="$(render empty-diff)"
assert_contains "$body" 'No changed symbols mapped to indexed code' "empty-diff: renders the no-op-diff line"

body="$(render not-indexed)"
assert_contains "$body" 'not indexed' "not-indexed: renders the not-indexed notice"

body="$(render not-a-repo)"
assert_contains "$body" 'Not a git repository' "not-a-repo: renders the not-a-repo notice"

body="$(render unknown-schema-version)"
assert_contains "$body" "doesn't understand this codemap version" "unknown-schema-version: renders the fail-soft notice"
assert_contains "$body" '<!-- codemap-review-action:marker -->' "unknown-schema-version: still carries the sticky marker"

body="$(render huge-blast-radius)"
assert_contains "$body" '⚠️ Untested' "huge-blast-radius: untested headline present at default cap"
assert_contains "$body" '🔴 `high`' "huge-blast-radius: risk band present at default cap"
assert_contains "$body" 'more (deeper) node(s) not shown' "huge-blast-radius: per-section row cap (BLAST_CAP) elides the long tail even under the default byte budget"

echo
echo "== render-comment.sh: size-cap cascade (forced with a tiny byte budget) =="
echo "   drop order per the plan: blast-radius list -> changed-symbols list -> changed-files list;"
echo "   risk band and untested headline must NEVER be dropped."

body="$(render huge-blast-radius 3000)"
size=$(printf '%s' "$body" | wc -c | tr -d ' ')
assert_contains "$body" '**Blast radius**: 3000 symbol(s) reachable from this diff' "cascade: blast-radius list is replaced by a count-only line"
assert_not_contains "$body" 'Shallowest first' "cascade: the blast-radius table itself is gone (count-only, not just capped)"
assert_contains "$body" '⚠️ Untested' "cascade: untested headline survives even the smallest byte budget"
assert_contains "$body" '🔴 `high`' "cascade: risk band survives even the smallest byte budget"
if [[ "$size" -lt 3500 ]]; then
  ok "cascade: forcing a 3000-byte budget actually shrinks the body materially (${size} bytes, was ~5000 uncapped)"
else
  bad "cascade: expected the tiny byte budget to shrink the body; got ${size} bytes"
fi

body="$(render huge-blast-radius 800)"
assert_contains "$body" '**Changed symbols**: 120 (list omitted' "cascade: at 800 bytes, changed-symbols list is also dropped to a count"
assert_contains "$body" '⚠️ Untested' "cascade: untested headline still survives at 800 bytes"

body="$(render huge-blast-radius 300)"
assert_contains "$body" '**Files touched**: 60 (list omitted' "cascade: at 300 bytes, even changed-files is dropped to a count (last resort)"
assert_contains "$body" '⚠️ Untested' "cascade: untested headline still survives at the smallest forced budget"
assert_contains "$body" '<!-- codemap-review-action:marker -->' "cascade: sticky marker survives the smallest forced budget"

echo
echo "== gate.sh: ordinal table (the load-bearing correctness property) =="

gate call-graph-unresolved false high
assert_eq "$GATE_EXIT" "0" "risk.level 'unknown' does NOT trip fail-on-risk high"
assert_not_contains "$GATE_LOG" "analysis-incomplete" "complete analysis with unknown risk is not mistaken for a partial review"

gate call-graph-unresolved true ""
assert_eq "$GATE_EXIT" "1" "fail-on-untested fails closed when mapped symbols' test coverage is unresolved"
assert_contains "$GATE_LOG" "test coverage is unresolved" "unresolved test-coverage gate reports the fail-closed reason"

gate risk-absent true high
assert_eq "$GATE_EXIT" "0" "absent risk object does NOT trip fail-on-risk"
assert_eq "$GATE_EXIT" "0" "absent risk object does NOT trip fail-on-untested (0 untested)"

gate hotspots-present false low
assert_eq "$GATE_EXIT" "1" "risk.level 'medium' DOES trip fail-on-risk low"

gate hotspots-present false high
assert_eq "$GATE_EXIT" "0" "risk.level 'medium' does NOT trip fail-on-risk high"

gate complete-low-risk false low
assert_eq "$GATE_EXIT" "1" "risk.level 'low' trips fail-on-risk low (>= is inclusive of the threshold itself)"

gate complete-low-risk false high
assert_eq "$GATE_EXIT" "0" "risk.level 'low' does NOT trip fail-on-risk high"

gate real-since-untested-high-risk true ""
assert_eq "$GATE_EXIT" "1" "fail-on-untested trips when untested_symbols is non-empty"

gate real-since-untested-high-risk false ""
assert_eq "$GATE_EXIT" "0" "fail-on-untested does not trip when the input is false, even with untested symbols present"

gate unknown-schema-version true high
assert_eq "$GATE_EXIT" "1" "unrecognized schema_version fails closed when a risk or untested gate is enabled"

gate unknown-schema-version false ""
assert_eq "$GATE_EXIT" "0" "unrecognized schema_version remains nonblocking for an explicitly ungated reporting-only run"

gate partial-analysis false high
assert_eq "$GATE_EXIT" "1" "analysis_complete=false fails closed when the risk gate is enabled"
assert_contains "$GATE_LOG" "analysis-incomplete" "incomplete gated review reports the fail-closed reason"

gate partial-analysis true ""
assert_eq "$GATE_EXIT" "1" "analysis_complete=false fails closed when the untested gate is enabled"

gate partial-analysis false ""
assert_eq "$GATE_EXIT" "0" "analysis_complete=false remains nonblocking in reporting-only mode"
assert_contains "$GATE_LOG" "::warning::" "incomplete reporting-only review emits a warning"

gate legacy-v1-no-completeness false high
assert_eq "$GATE_EXIT" "1" "older schema-v1 payload without analysis_complete fails closed when gated"

gate legacy-v1-no-completeness false ""
assert_eq "$GATE_EXIT" "0" "older schema-v1 payload without analysis_complete remains nonblocking when reporting-only"
assert_contains "$GATE_LOG" "analysis_complete is 'unknown'" "older reporting-only review warns that completeness is unknown"

gate golden-contract false bogus
assert_eq "$GATE_EXIT" "1" "an unrecognized fail-on-risk value fails instead of silently disabling the gate"

echo
echo "== gate.sh: fail-fast input validation =="

validate_gate_inputs false high
assert_eq "$VALIDATE_EXIT" "0" "valid gate inputs pass standalone validation"

validate_gate_inputs false bogus
assert_eq "$VALIDATE_EXIT" "1" "invalid fail-on-risk fails standalone validation"
assert_contains "$VALIDATE_LOG" "expected low, medium, high" "invalid fail-on-risk reports the accepted values"

validate_gate_inputs sometimes ""
assert_eq "$VALIDATE_EXIT" "1" "invalid fail-on-untested fails standalone validation"

validate_gate_inputs false "" sometimes
assert_eq "$VALIDATE_EXIT" "1" "invalid precise fails standalone validation"

validate_gate_inputs false "" true sometimes
assert_eq "$VALIDATE_EXIT" "1" "invalid install-ts-language-server fails standalone validation"

validate_gate_inputs false "" true false sometimes
assert_eq "$VALIDATE_EXIT" "1" "invalid install-pyright fails standalone validation"

validate_gate_inputs false "" true false false sometimes
assert_eq "$VALIDATE_EXIT" "1" "invalid skip-comment fails standalone validation"

validate_gate_inputs "" ""
assert_eq "$VALIDATE_EXIT" "1" "explicitly empty fail-on-untested is rejected instead of receiving its default"

validate_gate_inputs false "" ""
assert_eq "$VALIDATE_EXIT" "1" "explicitly empty precise is rejected instead of receiving its default"

validate_gate_inputs false "" true ""
assert_eq "$VALIDATE_EXIT" "1" "explicitly empty install-ts-language-server is rejected"

validate_gate_inputs false "" true false ""
assert_eq "$VALIDATE_EXIT" "1" "explicitly empty install-pyright is rejected"

validate_gate_inputs false "" true false false ""
assert_eq "$VALIDATE_EXIT" "1" "explicitly empty skip-comment is rejected"

validate_unset_defaults
assert_eq "$VALIDATE_EXIT" "0" "omitted boolean inputs still receive their documented defaults"

echo
echo "== gate.sh: outputs =="
gate real-since-untested-high-risk true ""
assert_contains "$GATE_OUT" "risk-level=high" "gate.sh sets the risk-level output even when the gate trips"
assert_contains "$GATE_OUT" "untested-count=1" "gate.sh sets the untested-count output even when the gate trips"
assert_contains "$GATE_OUT" "risk-score=0.9" "gate.sh sets the risk-score output (0.9 from the fixture's risk.score) even when the gate trips"
assert_contains "$GATE_OUT" "changed-symbols-count=1" "gate.sh sets the changed-symbols-count output even when the gate trips"
assert_contains "$GATE_OUT" "analysis-complete=true" "gate.sh sets analysis-complete even when a content gate trips"

gate partial-analysis false high
assert_contains "$GATE_OUT" "analysis-complete=false" "gate.sh exposes explicit incomplete analysis even when completeness trips the gate"
assert_contains "$GATE_OUT" "changed-symbols-count=3" "gate.sh uses total_symbols instead of the capped changed_symbols array length"

gate legacy-v1-no-completeness false ""
assert_contains "$GATE_OUT" "analysis-complete=unknown" "gate.sh exposes unknown for older schema-v1 reports without completeness metadata"
assert_contains "$GATE_OUT" "changed-symbols-count=1" "gate.sh falls back to changed_symbols length for older schema-v1 reports"

gate risk-absent false ""
assert_contains "$GATE_OUT" "risk-score=0" "gate.sh: risk-score is '0' when the diff has no risk object at all"
assert_contains "$GATE_OUT" "changed-symbols-count=0" "gate.sh: a docs-only diff reports zero mapped symbols when risk is absent"

gate unknown-schema-version false ""
assert_contains "$GATE_OUT" "risk-score=0" "gate.sh: unrecognized schema_version still sets risk-score (fail-soft default '0')"
assert_contains "$GATE_OUT" "changed-symbols-count=0" "gate.sh: unrecognized schema_version still sets changed-symbols-count (fail-soft default '0')"
assert_contains "$GATE_OUT" "analysis-complete=unknown" "gate.sh: unrecognized schema_version sets analysis-complete=unknown"

echo
echo "== run-review.sh: operational failures fail closed =="

run_review 23 true
assert_eq "$RUN_REVIEW_EXIT" "1" "a nonzero codemap index exit fails the review step"
assert_contains "$RUN_REVIEW_STDERR" "refusing to review a potentially partial or stale index" "index failure explains the false-green protection"
assert_contains "$RUN_REVIEW_LOG" "index --no-embed --no-tips --precise" "run-review.sh attempted the configured precise index"
assert_not_contains "$RUN_REVIEW_LOG" "review --since" "run-review.sh does not review after an index failure"

run_review 0 true
assert_eq "$RUN_REVIEW_EXIT" "0" "a successful index proceeds to review"
assert_contains "$RUN_REVIEW_LOG" "review --since base-sha --depth 3 --json" "successful indexing invokes the expected review command"
assert_contains "$RUN_REVIEW_OUTPUTS" "review-json-path=" "successful review exposes its JSON path"

run_review 0 true 17 complete-low-risk
assert_eq "$RUN_REVIEW_EXIT" "1" "any nonzero codemap review exit is fatal even with valid-looking schema-v1 stdout"
assert_contains "$RUN_REVIEW_STDERR" "codemap review exited nonzero (17)" "nonzero review exit reports the process status"
assert_eq "$RUN_REVIEW_OUTPUTS" "" "nonzero review exit does not expose its JSON as a successful action output"

run_review 0 false
assert_eq "$RUN_REVIEW_EXIT" "1" "a missing git checkout fails before init/index/review"
assert_contains "$RUN_REVIEW_STDERR" "not a git repository" "missing checkout reports the actual precondition"
assert_eq "$RUN_REVIEW_LOG" "" "missing checkout does not invoke codemap"

echo
echo "== action gate sequencing: report and outputs exist before failure =="
review_invocation="$(grep -E '^codemap review --since ' "${SCRIPTS}/run-review.sh")"
assert_not_contains "$review_invocation" "--fail-on-" "run-review.sh deliberately omits native gate flags so a success JSON is always available to render"

validate_step_line="$(grep -nF -- '    - name: Validate action inputs' "${ROOT_DIR}/action.yml" | cut -d: -f1)"
resolve_step_line="$(grep -nF -- '    - name: Resolve codemap version + platform' "${ROOT_DIR}/action.yml" | cut -d: -f1)"
if [[ -n "$validate_step_line" && -n "$resolve_step_line" && "$validate_step_line" -lt "$resolve_step_line" ]]; then
  ok "action.yml validates action inputs before downloading or running codemap"
else
  bad "action.yml must validate gate inputs before setup work (validate=${validate_step_line:-missing}, resolve=${resolve_step_line:-missing})"
fi
assert_contains "$(grep -F -- 'gate.sh --validate-inputs' "${ROOT_DIR}/action.yml")" "gate.sh --validate-inputs" "action.yml uses gate.sh's shared validation contract"
assert_contains "$(grep -F -- 'CODEMAP_PRECISE:' "${ROOT_DIR}/action.yml")" 'inputs.precise' "action.yml validates precise before setup"
assert_contains "$(grep -F -- 'INSTALL_TS_LANGUAGE_SERVER:' "${ROOT_DIR}/action.yml")" 'inputs.install-ts-language-server' "action.yml validates the TypeScript language-server toggle before setup"
assert_contains "$(grep -F -- 'INSTALL_PYRIGHT:' "${ROOT_DIR}/action.yml")" 'inputs.install-pyright' "action.yml validates the Pyright toggle before setup"
assert_contains "$(grep -F -- 'SKIP_COMMENT:' "${ROOT_DIR}/action.yml")" 'inputs.skip-comment' "action.yml validates skip-comment before setup"
assert_contains "$(grep -F -- 'analysis-complete:' "${ROOT_DIR}/../../.github/workflows/codemap-review-reusable.yml")" 'analysis-complete' "reusable workflow forwards the analysis-complete output"

gitlab_template="${ROOT_DIR}/gitlab/codemap-review.yml"
gitlab_chmod_line="$(grep -nF -- '    - chmod +x ./*.sh' "$gitlab_template" | cut -d: -f1)"
gitlab_validate_line="$(grep -nF -- '    - ./gate.sh --validate-inputs' "$gitlab_template" | cut -d: -f1)"
gitlab_resolve_line="$(grep -nF -- '    - CODEMAP_VERSION="$CODEMAP_VERSION_INPUT" ./resolve-version.sh' "$gitlab_template" | cut -d: -f1)"
gitlab_install_line="$(grep -nF -- '    - ./install-codemap.sh' "$gitlab_template" | cut -d: -f1)"
gitlab_review_line="$(grep -nF -- '    - ./run-review.sh' "$gitlab_template" | cut -d: -f1)"
gitlab_render_line="$(grep -nF -- '    - ./render-comment.sh' "$gitlab_template" | cut -d: -f1)"
gitlab_comment_line="$(grep -nF -- '    - ./post-comment-gitlab.sh' "$gitlab_template" | cut -d: -f1)"
if [[ -n "$gitlab_chmod_line" && -n "$gitlab_validate_line" && -n "$gitlab_resolve_line" \
  && -n "$gitlab_install_line" && -n "$gitlab_review_line" && -n "$gitlab_render_line" \
  && -n "$gitlab_comment_line" && "$gitlab_chmod_line" -lt "$gitlab_validate_line" \
  && "$gitlab_validate_line" -lt "$gitlab_resolve_line" \
  && "$gitlab_validate_line" -lt "$gitlab_install_line" \
  && "$gitlab_validate_line" -lt "$gitlab_review_line" \
  && "$gitlab_validate_line" -lt "$gitlab_render_line" \
  && "$gitlab_validate_line" -lt "$gitlab_comment_line" ]]; then
  ok "GitLab validates shared action inputs before resolve/install/index/comment work"
else
  bad "GitLab validation order is wrong (chmod=${gitlab_chmod_line:-missing}, validate=${gitlab_validate_line:-missing}, resolve=${gitlab_resolve_line:-missing}, install=${gitlab_install_line:-missing}, review=${gitlab_review_line:-missing}, render=${gitlab_render_line:-missing}, comment=${gitlab_comment_line:-missing})"
fi
assert_eq "$(grep -cF -- 'export FAIL_ON_UNTESTED=' "$gitlab_template")" "1" "GitLab exports FAIL_ON_UNTESTED once, before validation"
assert_eq "$(grep -cF -- 'export FAIL_ON_RISK=' "$gitlab_template")" "1" "GitLab exports FAIL_ON_RISK once, before validation"

summary_step_line="$(grep -nF -- '    - name: Write job summary' "${ROOT_DIR}/action.yml" | cut -d: -f1)"
gate_step_line="$(grep -nF -- '    - name: Gate on risk / untested' "${ROOT_DIR}/action.yml" | cut -d: -f1)"
if [[ -n "$summary_step_line" && -n "$gate_step_line" && "$gate_step_line" -gt "$summary_step_line" ]]; then
  ok "action.yml runs its gate after the job summary, so reporting survives a tripped gate"
else
  bad "action.yml must keep the gate after the job summary (summary=${summary_step_line:-missing}, gate=${gate_step_line:-missing})"
fi

echo
echo "== write-summary.sh: \$GITHUB_STEP_SUMMARY fallback path =="

write_summary() {
  # write_summary(comment_path, set_step_summary) -> sets $WS_EXIT, $WS_SUMMARY (file contents or "")
  local comment_path="$1" set_summary="$2"
  local summary_file="${WORK}/step-summary-$$-${RANDOM}"
  : > "$summary_file"
  if [[ "$set_summary" == "true" ]]; then
    COMMENT_PATH="$comment_path" GITHUB_STEP_SUMMARY="$summary_file" \
      bash "${SCRIPTS}/write-summary.sh" >/dev/null 2>&1
  else
    COMMENT_PATH="$comment_path" GITHUB_STEP_SUMMARY="" \
      bash "${SCRIPTS}/write-summary.sh" >/dev/null 2>&1
  fi
  WS_EXIT=$?
  WS_SUMMARY="$(cat "$summary_file" 2>/dev/null)"
}

rendered_comment="$(render golden-contract)"
comment_file="${WORK}/summary-source-comment.md"
printf '%s\n' "$rendered_comment" > "$comment_file"

write_summary "$comment_file" true
assert_eq "$WS_EXIT" "0" "write-summary.sh exits 0 when \$GITHUB_STEP_SUMMARY is set and the comment file exists"
assert_contains "$WS_SUMMARY" '<!-- codemap-review-action:marker -->' "write-summary.sh: appends the SAME rendered Markdown (sticky marker present) to the job summary"
assert_contains "$WS_SUMMARY" '⚪ `unknown`' "write-summary.sh: the job summary carries the same risk rendering as the PR comment (single rendering path)"
assert_contains "$WS_SUMMARY" '**⚠️ Incomplete analysis.**' "write-summary.sh: the job summary preserves the prominent completeness warning"

write_summary "${WORK}/does-not-exist.md" true
assert_eq "$WS_EXIT" "0" "write-summary.sh degrades gracefully (exit 0) when the rendered comment file is missing"
assert_eq "$WS_SUMMARY" "" "write-summary.sh: does not touch \$GITHUB_STEP_SUMMARY when there is nothing to write"

write_summary "$comment_file" false
assert_eq "$WS_EXIT" "0" "write-summary.sh exits 0 when \$GITHUB_STEP_SUMMARY is unset (e.g. not running in GitHub Actions)"

echo
echo "== resolve-version.sh: archive-name construction (no network needed for a pinned version) =="

resolve() {
  local os="$1" arch="$2" version="$3"
  local env_file="${WORK}/resolve-env-$$-${RANDOM}"
  : > "$env_file"
  RUNNER_OS="$os" RUNNER_ARCH="$arch" CODEMAP_VERSION="$version" GITHUB_ENV="$env_file" \
    bash "${SCRIPTS}/resolve-version.sh" >/dev/null 2>&1
  RESOLVE_EXIT=$?
  RESOLVE_ENV="$(cat "$env_file" 2>/dev/null)"
}

resolve Linux X64 v0.40.0
assert_eq "$RESOLVE_EXIT" "0" "linux/amd64 resolves"
assert_contains "$RESOLVE_ENV" "CODEMAP_ARCHIVE_OS=linux" "linux/amd64: os=linux"
assert_contains "$RESOLVE_ENV" "CODEMAP_ARCHIVE_ARCH=amd64" "linux/amd64: arch=amd64"
assert_contains "$RESOLVE_ENV" "CODEMAP_ARCHIVE_EXT=tar.gz" "linux/amd64: ext=tar.gz"
assert_contains "$RESOLVE_ENV" "CODEMAP_VERSION_NUM=0.40.0" "linux/amd64: version_num strips the leading 'v' (matches real GoReleaser asset codemap_0.40.0_...)"

resolve macOS ARM64 v0.40.0
assert_contains "$RESOLVE_ENV" "CODEMAP_ARCHIVE_OS=darwin" "darwin/arm64: os=darwin"
assert_contains "$RESOLVE_ENV" "CODEMAP_ARCHIVE_ARCH=arm64" "darwin/arm64: arch=arm64"
assert_contains "$RESOLVE_ENV" "CODEMAP_ARCHIVE_EXT=tar.gz" "darwin/arm64: ext=tar.gz"

resolve Windows X64 v0.40.0
assert_contains "$RESOLVE_ENV" "CODEMAP_ARCHIVE_OS=windows" "windows/amd64: os=windows"
assert_contains "$RESOLVE_ENV" "CODEMAP_ARCHIVE_EXT=zip" "windows/amd64: ext=zip (only windows uses zip per .goreleaser.yaml)"

resolve Windows ARM64 v0.40.0
assert_eq "$RESOLVE_EXIT" "1" "windows/arm64 is rejected (no such release build exists — .goreleaser.yaml ignores it)"

resolve Linux X64 0.40.0
assert_contains "$RESOLVE_ENV" "CODEMAP_TAG=v0.40.0" "a version input without a leading 'v' is normalized to 'v0.40.0'"

echo
echo "== resolve-version.sh: 'latest' (needs network — skips gracefully if unavailable) =="
if curl -sSL --max-time 5 -o /dev/null -w '' "https://api.github.com" 2>/dev/null; then
  resolve Linux X64 latest
  if [[ "$RESOLVE_EXIT" -eq 0 ]] && grep -q '^CODEMAP_TAG=v' <<<"$RESOLVE_ENV"; then
    ok "'latest' resolves to a concrete vX.Y.Z tag against the real GitHub API"
  else
    bad "'latest' resolution failed or didn't pin a concrete tag"
  fi
else
  skipped "'latest' resolution (no network access in this environment)"
fi

echo
echo "== install-codemap.sh: checksum verification =="

# Mismatch case: mock curl serves a bogus checksums.txt for a real archive.
mock_bin="${WORK}/mockbin"
mkdir -p "$mock_bin"
cat > "${mock_bin}/curl" <<'MOCKEOF'
#!/usr/bin/env bash
args=("$@")
url="${args[-1]}"
outfile=""
for i in "${!args[@]}"; do
  if [[ "${args[$i]}" == "-o" ]]; then
    outfile="${args[$((i+1))]}"
  fi
done
if [[ "$url" == *checksums.txt ]]; then
  echo "0000000000000000000000000000000000000000000000000000000000000000  codemap_0.40.0_darwin_arm64.tar.gz" > "$outfile"
  exit 0
fi
exec /usr/bin/curl "$@"
MOCKEOF
chmod +x "${mock_bin}/curl"

bad_bin_dir="${WORK}/badhash-bin"
env_file="${WORK}/install-env"
: > "$env_file"
{
  echo "CODEMAP_TAG=v0.40.0"
  echo "CODEMAP_VERSION_NUM=0.40.0"
  echo "CODEMAP_ARCHIVE_OS=darwin"
  echo "CODEMAP_ARCHIVE_ARCH=arm64"
  echo "CODEMAP_ARCHIVE_EXT=tar.gz"
  echo "CODEMAP_BIN_DIR=${bad_bin_dir}"
} > "$env_file"
set -a
# env_file is a dynamically generated fixture, not a fixed path.
# shellcheck disable=SC1090
source "$env_file"
set +a
export GITHUB_PATH="${WORK}/path-file"
: > "$GITHUB_PATH"
PATH="${mock_bin}:${PATH}" bash "${SCRIPTS}/install-codemap.sh" >/dev/null 2>&1
install_exit=$?
if [[ "$install_exit" -ne 0 && ! -x "${bad_bin_dir}/codemap" ]]; then
  ok "install-codemap.sh rejects a tampered checksums.txt and does not install the binary"
else
  bad "install-codemap.sh should have failed on a checksum mismatch (exit=${install_exit})"
fi

# Cache-hit case: pre-seed the bin dir; install-codemap.sh must skip
# network entirely (no curl needed at all — remove it from PATH).
cache_bin_dir="${WORK}/cache-hit-bin"
mkdir -p "$cache_bin_dir"
printf '#!/bin/sh\necho fake\n' > "${cache_bin_dir}/codemap"
chmod +x "${cache_bin_dir}/codemap"
CODEMAP_TAG=v0.40.0 CODEMAP_VERSION_NUM=0.40.0 CODEMAP_ARCHIVE_OS=darwin CODEMAP_ARCHIVE_ARCH=arm64 \
  CODEMAP_ARCHIVE_EXT=tar.gz CODEMAP_BIN_DIR="$cache_bin_dir" GITHUB_PATH="${WORK}/path-file2" \
  PATH="/usr/bin:/bin" bash "${SCRIPTS}/install-codemap.sh" >/dev/null 2>&1
cache_hit_exit=$?
if [[ "$cache_hit_exit" -eq 0 ]]; then
  ok "install-codemap.sh skips download entirely on an actions/cache hit (no curl on PATH, still succeeds)"
else
  bad "install-codemap.sh should short-circuit on a cache hit without needing curl"
fi

# Real end-to-end happy path against the live release, network permitting.
if command -v curl >/dev/null 2>&1 && curl -sSL --max-time 5 -o /dev/null -w '' "https://github.com" 2>/dev/null; then
  real_bin_dir="${WORK}/real-bin"
  CODEMAP_TAG=v0.40.0 CODEMAP_VERSION_NUM=0.40.0 CODEMAP_ARCHIVE_OS=darwin CODEMAP_ARCHIVE_ARCH=arm64 \
    CODEMAP_ARCHIVE_EXT=tar.gz CODEMAP_BIN_DIR="$real_bin_dir" GITHUB_PATH="${WORK}/path-file3" \
    bash "${SCRIPTS}/install-codemap.sh" >/dev/null 2>&1
  real_install_exit=$?
  if [[ "$real_install_exit" -eq 0 && -x "${real_bin_dir}/codemap" ]]; then
    ok "install-codemap.sh downloads + verifies + extracts a real v0.40.0 darwin/arm64 release"
  else
    bad "real end-to-end install of v0.40.0 darwin/arm64 failed"
  fi
else
  skipped "real end-to-end install (no network access in this environment)"
fi

echo
echo "== results: ${pass} passed, ${fail} failed, ${skip} skipped =="
if [[ "$fail" -gt 0 ]]; then
  exit 1
fi
exit 0
