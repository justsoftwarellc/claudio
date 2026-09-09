import * as path from 'node:path';
import { defineConfig } from '@rspress/core';

export default defineConfig({
  root: path.join(__dirname, 'docs'),
  lang: 'en',
  title: 'claudio',
  description: 'Run several sandboxed Claude Code sessions in parallel',
  themeConfig: {
    socialLinks: [
      {
        icon: 'github',
        mode: 'link',
        content: 'https://github.com/rodrigomorales/claudio',
      },
    ],
  },
});
