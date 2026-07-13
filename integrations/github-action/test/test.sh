#!/usr/bin/env bash
# Plain-bash test harness for codemap-action's scripts (bats-core isn't
# assumed to be installed; this runs anywhere bash+jq do). Exercises
# render-comment.sh and gate.sh against every fixture in testdata/, gate.sh's
# outputs (risk-level/risk-score/untested-count/changed-symbols-count) and
# ordinal table explicitly, the composite Action's gate-last sequencing,
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
  # gate(fixture, fail_on_untested, fail_on_risk) -> sets $GATE_EXIT, $GATE_OUT
  local fixture="$1" fu="$2" fr="$3"
  local out="${WORK}/gate-output-$$-${RANDOM}"
  : > "$out"
  GITHUB_OUTPUT="$out" FAIL_ON_UNTESTED="$fu" FAIL_ON_RISK="$fr" \
    bash "${SCRIPTS}/gate.sh" "${TESTDATA}/${fixture}.json" >/dev/null 2>&1
  GATE_EXIT=$?
  GATE_OUT="$(cat "$out")"
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
assert_contains "$body" '🟢 `low`' "golden-contract: renders risk low"
assert_contains "$body" 'call graph resolution is `name`' "golden-contract: call_graph=name surfaces as a caveat"
assert_contains "$body" 'the index is **stale**' "golden-contract: stale:true surfaces as a caveat"
assert_contains "$body" 'every changed symbol has at least one covering test' "golden-contract: untested_symbols empty renders the all-clear line"

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

gate risk-absent true high
assert_eq "$GATE_EXIT" "0" "absent risk object does NOT trip fail-on-risk"
assert_eq "$GATE_EXIT" "0" "absent risk object does NOT trip fail-on-untested (0 untested)"

gate hotspots-present false low
assert_eq "$GATE_EXIT" "1" "risk.level 'medium' DOES trip fail-on-risk low"

gate hotspots-present false high
assert_eq "$GATE_EXIT" "0" "risk.level 'medium' does NOT trip fail-on-risk high"

gate golden-contract false low
assert_eq "$GATE_EXIT" "1" "risk.level 'low' trips fail-on-risk low (>= is inclusive of the threshold itself)"

gate golden-contract false high
assert_eq "$GATE_EXIT" "0" "risk.level 'low' does NOT trip fail-on-risk high"

gate real-since-untested-high-risk true ""
assert_eq "$GATE_EXIT" "1" "fail-on-untested trips when untested_symbols is non-empty"

gate real-since-untested-high-risk false ""
assert_eq "$GATE_EXIT" "0" "fail-on-untested does not trip when the input is false, even with untested symbols present"

gate unknown-schema-version true high
assert_eq "$GATE_EXIT" "0" "unrecognized schema_version fails soft (exit 0), does not crash the gate"

gate golden-contract false bogus
assert_eq "$GATE_EXIT" "0" "an unrecognized fail-on-risk value is ignored (ordinal -1), not treated as a match"

echo
echo "== gate.sh: outputs =="
gate real-since-untested-high-risk true ""
assert_contains "$GATE_OUT" "risk-level=high" "gate.sh sets the risk-level output even when the gate trips"
assert_contains "$GATE_OUT" "untested-count=1" "gate.sh sets the untested-count output even when the gate trips"
assert_contains "$GATE_OUT" "risk-score=0.9" "gate.sh sets the risk-score output (0.9 from the fixture's risk.score) even when the gate trips"
assert_contains "$GATE_OUT" "changed-symbols-count=1" "gate.sh sets the changed-symbols-count output even when the gate trips"

gate risk-absent false ""
assert_contains "$GATE_OUT" "risk-score=0" "gate.sh: risk-score is '0' when the diff has no risk object at all"
assert_contains "$GATE_OUT" "changed-symbols-count=1" "gate.sh: changed-symbols-count is still populated when risk is absent"

gate unknown-schema-version false ""
assert_contains "$GATE_OUT" "risk-score=0" "gate.sh: unrecognized schema_version still sets risk-score (fail-soft default '0')"
assert_contains "$GATE_OUT" "changed-symbols-count=0" "gate.sh: unrecognized schema_version still sets changed-symbols-count (fail-soft default '0')"

echo
echo "== action gate sequencing: report and outputs exist before failure =="
review_invocation="$(grep -E '^codemap review --since ' "${SCRIPTS}/run-review.sh")"
assert_not_contains "$review_invocation" "--fail-on-" "run-review.sh deliberately omits native gate flags so a success JSON is always available to render"

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
assert_contains "$WS_SUMMARY" '🟢 `low`' "write-summary.sh: the job summary carries the same risk rendering as the PR comment (single rendering path)"

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
