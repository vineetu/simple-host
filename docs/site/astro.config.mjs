import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import fs from 'node:fs';

const brand = process.env.BRAND || 'host';
const areas = JSON.parse(fs.readFileSync(new URL('./areas.json', import.meta.url)));
const guides = Object.entries(areas).filter(([, a]) => a.brands.includes(brand) && a.guide);
export default defineConfig({
  base: '/docs/',
  trailingSlash: 'always',
  outDir: `../../internal/webassets/docs-site/${brand}`,
  compressHTML: false,
  build: { inlineStylesheets: 'never' },
  vite: { resolve: { alias: { '@docs': new URL('./src/components', import.meta.url).pathname } }, build: { sourcemap: false } },
  integrations: [starlight({
    title: brand === 'hack' ? 'Simple Hack' : 'Simple Host',
    disable404Route: true,
    credits: false,
    expressiveCode: false,
    tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 2 },
    customCss: ['./src/styles/docs.css'],
    components: {
      Head: './src/components/Head.astro',
      Header: './src/components/Header.astro',
      PageFrame: './src/components/PageFrame.astro',
      ThemeProvider: './src/components/Empty.astro',
      ThemeSelect: './src/components/Empty.astro',
    },
    sidebar: [
      { label: 'Docs home', link: '/' },
      { label: 'Get started', items: [
        { label: brand === 'hack' ? 'Run an event' : 'Your first site', slug: 'start' },
        ...(brand === 'hack' ? [{slug:'start/join'}, {slug:'start/judge'}] : []),
        { slug: 'start/api' }, { slug: 'start/pages' },
      ] },
      { label: 'Guides', items: guides.map(([id, a]) => ({label:a.title, slug:`guides/${id}`})) },
      { slug: 'webhooks' }, { slug: 'limits' },
      { label: 'Reference', items: [{ slug: 'api' }, { slug: 'mcp' }] },
      { label: 'Self-hosting', items: [{ slug: 'self-hosting' }, { slug: 'self-hosting/operate' }] },
    ],
  })],
});
