#!/usr/bin/env bash
# Fetch the pinned benchmark fixture by SHA into bench/fixtures/repo/ (gitignored).
# Idempotent: if the repo is already at the pinned SHA, it does nothing.
#
# The fixture is intentionally NOT vendored or submoduled — only fixture.lock
# (the SHA pin) and the derived ground truth are committed. This pins the exact
# bytes without submodule friction; swap the fixture by editing fixture.lock.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here"

# Parse fixture.lock (repo= / sha=).
repo="$(grep -E '^repo=' fixture.lock | head -1 | cut -d= -f2-)"
sha="$(grep -E '^sha=' fixture.lock | head -1 | cut -d= -f2-)"
if [[ -z "$repo" || -z "$sha" ]]; then
  echo "fetch: fixture.lock missing repo= or sha=" >&2
  exit 1
fi
dest="$here/repo"

if [[ -d "$dest/.git" ]]; then
  cur="$(git -C "$dest" rev-parse HEAD 2>/dev/null || echo none)"
  if [[ "$cur" == "$sha" ]]; then
    echo "fetch: fixture already at $sha"
    exit 0
  fi
fi

if [[ ! -d "$dest/.git" ]]; then
  echo "fetch: cloning $repo (blob:none) into repo/"
  git clone --quiet --filter=blob:none "$repo" "$dest"
fi

echo "fetch: fetching + checking out $sha"
git -C "$dest" fetch --quiet origin "$sha" 2>/dev/null || git -C "$dest" fetch --quiet --all
git -C "$dest" checkout --quiet "$sha"
echo "fetch: fixture at $(git -C "$dest" rev-parse HEAD)"
