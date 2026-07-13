---
layout: home
hero:
  name: codemap
  text: Your agent greps. codemap knows.
  tagline: A local-first code graph + semantic search your coding agent calls over MCP — who calls this, what breaks, which tests cover it. One pure-Go binary, fully offline, 42 tools.
  image:
    src: /hero-card.svg
    alt: "codemap_impact(\"Store.NodeAtLine\") returning a cited answer in a studio terminal pane"
  actions:
    - theme: brand
      text: Set up your agent
      link: /agents
    - theme: alt
      text: Quick Start
      link: /quick-start
    - theme: alt
      text: GitHub
      link: https://github.com/abdul-hamid-achik/codemap
features:
  - title: One-call context
    details: codemap_context returns a symbol's definition, callers, callees, blast radius, and covering tests — the orientation an agent otherwise burns five file reads on.
  - title: Review after every edit
    details: codemap review maps your diff to changed symbols, their blast radius, and the exact tests to run — with a machine-readable risk band CI can gate on.
  - title: grep that lands on symbols
    details: Exact-text search over the indexed file set, every hit joined to its enclosing symbol with a chainable selector. No more grep-then-re-read.
  - title: Honest by design
    details: Stale flags, a per-file coverage trust map, ambiguity candidates, confidence enums — every answer says how much to trust it.
  - title: Search by meaning
    details: Hybrid vector + BM25 with query-adaptive weighting. Local Ollama embeddings, an authenticated team endpoint, or none at all — it degrades, and says so.
  - title: Your harness, one command
    details: codemap agent setup cursor (or claude-code, codex, gemini, zed… 11 in all) registers the MCP server and teaches the agent when to reach for it.
---

<div class="cm-section">
<p class="cm-kicker">the difference, in one question</p>

## “What breaks if I change `Store.NodeAtLine`?”

<div class="cm-diff">
  <div class="cm-term">
    <div class="cm-term-bar"><span>agent without codemap</span><span class="cm-verdict-bad">guessing</span></div>
    <pre class="cm-term-body"><span class="dim">●</span> Grep(<span class="yellow">"NodeAtLine"</span>)
<span class="dim">●</span> Read(internal/graph/queries.go)
<span class="dim">●</span> Grep(<span class="yellow">"NodeAtLine("</span>)
<span class="dim">●</span> Read(internal/app/service_grep.go)
<span class="dim">●</span> Read(internal/app/symbol_at_batch.go)
<span class="dim">●</span> Read(internal/app/secret_impact.go)
<span class="dim">●</span> Grep(<span class="yellow">"SymbolAt"</span>)
<span class="dim">●</span> Read(internal/app/service_core.go)
<span class="dim">… 16 more greps and reads</span></pre>
    <div class="cm-tally"><span><strong>24 tool calls</strong>, six files pasted into context</span><span>callers still incomplete</span></div>
  </div>
  <div class="cm-term">
    <div class="cm-term-bar"><span>agent with codemap</span><span class="cm-verdict-good">cited</span></div>
    <pre class="cm-term-body"><span class="dim">●</span> <span class="cyan">codemap_impact</span>(<span class="yellow">"Store.NodeAtLine"</span>)
{
  <span class="green">"direct_callers"</span>: 7,
  <span class="green">"blast_radius"</span>: 23 symbols,
  <span class="green">"covering_tests"</span>: [
    <span class="yellow">"TestServiceGrepResolvesHits…"</span>,
    <span class="yellow">"TestSymbolAtBatch"</span>, <span class="dim">+11</span>
  ],
  <span class="green">"call_graph"</span>: <span class="yellow">"resolved"</span>
}</pre>
    <div class="cm-tally"><span><strong>1 tool call</strong>, structured answer</span><span>every entry carries file:line</span></div>
  </div>
</div>

<p class="cm-note">Real shape, real repo — this is codemap answering about its own source. The structure is precomputed once at index time; queries are milliseconds and work offline. In a hermetic 60-session A/B on go-git, modelled as deployed (core profile + playbook), the codemap arm used <strong>44% fewer tool calls</strong>, ran <strong>40% faster</strong>, and cost <strong>13% less</strong> at equal correctness — directional, negative results included — the full four-run story lives in <a href="https://github.com/abdul-hamid-achik/codemap/tree/main/bench">bench/</a>.</p>

</div>

<div class="cm-section">
<p class="cm-kicker">one command per harness</p>

## Wherever your agent lives

```bash
codemap agent setup claude-code   # or any of the others ↓
```

<ul class="cm-chips">
  <li class="on">claude-code</li>
  <li>cursor</li>
  <li>codex</li>
  <li>gemini</li>
  <li>cline</li>
  <li>roo</li>
  <li>zed</li>
  <li>vscode</li>
  <li>opencode</li>
  <li>aider</li>
  <li>agents-md</li>
</ul>

<p class="cm-note">Registers <code>codemap serve</code> in the harness's native MCP config (never clobbering your other servers) and installs a playbook that teaches the agent <em>when</em> to use it. Re-runs are byte-idempotent. Claude Code users can instead install the <a href="/agents">plugin</a>.</p>

</div>

<div class="cm-section">
<p class="cm-kicker">the same answers, on every pull request</p>

## Review as a merge gate

```yaml
jobs:
  review:
    uses: abdul-hamid-achik/codemap/.github/workflows/codemap-review-reusable.yml@main
    with:
      fail-on-untested: 'true'
      fail-on-risk: 'high'
```

<p class="cm-note">One sticky comment per PR: changed symbols, blast radius, untested-hotspot callouts, and a risk band you can gate merges on. GitLab mirror included. No embeddings, no secrets beyond <code>GITHUB_TOKEN</code>.</p>

</div>

<div class="cm-section">
<p class="cm-kicker">install</p>

## Two ways in

<div class="cm-install-grid">
<div class="cm-install-cell">

<p class="cm-install-label">brew</p>

```bash
brew install abdul-hamid-achik/tap/codemap
```

</div>
<div class="cm-install-cell">

<p class="cm-install-label">go</p>

```bash
go install github.com/abdul-hamid-achik/codemap/cmd/codemap@latest
```

</div>
</div>

<p class="cm-note cm-note-center">Then: <code>codemap init && codemap index</code> in any repo. Go, TypeScript, JavaScript, Python, and Vue out of the box — <code>--precise</code> upgrades the call graph to exact resolution.</p>

</div>
