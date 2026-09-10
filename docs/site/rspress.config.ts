import * as path from 'node:path';
import { pathToFileURL } from 'node:url';
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
//
// Everything documentation lives under /docs/; "/" is the landing page
// (the rotating 3D logo), which is deliberately absent from the sidebar
// — it has no prose to navigate, and the nav bar already links it.
const sidebar = {
  '/docs/': [
    { text: 'Home', link: '/docs/' },
    {
      text: 'Guide',
      link: '/docs/guide/getting-started',
      items: [
        { text: 'Getting Started', link: '/docs/guide/getting-started' },
        { text: 'Configuration', link: '/docs/guide/configuration' },
        { text: 'Adding a Database or Another Service', link: '/docs/guide/compose-sidecars' },
        { text: 'Non-Obvious Decisions', link: '/docs/guide/non-obvious-decisions' },
        { text: 'Troubleshooting', link: '/docs/guide/troubleshooting' },
      ],
    },
    { text: 'Architecture', link: '/docs/architecture' },
  ],
};

// Google Analytics (GA4). Injected as <head> tags rather than pasted into
// an HTML template because Rspress owns the document shell — there is no
// index.html to edit. The gtag.js URL is absolute, so Rsbuild's
// ensureAssetPrefix passes it through untouched (URL.canParse succeeds)
// even though tag injection defaults to publicPath: true.
//
// Rspress is a SPA: gtag('config') fires one page_view on the initial
// load, and client-side route changes after that do not reload the
// document. The listener below reports those to GA as page_view events,
// so in-site navigation isn't invisible in analytics.
const GA_MEASUREMENT_ID = 'G-VHK3N7FFQZ';

const googleAnalyticsTags = [
  {
    tag: 'script',
    head: true,
    attrs: {
      async: true,
      src: `https://www.googletagmanager.com/gtag/js?id=${GA_MEASUREMENT_ID}`,
    },
  },
  {
    tag: 'script',
    head: true,
    children: `
window.dataLayer = window.dataLayer || [];
function gtag(){dataLayer.push(arguments);}
gtag('js', new Date());
gtag('config', '${GA_MEASUREMENT_ID}');

// SPA route changes: patch the History API (pushState/replaceState fire
// no event of their own) and listen for back/forward via popstate.
(function () {
  var lastPath = location.pathname + location.search;
  function reportPageView() {
    var path = location.pathname + location.search;
    if (path === lastPath) return;
    lastPath = path;
    gtag('event', 'page_view', {
      page_path: path,
      page_location: location.href,
      page_title: document.title,
    });
  }
  ['pushState', 'replaceState'].forEach(function (method) {
    var original = history[method];
    history[method] = function () {
      var result = original.apply(this, arguments);
      // Defer so the new document.title is in place before we report.
      setTimeout(reportPageView, 0);
      return result;
    };
  });
  window.addEventListener('popstate', function () {
    setTimeout(reportPageView, 0);
  });
})();
`.trim(),
  },
];

export default defineConfig({
  root: path.join(__dirname, 'docs'),
  lang: 'en',
  title: 'claudio',
  description: 'Run several sandboxed Claude Code sessions in parallel',
  // A file:// URL, not a path: normalizeIcon (@rspress/core) rewrites a
  // plain absolute path to <docRoot>/public/<path>, which would look for
  // this file under docs/public/. favicon.ico lives beside this config,
  // and the URL form is passed through as an absolute path unchanged —
  // so it resolves wherever the build is invoked from.
  icon: pathToFileURL(path.join(__dirname, 'favicon.ico')),
  builderConfig: {
    html: {
      tags: googleAnalyticsTags,
    },
  },
  themeConfig: {
    nav: [
      { text: 'Home', link: '/docs/', activeMatch: '^/docs/$' },
      { text: 'Guide', link: '/docs/guide/getting-started', activeMatch: '/docs/guide/' },
      { text: 'Architecture', link: '/docs/architecture', activeMatch: '/docs/architecture' },
    ],
    sidebar,
    socialLinks: [
      {
        icon: 'github',
        mode: 'link',
        content: 'https://github.com/justsoftwarellc/claudio',
      },
    ],
  },
});
