---
description: Install codemap, build an offline structural index, and inspect your first dependencies.
---

# Quick start

Build a map of one repository, inspect a definition, and follow its relationships.
Start with offline structure; add semantic retrieval when you need search by intent.

## 1. Install

```bash
brew install abdul-hamid-achik/tap/codemap
# Or build from source:
go install github.com/abdul-hamid-achik/codemap/cmd/codemap@latest
```

Go, Ruby, Lua, GDScript, SQL, YAML, Markdown, HTML, and stylesheets have built-in
backends. TypeScript, JavaScript, and Vue need `typescript-language-server`;
Python needs `pyright-langserver`. Run `codemap doctor` and consult the
[language matrix](/languages) for exact capabilities and missing tools.

## 2. Index one repository

```bash
cd ~/projects/myapp
codemap init
codemap index --no-embed
codemap status --json
```

Indexing reports the files processed, graph size, skipped languages, and errors.
The counts depend on your repository. Run `index` again after changes; unchanged
files are skipped and dependencies are reconciled against the current graph.

For exact calls in Go, TypeScript, JavaScript, or Python:

```bash
codemap index --no-embed --precise
```

Check reported coverage. A failed or unavailable precise pass does not make the
whole graph exact. SQL, YAML, and Markdown expose other relationship types;
`--precise` does not add function calls to them.

## 3. Inspect a definition

Start with a file you recognize:

```bash
codemap symbols path/to/file.go --json
```

Replace the path with a file in your repository. Pick a returned definition and
use its file and line in a follow-up:

```bash
codemap context --at path/to/file.go:42 --json
```

The context includes source, callers, callees, references, covering tests, and
call impact where those domains are available. JSON results expose confidence
and a durable selector: `file`, `start_line`, `fqn`, and `kind`.

For data and documentation, use the [format-specific walkthrough](/data-and-docs):

```bash
codemap symbols README.md --json
codemap dependencies schema.sql --json
codemap docs formats
```

Replace `schema.sql` with an indexed file. `dependencies` reports inbound
evidence; `traverse` follows selected edge types. Missing evidence in a partial
graph does not prove a file is safe to delete.

## 4. Add semantic retrieval

If Vecgrep already owns semantic retrieval in your environment, configure
`semantic.backend: vecgrep` and use its resolved embedding provider. Vecgrep can
use OpenAI when configured; its global defaults and project overrides determine
the model. See [ecosystem setup](/ecosystem).

For Codemap's own local embeddings, start Ollama and pull `nomic-embed-text`, then
run `codemap index` with embeddings enabled. This path is optional. See
[configuration](/configuration) for endpoints, models, and remote-source handling.

```bash
codemap semantic "where are sessions persisted?" --json
```

## Connect your agent

`codemap agent setup <harness>` registers the MCP server and installs the agent
playbook. See [agent setup](/agents#one-command-setup), [CLI reference](/cli),
and [MCP reference](/mcp). For diff checks in CI, use the [GitHub Action](/ci).
