---
description: Keep codemap indexes aligned with Git branches and share or restore content-addressed index snapshots.
---

# Branches & caching

codemap's index (graph + vectors) is a snapshot of one working tree. Two features
keep that snapshot in step with your **git branch** and make (re)indexing cheap when
you return to a tree you've indexed before — both powered by the sibling tool
[**fcheap**](https://github.com/abdul-hamid-achik/fcheap), a local content-addressed
stash vault. Neither is a hard dependency: every path degrades gracefully to a normal
index when `fcheap` isn't on your `$PATH`.

## Branch-aware index switching

Switching branches with `git checkout` changes your files, but the code graph still
describes the *old* branch — until you reindex. Branch switching closes that gap: it
**snapshots** the branch you're leaving into fcheap and **restores** the target
branch's snapshot (or reindexes when none exists or it's stale), so the graph follows
the working tree with no full reindex/re-embed.

```bash
codemap branch-status                    # read-only: repo hash, branch, HEAD sha, index key
codemap branch-switch                    # snapshot current branch, restore/reindex the target
codemap branch-switch --to feature-x     # explicit target (defaults to the current git branch)
codemap branch-snapshot                  # stash the current branch's index without switching
```

`branch-switch` keys each snapshot on the branch's tip sha, so a restore only lands
when the snapshot's base is an ancestor of HEAD and its **embedding profile** matches
(the provider/model/dimensions/distance metric you're using now). Otherwise it falls back to an
incremental reindex — never a silent mismatch. A non-git directory or detached HEAD
is a clean no-op (the single flat index is left as-is).

### Automatic switching with a git hook

Install a `post-checkout` hook once and the index switches on every `git checkout`
with no further thought:

```bash
codemap branch-switch --install-hook     # writes .git/hooks/post-checkout (idempotent, preserves existing)
```

The hook runs a guarded `codemap branch-switch` pinned to the running binary, so it
works even if codemap isn't on `$PATH` in the hook's environment.

## Index caching (fcheap)

Caching is the *content-addressed* answer to "I already indexed this tree once."
`codemap cache` stashes a finished index keyed by a **tree hash** — a hash of all
indexed `(file_path, file_content_hash)` pairs — so two working trees with identical
code share one cache entry (fcheap dedups the content). Restoring a hit skips
extraction and embedding *entirely*; a reindex that would take seconds becomes
instant.

```bash
codemap cache save                       # stash the current index (keyed by tree hash)
codemap cache restore                    # restore the index matching the current tree
codemap cache list [--rebuild]           # list cached indexes for this repo
codemap cache drop --tree <hash>         # drop one entry (or --all for every entry in this repo)
```

`cache list --rebuild` reconstructs the list straight from fcheap (use it if the local
pointer file is lost); `cache drop` frees disk space when indexes are no longer needed.

### Auto-cache around `codemap index`

You usually don't call `cache save`/`restore` by hand — `codemap index` does it for you
behind the `--cache` flag (on by default):

- **Before a `--reindex`**, it looks for a cache entry matching the current tree hash +
  embedding profile and restores it, skipping the full wipe+extract+embed cycle. With
  `--precise`, it only accepts a cache entry that already has precise edges — otherwise
  the go/types pass the user asked for would be skipped, so it falls through to a real
  reindex.
- **After any successful index**, it saves the freshly-built index to fcheap
  (best-effort: a save failure never fails the index).

```bash
codemap index                            # builds the graph + embeddings, then auto-saves to cache
codemap index --reindex                  # tries cache restore first; rebuilds only on a miss
codemap index --reindex --precise        # restore only if the cache entry already has precise edges
codemap index --cache=false              # disable auto-cache/restore for this run
```

### Portable tarballs (team/CI-shareable, no fcheap required)

`cache save`/`restore` are **same-machine only** — fcheap's stash vault is a local content-
addressed store. `cache export`/`import` package the exact same snapshot (graph + vectors) into
a self-contained `tar.gz`, so a CI job that just built a full (`--precise`) index can hand it to
the next job or runner with no shared store and no re-indexing:

```bash
codemap cache export index.tar.gz          # write the current index to a portable tarball
codemap cache import index.tar.gz          # restore it (registers the project if unindexed)
codemap cache import index.tar.gz --force  # ...even if the working tree hash doesn't match
```

`import` validates, in order, before touching anything: the tarball's own wrapper schema, then
the **embedding profile** (a mismatched local model is always refused — force does not override
this, never mix embedding spaces), then the **working tree hash** against the tarball's recorded
one. A tree-hash mismatch is refused by default — a portable archive promises "this exact tree",
so silently importing a divergent one would answer queries against code that isn't actually
checked out. `--force` downgrades that refusal to a warning, for a deliberate case like seeding a
PR branch's cache from its base branch ahead of an incremental catch-up reindex.

See [codemap in CI](/ci) for a workflow snippet that caches the tarball between CI runs with
`actions/cache`.

## Where the data lives

- **Branch snapshots** are keyed `repo:<repoHash> branch:<branch>` in fcheap, tracked
  in a per-repo pointer file under `$XDG_DATA_HOME/codemap/` (the `branchstate` store).
- **Cache entries** are keyed `repo:<repoHash> tree:<treeHash>` in fcheap, tracked in a
  per-repo pointer file. `cache list --rebuild` rebuilds from fcheap if the pointer is
  gone.
- Both are best-effort and **never fail the index**: a missing `fcheap` binary, an
  unindexed project, or a non-git dir is a clean no-op. Cache commands return an
  actionable note when `fcheap` is unavailable.

## In MCP

The same operations are exposed as tools so an agent can keep the index aligned with
the working tree without shelling out:

| Tool | Description |
|---|---|
| `codemap_branch_status` | Read-only git branch/commit state + the stable repo/branch keys used to key per-branch snapshots |
| `codemap_branch_switch` | Switch the code index to a git branch (snapshots the old, restores/reindexes the new) |
| `codemap_cache_save` | Save the current index to the fcheap stash vault (keyed by tree hash) |
| `codemap_cache_restore` | Restore a matching fcheap cache entry, skipping extraction/embedding |
| `codemap_cache_list` | List cached indexes for a project (stash IDs, tree hashes, dates) |
| `codemap_cache_drop` | Drop a cached index by `stash_id`, or all cached indexes for the project |

See the [CLI reference](/cli#branches-caching) and [MCP tools](/mcp#branches-caching) for the
exact flags and JSON shapes.
