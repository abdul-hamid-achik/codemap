import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'codemap',
  description: 'Local-first code intelligence: a code graph + semantic search for agents and people.',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,
  srcDir: '.',
  base: process.env.VITEPRESS_BASE ?? '/',

  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    ['link', { rel: 'icon', type: 'image/png', sizes: '32x32', href: '/favicon-32.png' }],
    ['link', { rel: 'apple-touch-icon', sizes: '180x180', href: '/apple-touch-icon.png' }],
    ['link', { rel: 'preconnect', href: 'https://fonts.googleapis.com' }],
    ['link', { rel: 'preconnect', href: 'https://fonts.gstatic.com', crossorigin: '' }],
    ['meta', { name: 'description', content: 'A local-first code graph + semantic search your coding agent calls over MCP. One pure-Go binary, fully offline.' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:title', content: 'codemap — your agent greps. codemap knows.' }],
    ['meta', { property: 'og:description', content: 'Local-first code intelligence for coding agents: who calls this, what breaks, which tests cover it — one call each, over MCP or CLI.' }],
    ['meta', { property: 'og:url', content: 'https://codemap.tools' }],
    ['meta', { property: 'og:image', content: 'https://codemap.tools/og-image.png' }],
    ['meta', { property: 'og:image:width', content: '1200' }],
    ['meta', { property: 'og:image:height', content: '630' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:image', content: 'https://codemap.tools/og-image.png' }],
  ],

  sitemap: { hostname: 'https://codemap.tools' },
  themeConfig: {
    siteTitle: 'codemap',
    logo: '/mark.svg',
    search: { provider: 'local' },
    nav: [
      { text: 'Quick Start', link: '/quick-start' },
      { text: 'For Agents', link: '/agents' },
      { text: 'CLI', link: '/cli' },
      { text: 'studio', link: '/studio' },
      { text: 'MCP', link: '/mcp' },
      { text: 'CI', link: '/ci' },
    ],
    sidebar: [
      {
        text: 'Get Started',
        items: [
          { text: 'Overview', link: '/' },
          { text: 'Quick Start', link: '/quick-start' },
          { text: 'Configuration', link: '/configuration' },
          { text: 'Branches & caching', link: '/branches' },
        ],
      },
      {
        text: 'Surfaces',
        items: [
          { text: 'CLI', link: '/cli' },
          { text: 'studio (TUI)', link: '/studio' },
          { text: 'MCP server', link: '/mcp' },
        ],
      },
      {
        text: 'Agents',
        items: [
          { text: 'For agents', link: '/agents' },
          { text: 'CI review gate', link: '/ci' },
        ],
      },
      {
        text: 'Ecosystem',
        items: [
          { text: 'vecgrep · tinyvault', link: '/ecosystem' },
        ],
      },
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/abdul-hamid-achik/codemap' },
    ],
    editLink: {
      pattern: 'https://github.com/abdul-hamid-achik/codemap/edit/main/docs/:path',
      text: 'Edit this page',
    },
    footer: {
      message: 'Local-first code intelligence.',
      copyright: 'MIT Licensed © Abdul Hamid Achik',
    },
  },
})
