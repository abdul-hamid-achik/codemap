#!/usr/bin/env bash
# GitLab mirror of scripts/post-comment.js: create-or-update ONE sticky note
# on a merge request, keyed by the same hidden HTML marker, via curl + the
# Notes API (https://docs.gitlab.com/api/notes/) instead of Octokit.
#
# VERIFIED (WebSearch, 2026-07): $CI_JOB_TOKEN can only GET
# /projects/:id/merge_requests/:iid/notes in current GitLab — creating or
# updating a note needs a token with write access (a project/personal access
# token with `api` scope). Set CODEMAP_GITLAB_TOKEN as a masked CI/CD
# variable; this script refuses to silently downgrade to a job token that
# would just 403 on the write call.
#
# Predefined GitLab variables used (GitLab's analogues of GitHub's
# github.event.pull_request.* / github.token):
#   CI_API_V4_URL, CI_PROJECT_ID, CI_MERGE_REQUEST_IID
set -euo pipefail

REVIEW_COMMENT_PATH="${1:-${REVIEW_COMMENT_PATH:-}}"
if [[ -z "$REVIEW_COMMENT_PATH" || ! -f "$REVIEW_COMMENT_PATH" ]]; then
  echo "usage: post-comment-gitlab.sh <rendered-comment.md>  (or export REVIEW_COMMENT_PATH)" >&2
  exit 1
fi

: "${CI_API_V4_URL:?post-comment-gitlab.sh must run inside a GitLab CI job}"
: "${CI_PROJECT_ID:?}"
: "${CI_MERGE_REQUEST_IID:?this job must be restricted to merge_request_event pipelines, see gitlab/codemap-review.yml rules}"
: "${CODEMAP_GITLAB_TOKEN:?set CODEMAP_GITLAB_TOKEN to a masked project or personal access token with the api scope; the CI_JOB_TOKEN predefined variable cannot create or update notes}"

MARKER='<!-- codemap-review-action:marker -->'
notes_url="${CI_API_V4_URL}/projects/${CI_PROJECT_ID}/merge_requests/${CI_MERGE_REQUEST_IID}/notes"

body_json="$(jq -Rs '{body: .}' "$REVIEW_COMMENT_PATH")"

existing_id="$(
  curl -sSL --fail -H "PRIVATE-TOKEN: ${CODEMAP_GITLAB_TOKEN}" \
    "${notes_url}?per_page=100" \
  | jq -r --arg marker "$MARKER" '[.[] | select(.body != null and (.body | contains($marker)))][0].id // empty'
)"

if [[ -n "$existing_id" ]]; then
  curl -sSL --fail -X PUT \
    -H "PRIVATE-TOKEN: ${CODEMAP_GITLAB_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "$body_json" \
    "${notes_url}/${existing_id}" > /dev/null
  echo "codemap-action: updated sticky note ${existing_id} on MR !${CI_MERGE_REQUEST_IID}"
else
  created_id="$(
    curl -sSL --fail -X POST \
      -H "PRIVATE-TOKEN: ${CODEMAP_GITLAB_TOKEN}" \
      -H "Content-Type: application/json" \
      -d "$body_json" \
      "$notes_url" \
    | jq -r '.id'
  )"
  echo "codemap-action: created sticky note ${created_id} on MR !${CI_MERGE_REQUEST_IID}"
fi
