import * as path from 'node:path';
import { defineConfig } from '@rspress/core';

const sidebar = {
  '/': [
    {
      text: 'Home',
      link: '/',
      items: [
        { text: 'Requirements', link: '/#requirements' },
        { text: 'Install', link: '/#install' },
        { text: 'The 60-second path', link: '/#the-60-second-path' },
      ],
    },
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
    {
      text: 'Architecture',
      link: '/architecture',
      items: [
        { text: '1. Problem statement', link: '/architecture#1-problem-statement' },
        { text: '2. Design principles', link: '/architecture#2-design-principles' },
        { text: '3. System overview', link: '/architecture#3-system-overview' },
        { text: '4. Instance model', link: '/architecture#4-instance-model' },
        { text: '5. Filesystem and repository strategy', link: '/architecture#5-filesystem-and-repository-strategy' },
        { text: '6. Port forwarding', link: '/architecture#6-port-forwarding' },
        { text: '7. The container', link: '/architecture#7-the-container' },
        { text: '8. Credentials', link: '/architecture#8-credentials' },
        { text: '9. Host ↔ session interaction', link: '/architecture#9-host--session-interaction' },
        { text: '10. State, reconciliation, and failure', link: '/architecture#10-state-reconciliation-and-failure' },
        { text: '11. CLI surface', link: '/architecture#11-cli-surface' },
        { text: '12. Technology choices', link: '/architecture#12-technology-choices' },
        { text: '13. Phasing', link: '/architecture#13-phasing' },
        { text: '14. Open questions', link: '/architecture#14-open-questions' },
        { text: 'Appendix A — Mount strategy evidence', link: '/architecture#appendix-a--mount-strategy-evidence' },
        { text: 'Appendix B — Git worktrees across the container boundary', link: '/architecture#appendix-b--git-worktrees-across-the-container-boundary' },
      ],
    },
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
