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
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'codemap' }],
    ['meta', { property: 'og:image', content: 'https://codemap.tools/og-image.png' }],
    ['meta', { property: 'og:image:width', content: '1200' }],
    ['meta', { property: 'og:image:height', content: '630' }],
    ['meta', { property: 'og:image:alt', content: 'codemap returning cited structural impact for a code symbol' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:image', content: 'https://codemap.tools/og-image.png' }],
    ['meta', { name: 'twitter:image:alt', content: 'codemap returning cited structural impact for a code symbol' }],
    ['meta', { name: 'theme-color', media: '(prefers-color-scheme: light)', content: '#faf9f5' }],
    ['meta', { name: 'theme-color', media: '(prefers-color-scheme: dark)', content: '#141c19' }],
  ],

  sitemap: { hostname: 'https://codemap.tools' },
  transformPageData(pageData) {
    const relativeUrl = pageData.relativePath
      .replace(/(^|\/)index\.md$/, '$1')
      .replace(/\.md$/, '')
    const canonicalUrl = new URL(relativeUrl, 'https://codemap.tools/').href
    const isHome = pageData.relativePath === 'index.md'
    const socialTitle = isHome
      ? 'codemap — local-first code intelligence for agents'
      : `${pageData.title} — codemap`
    const description = pageData.description || 'Local-first code intelligence for agents and people.'
    const head = pageData.frontmatter.head ?? []

    head.push(
      ['link', { rel: 'canonical', href: canonicalUrl }],
      ['meta', { property: 'og:title', content: socialTitle }],
      ['meta', { property: 'og:description', content: description }],
      ['meta', { property: 'og:url', content: canonicalUrl }],
      ['meta', { name: 'twitter:title', content: socialTitle }],
      ['meta', { name: 'twitter:description', content: description }],
    )
    pageData.frontmatter.head = head
  },
  themeConfig: {
    siteTitle: 'codemap',
    logo: '/mark.svg',
    search: { provider: 'local' },
    nav: [
      { text: 'Quick Start', link: '/quick-start' },
      { text: 'For Agents', link: '/agents' },
      { text: 'CLI', link: '/cli' },
      { text: 'MCP', link: '/mcp' },
      { text: 'Languages', link: '/languages' },
    ],
    sidebar: [
      {
        text: 'Get Started',
        items: [
          { text: 'Overview', link: '/' },
          { text: 'Quick Start', link: '/quick-start' },
          { text: 'Language support', link: '/languages' },
          { text: 'Data, config & docs', link: '/data-and-docs' },
          { text: 'Configuration', link: '/configuration' },
          { text: 'Branches & caching', link: '/branches' },
        ],
      },
      {
        text: 'Surfaces',
        items: [
          { text: 'CLI', link: '/cli' },
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
      message: 'This docs site uses cookie-free Vercel Web Analytics. codemap sends no usage telemetry.',
      copyright: 'MIT Licensed © Abdul Hamid Achik',
    },
  },
})
