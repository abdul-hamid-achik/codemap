#!/usr/bin/env bash
# Regenerate benchmark ground truth from the pinned fixture, INDEPENDENTLY OF
# CODEMAP (see ./README). The oracle is the Go type checker (go/types via
# x/tools) — the standalone program under gen/ — NOT codemap and NOT its graph.
#
# Run only when fixture.lock's SHA changes or targets.json changes; the *.json
# output is the committed contract.
set -euo pipefail

# repo root is three levels up from bench/tasks/truth/
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$root"

if [[ ! -d bench/fixtures/repo/.git ]]; then
  echo "gen: fixture missing — running fetch.sh" >&2
  bench/fixtures/fetch.sh
fi

echo "gen: deriving ground truth via go/types (independent of codemap)"
go run ./bench/tasks/truth/gen \
  -fixture bench/fixtures/repo \
  -targets bench/tasks/truth/targets.json \
  -out bench/tasks/truth

echo "gen: done — review the transitive sets (04/05/07) before committing"
