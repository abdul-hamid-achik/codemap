#!/usr/bin/env bash
# Appends the already-rendered review Markdown (render-comment.sh's output) to
# the GitHub Actions job summary ($GITHUB_STEP_SUMMARY), so the report is
# visible on push events, on forks where the sticky PR comment step lacks
# write permission, and on any run where `skip-comment: true` was set — all
# cases where the PR-comment step never runs or can't write. Deliberately
# reuses render-comment.sh's Markdown file instead of re-deriving it from the
# review JSON, so there is exactly one rendering code path.
#
# Called by: action.yml, step "Write job summary" (runs with `if: always()`,
# after both render-comment.sh and the PR-comment step, GitHub Action only —
# GitLab CI has no $GITHUB_STEP_SUMMARY equivalent, so gitlab/codemap-review.yml
# does not call this script).
#
# Usage: COMMENT_PATH=<path> ./write-summary.sh
#        ./write-summary.sh <path-to-rendered-comment.md>
set -euo pipefail

COMMENT_PATH="${1:-${COMMENT_PATH:-}}"

if [[ -z "$COMMENT_PATH" || ! -f "$COMMENT_PATH" ]]; then
  echo "codemap-action: no rendered comment found at '${COMMENT_PATH:-<empty>}' — skipping job summary (render-comment.sh should run first)"
  exit 0
fi

if [[ -z "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo "codemap-action: \$GITHUB_STEP_SUMMARY is not set (not running in a GitHub Actions job, or the runner doesn't support it) — skipping job summary"
  exit 0
fi

{
  cat "$COMMENT_PATH"
  echo
} >> "$GITHUB_STEP_SUMMARY"

echo "codemap-action: appended the rendered review to \$GITHUB_STEP_SUMMARY"
