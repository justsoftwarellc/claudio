import { useEffect, useRef, useState } from 'react';
import styles from './Hero.module.css';

const BACKGROUND = '#07100a';

/**
 * The landing hero: the rotating claudio mark over a "Get started" link
 * into the docs.
 *
 * three.js and the model are loaded dynamically inside the effect rather
 * than imported at module scope. The site static-renders every route at
 * build time, where `window`, `document`, and WebGL do not exist — a
 * top-level `import 'three'` evaluates during SSR and breaks the build.
 * The dynamic import keeps that whole subtree client-only, and has the
 * happy side effect of leaving three out of the initial bundle for every
 * other page.
 */
export function Hero() {
  const mount = useRef<HTMLDivElement>(null);
  // Reflects renderer state, not just fetch state: WebGL can be absent
  // (or blocked) on a browser that loads the module fine, so failure has
  // to be a first-class outcome rather than an unhandled rejection.
  const [status, setStatus] = useState<'loading' | 'ready' | 'failed'>('loading');

  useEffect(() => {
    const host = mount.current;
    if (!host) return;

    // The effect can be torn down before the dynamic imports settle
    // (React 18 StrictMode double-invokes it in dev, and a fast route
    // change does the same in production). `cancelled` keeps the late
    // resolver from mounting a canvas into a detached node, and the
    // teardown refs let it clean up whatever did get built.
    let cancelled = false;
    let stage: { dispose(): void } | null = null;
    let model: { dispose(): void } | null = null;

    (async () => {
      try {
        const [{ createStage }, { buildModel }] = await Promise.all([
          import('./stage'),
          import('./model'),
        ]);
        if (cancelled) return;

        // Motion here is decorative — the turntable and the matrix
        // cascade carry no information the still model doesn't. CSS
        // can't reach either one (both are driven from JS), so the
        // preference is read once and passed down.
        const still = window.matchMedia(
          '(prefers-reduced-motion: reduce)',
        ).matches;

        const s = createStage(host, BACKGROUND, !still);
        stage = s;
        const m = buildModel(s.THREE, !still);
        model = m;
        s.setObject(m.object);
        setStatus('ready');
      } catch (err) {
        if (cancelled) return;
        // A missing WebGL context is a normal outcome on some machines,
        // not a bug to surface to the reader — the page still works, so
        // log for us and fall back to the wordmark for them.
        console.error('claudio logo: 3D stage failed to start', err);
        setStatus('failed');
      }
    })();

    return () => {
      cancelled = true;
      model?.dispose();
      stage?.dispose();
    };
  }, []);

  return (
    <section className={styles.hero} style={{ background: BACKGROUND }}>
      <div
        ref={mount}
        className={styles.stage}
        // The canvas is decoration; the h1 below carries the name.
        aria-hidden="true"
        data-status={status}
      />

      {status === 'failed' && <p className={styles.fallbackMark}>claudio</p>}

      <div className={styles.content}>
        <h1 className={styles.title}>claudio</h1>
        <p className={styles.tagline}>
          Run several sandboxed Claude Code sessions in parallel — each with its
          own container, its own git worktree, and its own ports.
        </p>
        <a className={styles.cta} href="/docs/">
          Get started
        </a>
      </div>
    </section>
  );
}
