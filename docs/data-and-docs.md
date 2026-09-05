---
description: Trace SQL queries, YAML dependencies, Markdown links, and HTML assets alongside your code.
---

# Connect data, configuration, and documentation

Index SQL, YAML, and Markdown with the same command you use for code. Inspect their definitions with `symbols` and `source`, find inbound relationships with `dependencies`, and follow specific edges with `traverse`. These formats work offline without a language server or embeddings.

```bash
codemap index --no-embed
codemap docs formats
```

After upgrading, run `index` in each project to discover the new file types. Ordinary incremental indexing adds previously unsupported files. Existing SQL/YAML/Markdown content now contributes definitions to search; adjust your [excludes](/configuration) if you want to omit documentation or fixtures.

## SQL and sqlc

The SQL backend extracts table and view declarations, sqlc-named queries, and anonymous SELECT/INSERT/UPDATE/DELETE/WITH/ALTER/DROP statements. It records lexical `reads` and `writes` edges to indexed tables and views. It handles quoted identifiers, comments, string literals, and PostgreSQL dollar-quoted bodies without interpreting their contents as executable queries.

For example, create `schema.sql`:

```sql
CREATE TABLE sessions (id integer PRIMARY KEY, title text);
```

Create `queries/get.sql`:

```sql
-- name: GetSession :one
SELECT * FROM sessions WHERE id = ?;
```

After indexing, inspect the query and its table dependency:

```bash
codemap symbols queries/get.sql --json
codemap traverse --at queries/get.sql:1 --edge-types reads --json
codemap dependencies schema.sql --json
```

The traversal reports a `reads` candidate to `sessions`. The dependency report includes `queries/get.sql` as an inbound dependent of `schema.sql`.

When a version-2 `sqlc.yaml` or `sqlc.yml` declares the SQL inputs and Go output directory, Codemap can connect generated Go functions/methods to their named queries with `depends_on`. It requires the sqlc generated-file header, a matching query annotation, and a unique query in the configured input scope. Generated sqlc query files contribute structure but are not embedded again by Codemap; other generated source remains excluded.

```text
Go caller → generated Go method → named SQL query → table
             calls               depends_on        reads/writes
```

**SQL limits:** this is conservative lexical analysis, not database execution or a complete SQL grammar. It does not evaluate migration order, report the live schema, resolve dynamic SQL, derive column lineage, or understand ORM query builders. Relation names may have several candidate definitions across migration files or schemas. CTE aliases are excluded from table matching. Comma-separated table lists and complex dialect-specific constructs can remain incomplete. Use the source and confidence fields before deciding what to change.

## YAML configuration

Every scalar mapping key has a source range and a stable key-path identity. The extractor preserves nested paths, sequences, separate YAML documents, and quoted keys. It does not expand aliases or evaluate templates and rejects duplicate mapping keys.

Codemap also records these explicit `depends_on` relationships:

| Format | Recognized dependency |
|---|---|
| `Taskfile.yml` / `.yaml`, including `.dist` variants | `tasks.<name>.deps` strings and `{task: name}` entries |
| Compose filenames containing `compose` | `services.<name>.depends_on` list or mapping entries |
| `.github/workflows/*.yml` / `.yaml` | `jobs.<name>.needs` strings or lists |

Inspect a Taskfile with:

```bash
codemap symbols Taskfile.yml --json
```

Use a returned key's line to follow its dependencies:

```bash
codemap traverse --at Taskfile.yml:6 --edge-types depends_on --json
```

Replace line `6` with the actual `deps` key line. Use a durable selector in MCP to keep the request tied to that key after line shifts. Key identities use JSON Pointer escaping, so a literal `a/b` key cannot collide with nested `a` and `b` mappings. The first YAML document has an unnumbered identity; later documents include their document number.

The indexer, watcher, and freshness checks include `.github`; other hidden directories stay excluded. Configured excludes still apply. YAML values remain source content and can be returned by source/export or embedded when you enable embeddings. Exclude operator configuration files you do not want indexed.

**YAML limits:** key extraction does not establish which application code consumes a value. Task templates, included Taskfile namespaces, arbitrary shell commands, Compose variable expansion, Pulumi execution, and Kubernetes runtime relationships are not evaluated.

## Markdown documentation

Markdown headings become `section` definitions with source ranges. Local links create `documents` edges to indexed files or known Markdown heading anchors. Reference-style links work through the CommonMark parser. Documents without headings still get one searchable section.

Add this link to `README.md`:

```markdown
# Sessions

Read the [session query](queries/get.sql).
```

Then inspect the documentation alongside the query:

```bash
codemap index --no-embed
codemap symbols README.md --json
codemap dependencies queries/get.sql --json
```

The dependency report includes the section in `README.md` that links to the query. An initially missing destination can acquire a relationship when you add it and index again.

**Markdown limits:** fenced examples never produce executable symbols or calls. External URLs are not fetched. Bare symbol mentions do not imply a dependency. Links only resolve to indexed targets; arbitrary assets, site-specific routing, MDX execution, and custom heading-anchor conventions are not inferred.

## HTML and stylesheets

HTML indexing connects static `class`/`id` attributes to stylesheet selector definitions. It also extracts local stylesheet/script imports and CSS selectors inside `<style>` blocks, preserving the original HTML line numbers. The tokenizer ignores markup-looking strings inside script bodies and comments.

CSS, SCSS, Sass, and Less retain selector and stylesheet-import support. Cross-file selector matches are candidates: class names can repeat in unrelated stylesheets. CSS Modules member access, CSS-in-JS, cascade/specificity, dynamic classes, and framework templates are outside this backend's coverage.

## Choose the right query

| Question | CLI | MCP |
|---|---|---|
| What definitions are in this file? | `symbols <file>` | `codemap_symbols` |
| What does this definition contain? | `source --at <file>:<line>` | `codemap_source` with `selector` |
| What indexed files point here? | `dependencies <file>` | `codemap_dependencies` |
| What relationships leave this definition? | `traverse --at <file>:<line>` | `codemap_traverse` with `selector`, full profile |
| What are this format's limits? | `docs formats` | `codemap_docs` with `topic: "formats"` |

SQL, YAML, and Markdown definitions return `call_graph: "none"`. Running `--precise` does not create a call graph for them. Function-oriented impact and risk reports explain this limitation; use dependencies and typed traversal for their structural relationships. A partial dependency report cannot prove that deleting a file is safe.

Vecgrep can consume these definitions through the existing structural export. Keep Vecgrep as the semantic owner if your ecosystem already delegates retrieval to it; no second embedding pipeline is required. See [ecosystem setup](/ecosystem).
