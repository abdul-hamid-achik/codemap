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
    ['link', { rel: 'icon', type: 'image/x-icon', href: '/favicon.ico' }],
    ['link', { rel: 'apple-touch-icon', sizes: '180x180', href: '/apple-touch-icon.png' }],
    ['meta', { name: 'description', content: 'codemap documentation site.' }],
  ],

  sitemap: { hostname: 'https://codemap.tools' },
  themeConfig: {
    siteTitle: 'codemap',
    logo: { src: '/logo.svg', dark: '/logo-dark.svg' },
    search: { provider: 'local' },
    nav: [
      { text: 'Quick Start', link: '/quick-start' },
      { text: 'For Agents', link: '/agents' },
      { text: 'CLI', link: '/cli' },
      { text: 'studio', link: '/studio' },
      { text: 'MCP', link: '/mcp' },
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
