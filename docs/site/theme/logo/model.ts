/**
 * The claudio mark: seven containers riding a broken ring, each face a
 * terminal running its own matrix cascade, fed by coolant spokes from a
 * tumbling hub.
 *
 * The geometry, materials, and animation are the Claude Design export's
 * (docs/logo-design/claudio-model.js) — reproduced here as a module that
 * takes its THREE instance and returns a teardown handle, rather than
 * reaching for a global <three-d-stage> and leaking a rAF loop. The
 * numbers are the design's own; changing them changes the mark.
 */
import type * as THREE_NS from 'three';

export interface Model {
  object: THREE_NS.Object3D;
  /** Stop the animation loop and free the generated canvas textures. */
  dispose(): void;
}

const GLYPHS =
  'ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﬀﬁ0123456789ABCDEFGHKLMNPRSTVXZ<>|/*+=';

export function buildModel(THREE: typeof THREE_NS, animate = true): Model {
  const disposables: { dispose(): void }[] = [];

  /* ---------- materials ---------- */
  const M = {
    green: new THREE.MeshStandardMaterial({ name: 'matrix_green', color: 0x1f7a34, roughness: 0.5, metalness: 0.08, emissive: 0x081c0e }),
    black: new THREE.MeshStandardMaterial({ name: 'matrix_black', color: 0x14180f, roughness: 0.55, metalness: 0.2 }),
    edge: new THREE.MeshStandardMaterial({ name: 'edge_green', color: 0x2fa54c, roughness: 0.35, metalness: 0.25 }),
    door: new THREE.MeshStandardMaterial({ name: 'door_black', color: 0x0d110c, roughness: 0.5, metalness: 0.2 }),
  };
  Object.values(M).forEach((m) => disposables.push(m));

  /* ---------- matrix rain textures ---------- */
  interface Rain {
    tex: THREE_NS.CanvasTexture;
    step(): void;
  }
  const rains: Rain[] = [];

  function makeRain(): Rain {
    const W = 192, H = 384, cols = 5, fs = 36;
    const colW = W / cols, rows = Math.ceil(H / fs);
    const canvas = document.createElement('canvas');
    canvas.width = W;
    canvas.height = H;
    const ctx = canvas.getContext('2d')!;
    ctx.fillStyle = '#050a06';
    ctx.fillRect(0, 0, W, H);
    const drops = Array.from({ length: cols }, () => Math.floor(Math.random() * rows));
    const speed = Array.from({ length: cols }, () => 0.5 + Math.random());
    const tex = new THREE.CanvasTexture(canvas);
    tex.colorSpace = THREE.SRGBColorSpace;
    disposables.push(tex);
    const rain: Rain = {
      tex,
      step() {
        ctx.fillStyle = 'rgba(5,10,6,0.28)';
        ctx.fillRect(0, 0, W, H);
        ctx.font = `${fs - 4}px "Courier New", monospace`;
        ctx.textAlign = 'center';
        for (let c = 0; c < cols; c++) {
          const g = GLYPHS[Math.floor(Math.random() * GLYPHS.length)];
          const y = drops[c] * fs;
          ctx.fillStyle = '#1f8c3c';
          ctx.fillText(GLYPHS[Math.floor(Math.random() * GLYPHS.length)], colW * (c + 0.5), y - fs);
          ctx.fillStyle = '#c9ffd8';
          ctx.fillText(g, colW * (c + 0.5), y);
          drops[c] += speed[c] > 1 ? 1 : Math.random() < 0.7 ? 1 : 0;
          if (drops[c] * fs > H + fs * 3 && Math.random() < 0.3) drops[c] = 0;
        }
        tex.needsUpdate = true;
      },
    };
    rains.push(rain);
    return rain;
  }
  const rainVariants = [makeRain(), makeRain(), makeRain()];

  function screenMaterial(rain: Rain) {
    const m = new THREE.MeshStandardMaterial({
      name: 'terminal_screen',
      color: 0x0a1209,
      roughness: 0.25,
      metalness: 0,
      map: rain.tex,
      emissive: 0xffffff,
      emissiveMap: rain.tex,
      emissiveIntensity: 1.45,
    });
    disposables.push(m);
    return m;
  }

  /* ---------- flowing-liquid pipe textures ---------- */
  function flowTexture(repeatX: number, repeatY: number) {
    const W = 8, H = 128;
    const canvas = document.createElement('canvas');
    canvas.width = W;
    canvas.height = H;
    const ctx = canvas.getContext('2d')!;
    const grad = ctx.createLinearGradient(0, 0, 0, H);
    grad.addColorStop(0.0, '#02150a');
    grad.addColorStop(0.34, '#0d3d1c');
    grad.addColorStop(0.5, '#8dffb0');
    grad.addColorStop(0.62, '#3fd968');
    grad.addColorStop(0.8, '#0d3d1c');
    grad.addColorStop(1.0, '#02150a');
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, W, H);
    const tex = new THREE.CanvasTexture(canvas);
    tex.colorSpace = THREE.SRGBColorSpace;
    tex.wrapS = tex.wrapT = THREE.RepeatWrapping;
    tex.repeat.set(repeatX, repeatY);
    disposables.push(tex);
    return tex;
  }
  const spokeFlow = flowTexture(1, 1.6);

  // dark gradient shell for the outer C rails
  function darkGradient() {
    const W = 8, H = 256;
    const canvas = document.createElement('canvas');
    canvas.width = W;
    canvas.height = H;
    const ctx = canvas.getContext('2d')!;
    const g = ctx.createLinearGradient(0, 0, 0, H);
    g.addColorStop(0.0, '#050b07');
    g.addColorStop(0.45, '#123a20');
    g.addColorStop(0.7, '#0a1c11');
    g.addColorStop(1.0, '#020704');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, W, H);
    const tex = new THREE.CanvasTexture(canvas);
    tex.colorSpace = THREE.SRGBColorSpace;
    tex.wrapS = tex.wrapT = THREE.RepeatWrapping;
    disposables.push(tex);
    return tex;
  }
  const railGradient = darkGradient();

  const spokeMat = new THREE.MeshStandardMaterial({
    name: 'coolant_spoke',
    color: 0x0b1a0f,
    roughness: 0.28,
    metalness: 0.1,
    map: spokeFlow,
    emissive: 0xffffff,
    emissiveMap: spokeFlow,
    emissiveIntensity: 1.1,
  });
  const railMat = new THREE.MeshStandardMaterial({
    name: 'rail_dark',
    color: 0xffffff,
    map: railGradient,
    roughness: 0.4,
    metalness: 0.22,
    emissive: 0x0a2412,
    emissiveIntensity: 0.5,
  });
  disposables.push(spokeMat, railMat);

  /* ---------- containers ---------- */
  const R = 0.58;
  const CX = 0.22, CY = 0.34, CZ = 0.26;

  function makeContainer(mat: THREE_NS.Material, rain: Rain, label: string) {
    const g = new THREE.Group();
    g.name = label;

    const body = new THREE.Mesh(new THREE.BoxGeometry(CX, CY, CZ), mat);
    body.name = label + '_shell';
    g.add(body);

    const ribGeo = new THREE.BoxGeometry(CX * 0.94, 0.022, 0.016);
    const ribCount = 9;
    for (let s = -1; s <= 1; s += 2) {
      for (let i = 0; i < ribCount; i++) {
        const rib = new THREE.Mesh(ribGeo, mat);
        rib.name = label + '_rib';
        rib.position.set(0, -CY / 2 + (CY * (i + 0.5)) / ribCount, s * (CZ / 2 + 0.006));
        g.add(rib);
      }
    }

    // terminal screens on both broad faces — matrix cascade
    const screenMat = screenMaterial(rain);
    for (let s = -1; s <= 1; s += 2) {
      const screen = new THREE.Mesh(new THREE.PlaneGeometry(CX * 0.78, CY * 0.86), screenMat);
      screen.name = label + '_screen';
      screen.position.z = s * (CZ / 2 + 0.017);
      if (s < 0) screen.rotation.y = Math.PI;
      g.add(screen);
    }

    const plate = new THREE.Mesh(new THREE.BoxGeometry(0.014, CY * 0.86, CZ * 0.84), M.door);
    plate.name = label + '_door';
    plate.position.x = CX / 2 + 0.005;
    g.add(plate);
    for (let s = -1; s <= 1; s += 2) {
      const bar = new THREE.Mesh(new THREE.CylinderGeometry(0.011, 0.011, CY * 0.9, 16), M.edge);
      bar.name = label + '_latch';
      bar.position.set(CX / 2 + 0.016, 0, s * CZ * 0.22);
      g.add(bar);
    }
    return g;
  }

  const logo = new THREE.Group();
  logo.name = 'claudio_logo';

  const start = 42, sweep = 276, n = 7;
  for (let i = 0; i < n; i++) {
    const deg = start + sweep * (i / (n - 1));
    const t = THREE.MathUtils.degToRad(deg);
    const mat = i % 2 === 0 ? M.black : M.green;
    const c = makeContainer(mat, rainVariants[i % rainVariants.length], 'container_' + (i + 1));
    c.position.set(Math.cos(t) * R, Math.sin(t) * R, 0);
    c.rotation.z = t;
    logo.add(c);
  }

  /* ---------- rails ---------- */
  const arc = THREE.MathUtils.degToRad(sweep);
  for (let s = -1; s <= 1; s += 2) {
    const rail = new THREE.Mesh(new THREE.TorusGeometry(R, 0.026, 20, 160, arc), railMat);
    rail.name = 'rail_' + (s < 0 ? 'back' : 'front');
    rail.rotation.z = THREE.MathUtils.degToRad(start);
    rail.position.z = s * (CZ / 2 + 0.055);
    logo.add(rail);
  }

  /* ---------- hub ---------- */
  const hubRain = makeRain();
  const hubMat = screenMaterial(hubRain);
  hubMat.name = 'hub_screen';
  const hubGeo = new THREE.IcosahedronGeometry(0.145, 0);
  // per-face UVs so each facet shows its own slice of the cascade
  {
    const pos = hubGeo.attributes.position;
    const uv: number[] = [];
    for (let i = 0; i < pos.count; i += 3) {
      const c = (i / 3) % 5;
      uv.push(c * 0.2, 0, (c + 1) * 0.2, 0, (c + 0.5) * 0.2, 1);
    }
    hubGeo.setAttribute('uv', new THREE.Float32BufferAttribute(uv, 2));
  }
  const hub = new THREE.Mesh(hubGeo, hubMat);
  hub.name = 'hub';
  logo.add(hub);
  const collar = new THREE.Mesh(new THREE.TorusGeometry(0.185, 0.02, 16, 64), M.edge);
  collar.name = 'hub_collar';
  logo.add(collar);

  /* ---------- spokes ---------- */
  for (let i = 0; i < n; i++) {
    const deg = start + sweep * (i / (n - 1));
    const t = THREE.MathUtils.degToRad(deg);
    const inner = 0.16, outer = R - CX / 2;
    const len = outer - inner, mid = inner + len / 2;
    const spoke = new THREE.Mesh(new THREE.CylinderGeometry(0.019, 0.019, len, 20, 1, true), spokeMat);
    spoke.name = 'spoke_' + (i + 1);
    spoke.position.set(Math.cos(t) * mid, Math.sin(t) * mid, 0);
    spoke.rotation.z = t - Math.PI / 2;
    logo.add(spoke);
  }

  const box = new THREE.Box3().setFromObject(logo);
  logo.position.y -= box.min.y;

  /* ---------- animation: rain, flow, spinning hub ---------- */
  // The cascade, the coolant flow, and the tumbling hub are the model's
  // life, but they are all decorative — under prefers-reduced-motion we
  // paint one frame of rain so the screens aren't blank, then hold still.
  let last = 0, acc = 0, raf = 0;
  const frame = (now: number) => {
    const dt = last ? (now - last) / 1000 : 0;
    last = now;
    acc += dt;
    if (acc > 0.09) {
      acc = 0;
      rains.forEach((r) => r.step());
    }
    spokeFlow.offset.y = (spokeFlow.offset.y - dt * 0.55) % 1;
    hub.rotation.y += dt * 0.9;
    hub.rotation.x += dt * 0.32;
    raf = requestAnimationFrame(frame);
  };
  if (animate) {
    raf = requestAnimationFrame(frame);
  } else {
    rains.forEach((r) => r.step());
  }

  return {
    object: logo,
    dispose() {
      cancelAnimationFrame(raf);
      logo.traverse((o: THREE_NS.Object3D) => {
        const mesh = o as THREE_NS.Mesh;
        if (mesh.isMesh) mesh.geometry.dispose();
      });
      disposables.forEach((d) => d.dispose());
    },
  };
}
