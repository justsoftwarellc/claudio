/**
 * The doc footer's prev/next links are derived from the sidebar, not
 * written by hand, so a malformed sidebar shows up only as a wrong
 * arrow at the bottom of a page. `walk` below is a port of Rspress's
 * own flattening (@rspress/core theme/logic/usePrevNextPage): a group
 * contributes its own entry when it has a `link`, then each child. The
 * lookup is findIndex — first match wins — so a group linked to its
 * own first child appears twice in a row and that child's "next"
 * points back at itself. These tests pin the resulting order.
 */
import { describe, expect, it } from 'vitest';
import config from './rspress.config';

type Item = { text: string; link?: string; items?: Item[] };

/** Rspress's sidebar flattening, reproduced. */
function flatten(items: Item[]): { text: string; link: string }[] {
  const flat: { text: string; link: string }[] = [];
  const walk = (item: Item) => {
    if (item.items) {
      if (item.link) flat.push({ text: item.text, link: item.link });
      item.items.forEach(walk);
    } else if (item.link) {
      flat.push({ text: item.text, link: item.link });
    }
  };
  items.forEach(walk);
  return flat;
}

const sidebar = config.themeConfig?.sidebar as Record<string, Item[]>;
const flat = flatten(sidebar['/docs/']);

/** The prev/next pair Rspress would render at the foot of `link`. */
function footer(link: string) {
  const i = flat.findIndex((entry) => entry.link === link);
  expect(i, `${link} is not in the sidebar`).toBeGreaterThanOrEqual(0);
  return { prev: flat[i - 1]?.link ?? null, next: flat[i + 1]?.link ?? null };
}

describe('doc footer navigation', () => {
  it('sends Getting Started on to Configuration, not back to itself', () => {
    expect(footer('/docs/guide/getting-started').next).toBe('/docs/guide/configuration');
  });

  it('walks the whole guide in sidebar order', () => {
    expect(flat.map((entry) => entry.link)).toEqual([
      '/docs/',
      '/docs/guide/getting-started',
      '/docs/guide/configuration',
      '/docs/guide/compose-sidecars',
      '/docs/guide/host-services',
      '/docs/guide/non-obvious-decisions',
      '/docs/guide/troubleshooting',
      '/docs/architecture',
    ]);
  });

  it('lists no page twice, so no page can link to itself', () => {
    const links = flat.map((entry) => entry.link);
    expect(new Set(links).size).toBe(links.length);
  });

  it('leaves the ends of the run without a prev/next', () => {
    expect(footer('/docs/').prev).toBeNull();
    expect(footer('/docs/architecture').next).toBeNull();
  });
});
