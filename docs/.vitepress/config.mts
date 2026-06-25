import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'codemap',
  description: 'Local-first code intelligence: a code graph + semantic search for agents and people.',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,
  srcDir: '.',
  base: process.env.VITEPRESS_BASE ?? '/',
  themeConfig: {
    siteTitle: 'codemap',
    search: { provider: 'local' },
    nav: [
      { text: 'Quick Start', link: '/quick-start' },
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
        text: 'Ecosystem',
        items: [
          { text: 'codemap ⇄ vecgrep', link: '/ecosystem' },
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
