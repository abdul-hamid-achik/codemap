<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { withBase } from 'vitepress'

const examples = [
  { id: 'code', label: 'Code', command: 'codemap context GetSession', nodes: [['Go', 'SessionService.Load'], ['Go', 'Queries.GetSession'], ['SQL', 'GetSession']], edges: ['calls · code graph', 'depends_on · sqlc mapping'], note: 'Call accuracy depends on the language and precise coverage.', link: '/agents' },
  { id: 'data', label: 'Data', command: 'codemap dependencies schema.sql', nodes: [['SQL', 'GetSession'], ['SQL', 'sessions'], ['SQL', 'SaveSession']], edges: ['reads → · candidate', '← writes · candidate'], note: 'SQL relationships are lexical candidates. Dynamic SQL and live database state are outside coverage.', link: '/data-and-docs' },
  { id: 'docs', label: 'Docs & config', command: 'codemap dependencies queries/get.sql', nodes: [['Markdown', 'README.md / Sessions'], ['SQL', 'queries/get.sql'], ['Go', 'Queries.GetSession']], edges: ['documents → · explicit link', '← depends_on · sqlc mapping'], note: 'Markdown examples stay outside the call graph. YAML templates are not evaluated.', link: '/data-and-docs' },
]
const selected = ref(0)
const example = computed(() => examples[selected.value])
const copied = ref<'idle' | 'copied' | 'error'>('idle')
let timer: ReturnType<typeof setTimeout> | undefined
async function copyInstall() {
  clearTimeout(timer)
  try {
    await navigator.clipboard.writeText('brew install abdul-hamid-achik/tap/codemap')
    copied.value = 'copied'
  } catch { copied.value = 'error' }
  timer = setTimeout(() => { copied.value = 'idle' }, 2500)
}
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <div class="cm-home">
    <section class="cm-hero cm-shell" aria-labelledby="cm-title">
      <div class="cm-intro">
        <p class="cm-eyebrow"><span class="cm-signal"></span> Local code intelligence · CLI + MCP</p>
        <h1 id="cm-title">See what your<br />change <em>touches.</em></h1>
        <p class="cm-lede">Explore the connections between code, data, configuration, and documentation. Give your agent source locations and explicit limits before it edits.</p>
        <div class="cm-actions">
          <a class="cm-button" :href="withBase('/quick-start')">Index your first project <span aria-hidden="true">↗</span></a>
          <a class="cm-text-link" :href="withBase('/agents')">Connect your agent <span aria-hidden="true">→</span></a>
        </div>
        <div class="cm-install">
          <code>brew install abdul-hamid-achik/tap/codemap</code>
          <button type="button" @click="copyInstall" aria-label="Copy install command">{{ copied === 'copied' ? 'Copied' : 'Copy' }}</button>
        </div>
        <p class="cm-copy-status" role="status">{{ copied === 'error' ? 'Clipboard unavailable. Select the command to copy it.' : copied === 'copied' ? 'Install command copied.' : '' }}</p>
        <p class="cm-small">One Go binary. Offline structural queries. Optional embeddings.</p>
      </div>

      <div class="cm-evidence" role="region" aria-label="Explore illustrative relationship examples">
        <header class="cm-evidence-header"><span>Relationship explorer</span><span class="cm-small">Example workspace</span></header>
        <div class="cm-example-buttons" role="group" aria-label="Example type">
          <button v-for="(item, i) in examples" :key="item.id" type="button" :aria-pressed="selected === i" @click="selected = i">{{ item.label }}</button>
        </div>
        <div class="cm-example" aria-live="polite">
          <div class="cm-graph">
            <template v-for="(node, i) in example.nodes" :key="`${example.id}-${i}`">
              <div v-if="i" class="cm-relationship"><span aria-hidden="true">│</span>{{ example.edges[i - 1] }}</div>
              <div class="cm-node" :class="{ 'cm-node-focus': i === 1 }"><span>{{ node[0] }}</span><strong>{{ node[1] }}</strong></div>
            </template>
          </div>
          <div class="cm-example-command"><span aria-hidden="true">$</span><code>{{ example.command }}</code></div>
          <p class="cm-evidence-note">{{ example.note }}</p>
        </div>
        <footer><span>Illustrative relationships, not live output</span><a :href="withBase(example.link)">Explore the workflow ↗</a></footer>
      </div>
    </section>

    <section class="cm-domains" aria-label="Graph domains">
      <div class="cm-shell"><span>Read the relationships</span><ul><li>Definitions</li><li>Calls & tests</li><li>SQL queries</li><li>YAML keys</li><li>Document links</li><li>Styles</li></ul></div>
    </section>

    <section class="cm-shell cm-workflow" aria-labelledby="cm-workflow-title">
      <div class="cm-section-intro"><p class="cm-eyebrow">A practical working loop</p><h2 id="cm-workflow-title">Start with a question.<br />Leave with evidence.</h2><p>Use the same graph from a terminal or a coding agent. Keep every follow-up tied to the definition you inspected.</p><a class="cm-text-link" :href="withBase('/quick-start')">Follow the walkthrough →</a></div>
      <ol class="cm-steps">
        <li><span>01</span><div><h3>Build the local map</h3><p>Index structure without an embedding service. Add precise calls where the language supports them.</p><code>codemap index --no-embed</code></div></li>
        <li><span>02</span><div><h3>Inspect what connects</h3><p>Find a definition, inspect its context, or follow a specific relationship across files.</p><code>codemap context GetSession</code></div></li>
        <li><span>03</span><div><h3>Check the change</h3><p>Inspect your diff and index freshness. Review call impact and non-call dependencies separately.</p><code>codemap review --json</code></div></li>
      </ol>
    </section>

    <section class="cm-format-section" aria-labelledby="cm-format-title">
      <div class="cm-shell">
        <div class="cm-section-heading"><div><p class="cm-eyebrow">Beyond source files</p><h2 id="cm-format-title">The rest of your repository<br />has structure, too.</h2></div><a class="cm-text-link" :href="withBase('/languages')">Read the capability matrix ↗</a></div>
        <div class="cm-format-list">
          <a :href="withBase('/data-and-docs#sql-and-sqlc')"><span class="cm-format-tag">.sql</span><div><h3>Queries belong in the map.</h3><p>Inspect tables, views, named queries, and lexical read/write relationships. Follow configured sqlc queries from generated Go.</p></div><span aria-hidden="true">↗</span></a>
          <a :href="withBase('/data-and-docs#yaml-configuration')"><span class="cm-format-tag">.yaml</span><div><h3>Configuration has dependencies.</h3><p>Navigate exact key paths. Trace Task dependencies, Compose services, and GitHub Actions job dependencies.</p></div><span aria-hidden="true">↗</span></a>
          <a :href="withBase('/data-and-docs#markdown-documentation')"><span class="cm-format-tag">.md</span><div><h3>Keep documentation connected.</h3><p>Inspect sections and follow local links into indexed files. Code examples remain documentation.</p></div><span aria-hidden="true">↗</span></a>
        </div>
      </div>
    </section>

    <section class="cm-shell cm-trust" aria-labelledby="cm-trust-title">
      <div><p class="cm-eyebrow">Know what the answer means</p><h2 id="cm-trust-title">Useful evidence.<br />Visible boundaries.</h2><p>A missing edge is not proof that nothing depends on your code. Codemap reports provenance, freshness, and partial coverage so you can choose the next check.</p><a class="cm-text-link" :href="withBase('/languages')">Understand accuracy →</a></div>
      <dl class="cm-trust-list"><div><dt>Exact location</dt><dd>File, line, kind, and qualified name identify the definition. Follow-up selectors survive ordinary line shifts.</dd></div><div><dt>Relationship confidence</dt><dd>Exact references and name-based candidates remain distinguishable. SQL and configuration relationships do not become function calls.</dd></div><div><dt>Local by default</dt><dd>Query the stored graph offline. Use local Ollama or delegate retrieval to Vecgrep. Remote embedding endpoints require explicit configuration.</dd></div></dl>
    </section>

    <section class="cm-shell cm-start" aria-labelledby="cm-start-title">
      <div><p class="cm-eyebrow">Two ways in</p><h2 id="cm-start-title">Your terminal.<br />Your agent’s tools.</h2></div>
      <div class="cm-start-options"><article><h3>Use the CLI</h3><code>codemap docs formats</code><p>Learn the workflow in your terminal. Query commands support JSON for scripts.</p><a class="cm-text-link" :href="withBase('/cli')">CLI reference →</a></article><article><h3>Connect over MCP</h3><code>codemap serve</code><p>Use the same services through MCP. Start with codemap_docs and select the profile your harness needs.</p><a class="cm-text-link" :href="withBase('/mcp')">MCP setup →</a></article></div>
    </section>
  </div>
</template>
