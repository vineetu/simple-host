import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import fs from 'node:fs';

const brand = process.env.BRAND || 'host';
const areas = JSON.parse(fs.readFileSync(new URL('./areas.json', import.meta.url)));
const guides = Object.entries(areas).filter(([, a]) => a.brands.includes(brand) && a.guide);
// Written by scripts/docs-reference.py (docs-site.py prepare) from the Go
// inventory commands, not a hand-kept list.
const reference = JSON.parse(fs.readFileSync(new URL('./.generated-content/reference.json', import.meta.url)));
const referenceGroup = (entries) => ({
  label: entries[0].title,
  collapsed: true,
  items: entries.map((e) => ({ label: e.title, slug: e.slug })),
});
// Written by scripts/docs-repo-pages.py's prepare(), freshly for this brand.
const repoPages = JSON.parse(fs.readFileSync(new URL('./.generated-content/self-hosting-pages.json', import.meta.url)));
const subgroup = (key) => ({
  label: repoPages[key].label,
  collapsed: true,
  items: [...(repoPages[key].overview ? [repoPages[key].overview] : []), ...repoPages[key].items],
});
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
      { label: 'Reference', items: [referenceGroup(reference.api), referenceGroup(reference.mcp)] },
      { label: 'Self-hosting and Enterprise', items: [
        { label: 'Overview', slug: 'self-hosting' },
        { label: 'Run and operate', slug: 'self-hosting/operate' },
        repoPages.configuration,
        subgroup('settings'),
        subgroup('platforms'),
        subgroup('operations'),
        ...(repoPages.kubernetes ? [repoPages.kubernetes] : []),
      ] },
      repoPages.changelog,
    ],
  })],
});
