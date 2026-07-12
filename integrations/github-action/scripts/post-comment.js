// Create-or-update the ONE sticky codemap review comment on a pull request.
//
// Used from actions/github-script@v9 (action.yml), which invokes this file's
// default export with { github, context, core } already bound to the
// workflow's octokit client / event context / logger — see
// https://github.com/actions/github-script (verified current usage: v9,
// `require('${{ github.action_path }}/scripts/post-comment.js')` from an
// inline `script:` block, calling the exported function with those three).
//
// Kept as the ONLY JavaScript in this repo on purpose: every other script is
// bash + jq so it ports verbatim to the GitLab CI mirror (gitlab/). Only the
// actual comment-posting call is platform-specific — see
// scripts/post-comment-gitlab.sh for the curl + Notes API equivalent.
const fs = require('fs');

const MARKER = '<!-- codemap-review-action:marker -->';

module.exports = async ({ github, context, core }) => {
  const commentPath = process.env.REVIEW_COMMENT_PATH;
  if (!commentPath) {
    core.setFailed('REVIEW_COMMENT_PATH is not set — render-comment.sh must run before post-comment.js and export its comment-path output into this step\'s env.');
    return;
  }

  let body;
  try {
    body = fs.readFileSync(commentPath, 'utf8');
  } catch (err) {
    core.setFailed(`could not read rendered comment at ${commentPath}: ${err.message}`);
    return;
  }

  const pr = context.payload.pull_request;
  if (!pr) {
    core.warning('codemap-action: no pull_request in this event payload — skipping the sticky PR comment (this step only posts on pull_request-triggered runs; set skip-comment: true to silence this warning on other triggers).');
    return;
  }

  const { owner, repo } = context.repo;
  const issue_number = pr.number;

  const comments = await github.paginate(github.rest.issues.listComments, {
    owner,
    repo,
    issue_number,
    per_page: 100,
  });

  const existing = comments.find((c) => typeof c.body === 'string' && c.body.includes(MARKER));

  if (existing) {
    await github.rest.issues.updateComment({ owner, repo, comment_id: existing.id, body });
    core.info(`codemap-action: updated sticky comment #${existing.id} on PR #${issue_number}`);
  } else {
    const created = await github.rest.issues.createComment({ owner, repo, issue_number, body });
    core.info(`codemap-action: created sticky comment #${created.data.id} on PR #${issue_number}`);
  }
};
