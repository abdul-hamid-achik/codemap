<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'

type CopyState = 'idle' | 'copied' | 'error'

const copyState = ref<CopyState>('idle')
let resetTimer: ReturnType<typeof setTimeout> | undefined

async function copyInstall() {
  if (resetTimer) clearTimeout(resetTimer)

  try {
    await navigator.clipboard.writeText('brew install abdul-hamid-achik/tap/codemap')
    copyState.value = 'copied'
  } catch {
    copyState.value = 'error'
  }

  resetTimer = setTimeout(() => {
    copyState.value = 'idle'
  }, 2200)
}

onBeforeUnmount(() => {
  if (resetTimer) clearTimeout(resetTimer)
})
</script>

<template>
  <main class="cm-home">
    <section class="cm-home-hero" aria-labelledby="cm-home-title">
      <div class="cm-home-grid" aria-hidden="true"></div>
      <div class="cm-home-shell cm-home-hero-layout">
        <div class="cm-home-hero-copy">
          <p class="cm-home-eyebrow"><span></span> Local-first code intelligence</p>
          <h1 id="cm-home-title">Your agent greps.<br /><span>codemap knows.</span></h1>
          <p class="cm-home-lede">
            Give coding agents a cited map of definitions, callers, blast radius, and tests—before
            they change the code. One local binary, exposed over MCP and CLI.
          </p>

          <div class="cm-home-actions">
            <a class="cm-home-button cm-home-button-primary" href="/agents">
              Set up your agent
              <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M4 10h11M11 6l4 4-4 4" /></svg>
            </a>
            <a class="cm-home-button cm-home-button-secondary" href="/quick-start">Read the quick start</a>
            <a
              class="cm-home-icon-link"
              href="https://github.com/abdul-hamid-achik/codemap"
              target="_blank"
              rel="noreferrer"
              aria-label="View codemap on GitHub"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="M12 2.6a9.6 9.6 0 0 0-3 18.7c.5.1.7-.2.7-.5v-1.9c-2.8.6-3.4-1.2-3.4-1.2-.5-1.2-1.1-1.5-1.1-1.5-.9-.6.1-.6.1-.6 1 0 1.6 1.1 1.6 1.1.9 1.6 2.4 1.1 3 .8.1-.7.4-1.1.7-1.4-2.3-.3-4.6-1.1-4.6-4.8 0-1.1.4-1.9 1-2.6-.1-.3-.4-1.3.1-2.6 0 0 .8-.3 2.7 1a9.3 9.3 0 0 1 4.9 0c1.8-1.3 2.7-1 2.7-1 .5 1.3.2 2.3.1 2.6.6.7 1 1.5 1 2.6 0 3.7-2.3 4.5-4.6 4.8.4.3.7.9.7 1.8v2.7c0 .4.2.6.7.5A9.6 9.6 0 0 0 12 2.6Z" />
              </svg>
            </a>
          </div>

          <div class="cm-home-install">
            <span aria-hidden="true">$</span>
            <code>brew install abdul-hamid-achik/tap/codemap</code>
            <button type="button" @click="copyInstall" aria-label="Copy Homebrew install command">
              <svg v-if="copyState !== 'copied'" viewBox="0 0 20 20" aria-hidden="true">
                <rect x="6.5" y="6.5" width="9" height="9" rx="1.5" />
                <path d="M13.5 6.5v-2A1.5 1.5 0 0 0 12 3H4.5A1.5 1.5 0 0 0 3 4.5V12A1.5 1.5 0 0 0 4.5 13.5h2" />
              </svg>
              <svg v-else viewBox="0 0 20 20" aria-hidden="true"><path d="m4 10 3.5 3.5L16 5" /></svg>
            </button>
          </div>
          <p class="cm-home-copy-state" aria-live="polite">
            <span v-if="copyState === 'copied'">Copied to clipboard.</span>
            <span v-else-if="copyState === 'error'">Copy failed. Select the command manually.</span>
          </p>

          <ul class="cm-home-facts" aria-label="codemap product facts">
            <li>Pure-Go binary</li>
            <li>Stored-graph queries work offline</li>
            <li>7 indexed languages</li>
            <li>42 MCP tools in full</li>
          </ul>
        </div>

        <div class="cm-home-intel" aria-label="Example codemap impact result">
          <div class="cm-home-panel-bar">
            <div aria-hidden="true"><span></span><span></span><span></span></div>
            <span>structural evidence</span>
            <strong><i></i> cited</strong>
          </div>
          <div class="cm-home-query"><span aria-hidden="true">›</span><code>codemap_impact(<b>"Store.NodeAtLine"</b>)</code></div>

          <div class="cm-home-graph">
            <svg viewBox="0 0 560 230" preserveAspectRatio="none" aria-hidden="true">
              <path d="M280 115 92 54M280 115 468 54M280 115 92 180M280 115 468 180" />
              <circle cx="280" cy="115" r="3" /><circle cx="92" cy="54" r="3" />
              <circle cx="468" cy="54" r="3" /><circle cx="92" cy="180" r="3" /><circle cx="468" cy="180" r="3" />
            </svg>
            <div class="cm-home-node cm-home-node-callers"><span>callers</span><strong>7 direct</strong></div>
            <div class="cm-home-node cm-home-node-callees"><span>callees</span><strong>4 edges</strong></div>
            <div class="cm-home-node cm-home-node-tests"><span>verification</span><strong>13 tests</strong></div>
            <div class="cm-home-node cm-home-node-blast"><span>blast radius</span><strong>23 symbols</strong></div>
            <div class="cm-home-node cm-home-node-center">
              <span>exact definition</span><strong>Store.NodeAtLine</strong><small>internal/graph/queries.go</small>
            </div>
          </div>

          <div class="cm-home-answer">
            <div><span>call graph</span><strong><i></i> resolved</strong></div>
            <div><span>freshness</span><strong><i></i> current</strong></div>
            <div><span>selector</span><strong>durable</strong></div>
          </div>
          <div class="cm-home-panel-footer"><span>1 call</span><span>every result carries file:line</span></div>
        </div>
      </div>
    </section>

    <section class="cm-home-benchmark" aria-labelledby="cm-home-benchmark-title">
      <div class="cm-home-shell cm-home-benchmark-layout">
        <div>
          <p class="cm-home-eyebrow">Measured on a real repository</p>
          <h2 id="cm-home-benchmark-title">Less searching. More verified work.</h2>
          <p>A hermetic 60-session A/B on go-git, modelled with the shipped core profile and agent playbook. Directional results, negative runs included.</p>
          <a href="https://github.com/abdul-hamid-achik/codemap/tree/main/bench">Read the benchmark methodology →</a>
        </div>
        <dl class="cm-home-metrics">
          <div><dt>44%</dt><dd>fewer tool calls</dd></div>
          <div><dt>40%</dt><dd>faster completion</dd></div>
          <div><dt>13%</dt><dd>lower modelled cost</dd></div>
          <div><dt>60</dt><dd>isolated sessions</dd></div>
        </dl>
      </div>
    </section>

    <section class="cm-home-section" aria-labelledby="cm-home-workflow-title">
      <div class="cm-home-shell">
        <div class="cm-home-heading">
          <div><p class="cm-home-eyebrow">One graph, three surfaces</p><h2 id="cm-home-workflow-title">Ask once. Follow exact evidence.</h2></div>
          <p>codemap joins semantic intent to structural relationships, then preserves the evidence an agent needs to act safely.</p>
        </div>
        <ol class="cm-home-pipeline">
          <li>
            <span class="cm-home-step">01</span>
            <div class="cm-home-step-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><circle cx="10.5" cy="10.5" r="5.5" /><path d="m15 15 5 5" /></svg></div>
            <h3>Find the intent</h3><p>Search by name, exact text, or meaning. Every hit lands on a real symbol.</p><code>semantic "jwt validation"</code>
          </li>
          <li>
            <span class="cm-home-step">02</span>
            <div class="cm-home-step-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><circle cx="6" cy="6" r="2.5" /><circle cx="18" cy="7" r="2.5" /><circle cx="12" cy="18" r="2.5" /><path d="m8.4 6.2 7.1.5M7.4 8.1l3.5 7.7m5.8-6.7-3.5 6.8" /></svg></div>
            <h3>Expand the structure</h3><p>Join definitions, calls, callback references, imports, and annotations.</p><code>context authenticateUser</code>
          </li>
          <li>
            <span class="cm-home-step">03</span>
            <div class="cm-home-step-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M12 3 5 6v5c0 4.7 2.8 8.2 7 10 4.2-1.8 7-5.3 7-10V6l-7-3Z" /><path d="m8.5 12 2.2 2.2 4.8-5" /></svg></div>
            <h3>Verify the change</h3><p>Return blast radius, covering tests, and confidence before code is changed.</p><code>review --fail-on-risk high</code>
          </li>
        </ol>
      </div>
    </section>

    <section class="cm-home-section" aria-labelledby="cm-home-capabilities-title">
      <div class="cm-home-shell">
        <div class="cm-home-heading">
          <div><p class="cm-home-eyebrow">Built for agent loops</p><h2 id="cm-home-capabilities-title">A code map that earns trust.</h2></div>
          <p>Fast enough for every edit, explicit enough for automation, and useful even when embeddings or language servers are unavailable.</p>
        </div>

        <div class="cm-home-bento">
          <article class="cm-home-card cm-home-card-context">
            <div class="cm-home-card-label"><span>01 · context</span><span>one call</span></div>
            <h3>Everything around a symbol, already joined.</h3>
            <p>Definition, source, callers, callees, value references, blast radius, and covering tests arrive as one bounded response.</p>
            <div class="cm-home-mini-terminal">
              <div><span>›</span> codemap_context(<b>"Authorize"</b>)</div>
              <dl><div><dt>definition</dt><dd>auth/service.go:84</dd></div><div><dt>callers</dt><dd>6 exact edges</dd></div><div><dt>covering tests</dt><dd>8 selected</dd></div></dl>
            </div>
          </article>

          <article class="cm-home-card cm-home-card-trust">
            <div class="cm-home-card-label"><span>02 · confidence</span><span>machine-readable</span></div>
            <h3>Honest when the graph is incomplete.</h3>
            <p>No vague “best effort.” Call-graph and impact reports carry explicit confidence signals.</p>
            <ul class="cm-home-confidence">
              <li><i class="resolved"></i><span>resolved</span><strong>exact coverage</strong></li>
              <li><i class="name"></i><span>name</span><strong>candidate edges</strong></li>
              <li><i class="unresolved"></i><span>unresolved</span><strong>reindex action</strong></li>
            </ul>
          </article>

          <article class="cm-home-card">
            <div class="cm-home-card-label"><span>03 · review</span><span>diff aware</span></div>
            <h3>Turn every edit into a test plan.</h3>
            <p>Map the current diff to affected code, untested hotspots, and one risk band.</p>
            <div class="cm-home-risk"><div><span>aggregate risk</span><strong>medium · 0.54</strong></div><div><span></span></div><small>12 affected symbols · 5 covering tests</small></div>
          </article>

          <article class="cm-home-card">
            <div class="cm-home-card-label"><span>04 · retrieval</span><span>hybrid</span></div>
            <h3>Search by meaning. Land on structure.</h3>
            <p>Vector + BM25 retrieval hands durable selectors to the graph—not loose snippets.</p>
            <div class="cm-home-hit"><span>0.92</span><div><strong>auth.TokenValidator.Validate</strong><small>internal/auth/token.go:37</small></div></div>
            <div class="cm-home-hit muted"><span>0.87</span><div><strong>middleware.requireJWT</strong><small>api/middleware/auth.go:52</small></div></div>
          </article>

          <article class="cm-home-card">
            <div class="cm-home-card-label"><span>05 · local first</span><span>degrades cleanly</span></div>
            <h3>Your code stays local by default.</h3>
            <p>Structural queries work offline after indexing. Semantic retrieval can use local Ollama—or an explicitly configured remote endpoint.</p>
            <ul class="cm-home-stack"><li><span>graph</span><strong>SQLite · pure Go</strong></li><li><span>vectors</span><strong>veclite · local</strong></li><li><span>transport</span><strong>stdio MCP</strong></li></ul>
          </article>
        </div>
      </div>
    </section>

    <section class="cm-home-section cm-home-comparison" aria-labelledby="cm-home-comparison-title">
      <div class="cm-home-shell">
        <div class="cm-home-heading cm-home-heading-wide">
          <div><p class="cm-home-eyebrow">The difference, in one question</p><h2 id="cm-home-comparison-title">“What breaks if I change <code>Store.NodeAtLine</code>?”</h2></div>
        </div>
        <div class="cm-home-terminals">
          <article class="cm-home-terminal">
            <header><span>agent without codemap</span><strong class="bad">guessing</strong></header>
            <pre><span class="dim">●</span> Grep(<b>"NodeAtLine"</b>)
<span class="dim">●</span> Read(internal/graph/queries.go)
<span class="dim">●</span> Grep(<b>"NodeAtLine("</b>)
<span class="dim">●</span> Read(internal/app/service_grep.go)
<span class="dim">●</span> Read(internal/app/symbol_at_batch.go)
<span class="dim">… 19 more greps and reads</span></pre>
            <footer><span><strong>24 calls</strong> · six files in context</span><span>callers incomplete</span></footer>
          </article>
          <article class="cm-home-terminal">
            <header><span>agent with codemap</span><strong class="good">cited</strong></header>
            <pre><span class="dim">●</span> <i>codemap_impact</i>(<b>"Store.NodeAtLine"</b>)
{
  <em>"direct_callers"</em>: 7,
  <em>"blast_radius"</em>: 23,
  <em>"covering_tests"</em>: 13,
  <em>"call_graph"</em>: <b>"resolved"</b>
}</pre>
            <footer><span><strong>1 call</strong> · structured answer</span><span>every entry has file:line</span></footer>
          </article>
        </div>
        <p class="cm-home-comparison-note">Illustrative real-repository response shape. The measured benchmark above contains the reproducible aggregate result and methodology.</p>
      </div>
    </section>

    <section class="cm-home-section cm-home-adoption" aria-labelledby="cm-home-adoption-title">
      <div class="cm-home-shell">
        <div class="cm-home-heading">
          <div><p class="cm-home-eyebrow">Fits the loop you already use</p><h2 id="cm-home-adoption-title">From local agent to merge gate.</h2></div>
          <p>Install the same structural workflow in your coding harness and in pull-request automation.</p>
        </div>
        <div class="cm-home-adoption-grid">
          <article>
            <div class="cm-home-adoption-index">A</div><h3>One command per harness.</h3>
            <p>Register the MCP server without clobbering existing configuration, then install a playbook that teaches the agent when to use codemap.</p>
            <div class="cm-home-command"><span>$</span><code>codemap agent setup claude-code</code></div>
            <ul class="cm-home-harnesses" aria-label="Supported agent harnesses"><li>claude-code</li><li>cursor</li><li>codex</li><li>gemini</li><li>cline</li><li>roo</li><li>zed</li><li>vscode</li><li>opencode</li><li>aider</li><li>agents-md</li></ul>
            <a href="/agents">See agent integrations →</a>
          </article>
          <article>
            <div class="cm-home-adoption-index">B</div><h3>Review every pull request.</h3>
            <p>Post one sticky impact report and fail only on the risk or test-coverage rules your team chooses.</p>
            <pre><span>jobs:</span>
  review:
    uses: abdul-hamid-achik/codemap/
      .github/workflows/codemap-review-reusable.yml@main
    with:
      fail-on-untested: <b>'true'</b>
      fail-on-risk: <b>'high'</b></pre>
            <a href="/ci">Configure the CI review gate →</a>
          </article>
        </div>
      </div>
    </section>

    <section class="cm-home-final" aria-labelledby="cm-home-final-title">
      <div class="cm-home-shell cm-home-final-layout">
        <div>
          <p class="cm-home-eyebrow">Start with one repository</p><h2 id="cm-home-final-title">Give your agent the map.</h2>
          <p>Install codemap, index the repository, and ask your first structural question.</p>
          <div class="cm-home-actions"><a class="cm-home-button cm-home-button-primary" href="/quick-start">Index your first project</a><a class="cm-home-button cm-home-button-secondary" href="/mcp">Explore all MCP tools</a></div>
        </div>
        <div class="cm-home-final-terminal" aria-label="Three commands to start using codemap">
          <div><span>1</span><code>codemap init</code></div><div><span>2</span><code>codemap index --precise</code></div><div><span>3</span><code>codemap context YourSymbol</code></div>
        </div>
      </div>
    </section>
  </main>
</template>
