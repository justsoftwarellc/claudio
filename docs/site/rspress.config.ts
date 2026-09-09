import * as path from 'node:path';
import { defineConfig } from '@rspress/core';

// Sidebar "items" only make sense for links that resolve to distinct
// pathnames — Rspress's active-state matcher (runtime/route.js's
// matchPath) compares pathname only, ignoring the hash fragment, so a
// group of hash-only links to the same page (e.g. "/#requirements",
// "/#install") all normalize to the same pathname and light up as
// active simultaneously. Verified empirically (and reported by the
// user with a screenshot: every "Home" sub-item highlighted at once).
// Guide's items are real, separate pages, so those work correctly;
// Home and Architecture are single pages and stay single sidebar
// entries — Rspress's own right-hand "on this page" outline already
// covers in-page heading navigation for both.
const sidebar = {
  '/': [
    { text: 'Home', link: '/' },
    {
      text: 'Guide',
      link: '/guide/getting-started',
      items: [
        { text: 'Getting Started', link: '/guide/getting-started' },
        { text: 'Configuration', link: '/guide/configuration' },
        { text: 'Adding a Database or Another Service', link: '/guide/compose-sidecars' },
        { text: 'Non-Obvious Decisions', link: '/guide/non-obvious-decisions' },
        { text: 'Troubleshooting', link: '/guide/troubleshooting' },
      ],
    },
    { text: 'Architecture', link: '/architecture' },
  ],
};

export default defineConfig({
  root: path.join(__dirname, 'docs'),
  lang: 'en',
  title: 'claudio',
  description: 'Run several sandboxed Claude Code sessions in parallel',
  themeConfig: {
    nav: [
      { text: 'Home', link: '/', activeMatch: '^/$' },
      { text: 'Guide', link: '/guide/getting-started', activeMatch: '/guide/' },
      { text: 'Architecture', link: '/architecture', activeMatch: '/architecture' },
    ],
    sidebar,
    socialLinks: [
      {
        icon: 'github',
        mode: 'link',
        content: 'https://github.com/rodrigomorales/claudio',
      },
    ],
  },
});
