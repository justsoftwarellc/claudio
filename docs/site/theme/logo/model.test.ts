/**
 * The logo model is pure CPU work — geometry, materials, and canvas
 * textures — so it can be built and driven headlessly without WebGL.
 * These tests pin the things that would silently ruin the mark: a
 * container going missing, the model floating off the ground plane, the
 * cascade never painting, and the animation loop outliving the page.
 */
import { describe, expect, it, vi } from 'vitest';
import * as THREE from 'three';
import { buildModel } from './model';

/** A canvas 2D stub that records the glyphs the cascade paints. */
function stubCanvas() {
  const strokes: string[] = [];
  vi.stubGlobal('document', {
    createElement: () => ({
      width: 0,
      height: 0,
      getContext: () => ({
        fillStyle: '',
        font: '',
        textAlign: '',
        fillRect() {},
        fillText(text: string) {
          strokes.push(text);
        },
        createLinearGradient: () => ({ addColorStop() {} }),
      }),
    }),
  });
  return strokes;
}

/**
 * A rAF stub that re-captures the callback on every schedule. The loop
 * reschedules itself each tick, so a stub that keeps only the first
 * callback replays frame one forever and never crosses the cascade's
 * accumulator threshold.
 */
function stubRaf() {
  const state = { pending: null as ((t: number) => void) | null };
  vi.stubGlobal('requestAnimationFrame', (f: (t: number) => void) => {
    state.pending = f;
    return 1;
  });
  vi.stubGlobal('cancelAnimationFrame', () => {
    state.pending = null;
  });
  return {
    state,
    /** Run `count` frames `stepMs` apart, as a real rAF would. */
    run(count: number, stepMs = 50) {
      let t = 0;
      for (let i = 0; i < count; i++) {
        const f = state.pending;
        if (!f) break;
        t += stepMs;
        f(t);
      }
    },
  };
}

describe('buildModel', () => {
  it('assembles the full mark', () => {
    stubCanvas();
    stubRaf();
    const model = buildModel(THREE, true);
    const children = model.object.children;

    expect(model.object.name).toBe('claudio_logo');
    expect(children.filter((c) => c.name.startsWith('container_'))).toHaveLength(7);
    expect(children.filter((c) => c.name.startsWith('spoke_'))).toHaveLength(7);
    expect(children.filter((c) => c.name.startsWith('rail_'))).toHaveLength(2);
    expect(children.some((c) => c.name === 'hub')).toBe(true);

    model.dispose();
  });

  it('rests on the ground plane so the stage shadow lands under it', () => {
    stubCanvas();
    stubRaf();
    const model = buildModel(THREE, true);

    const box = new THREE.Box3().setFromObject(model.object);
    expect(box.min.y).toBeCloseTo(0, 6);

    model.dispose();
  });

  it('animates the cascade and tumbles the hub', () => {
    const strokes = stubCanvas();
    const raf = stubRaf();
    const model = buildModel(THREE, true);
    const hub = model.object.children.find((c) => c.name === 'hub')!;
    const before = hub.rotation.y;

    raf.run(12);

    expect(strokes.length).toBeGreaterThan(0);
    expect(hub.rotation.y).toBeGreaterThan(before);

    model.dispose();
  });

  it('holds still under reduced motion, but still paints the screens', () => {
    const strokes = stubCanvas();
    const raf = stubRaf();

    const model = buildModel(THREE, false);

    // No loop scheduled — nothing moves on its own…
    expect(raf.state.pending).toBeNull();
    // …but the terminals are lit rather than blank.
    expect(strokes.length).toBeGreaterThan(0);

    model.dispose();
  });

  it('cancels its animation loop on dispose', () => {
    stubCanvas();
    const raf = stubRaf();
    const model = buildModel(THREE, true);
    expect(raf.state.pending).not.toBeNull();

    model.dispose();

    // A hero that outlives its route must not keep a rAF loop alive.
    expect(raf.state.pending).toBeNull();
  });
});
