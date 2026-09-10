/**
 * The landing page's 3D stage: renderer, studio lighting, orbit controls,
 * and a camera auto-framed to the model's bounds.
 *
 * Adapted from the `<three-d-stage>` web component exported by Claude
 * Design (kept verbatim in docs/logo-design/ as the design source of
 * record). Two deliberate departures from that export:
 *
 *   - three.js is imported as a bundled dependency rather than through
 *     the export's pinned unpkg import map, so the landing page renders
 *     with no network dependency at page load.
 *   - the OBJ/GLB export toolbar is dropped. It exists to get a model
 *     out of the design tool; on a landing page it is a stray pair of
 *     download buttons over the hero.
 *
 * The lighting, shadow, and framing maths are the export's own — they are
 * what make the model read correctly, so they are reproduced rather than
 * reinvented.
 */
import * as THREE from 'three';
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js';

export interface Stage {
  /** Show the object, rest it on the ground, and frame the camera to it. */
  setObject(object: THREE.Object3D): void;
  /** Stop rendering and release the WebGL context. */
  dispose(): void;
  THREE: typeof THREE;
}

export function createStage(
  host: HTMLElement,
  background: string,
  autorotate = true,
): Stage {
  const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = THREE.PCFSoftShadowMap;
  renderer.domElement.style.display = 'block';
  host.appendChild(renderer.domElement);

  const scene = new THREE.Scene();
  scene.background = new THREE.Color(background);

  const camera = new THREE.PerspectiveCamera(45, 1, 0.01, 500);
  camera.position.set(3, 2.2, 4);

  const controls = new OrbitControls(camera, renderer.domElement);
  controls.enableDamping = true;
  controls.dampingFactor = 0.08;
  controls.autoRotate = autorotate;
  controls.autoRotateSpeed = 1.2;
  // The turntable is an invitation, not a ride: the first drag hands
  // control to the reader and never takes it back.
  controls.addEventListener('start', () => {
    controls.autoRotate = false;
  });

  // Neutral studio: soft sky/ground wash, a shadow-casting key light,
  // and a dim fill from behind so silhouettes never go black.
  scene.add(new THREE.HemisphereLight(0xffffff, 0xd8d2c4, 1.0));
  const key = new THREE.DirectionalLight(0xffffff, 2.2);
  key.position.set(4, 7, 5);
  key.castShadow = true;
  key.shadow.mapSize.set(2048, 2048);
  key.shadow.bias = -0.0002;
  scene.add(key);
  const fill = new THREE.DirectionalLight(0xfff4e6, 0.5);
  fill.position.set(-5, 3, -4);
  scene.add(fill);

  const ground = new THREE.Mesh(
    new THREE.PlaneGeometry(200, 200),
    new THREE.ShadowMaterial({ opacity: 0.18 }),
  );
  ground.rotation.x = -Math.PI / 2;
  ground.receiveShadow = true;
  scene.add(ground);

  let object: THREE.Object3D | null = null;

  const fit = () => {
    const w = host.clientWidth || 1;
    const h = host.clientHeight || 1;
    renderer.setSize(w, h);
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
  };
  fit();
  const ro = new ResizeObserver(fit);
  ro.observe(host);

  renderer.setAnimationLoop(() => {
    controls.update();
    renderer.render(scene, camera);
  });

  return {
    THREE,

    setObject(next) {
      if (object) scene.remove(object);
      object = next;
      next.traverse((o: THREE.Object3D) => {
        if ((o as THREE.Mesh).isMesh) {
          o.castShadow = true;
          o.receiveShadow = true;
        }
      });
      const box = new THREE.Box3().setFromObject(next);
      if (!box.isEmpty()) {
        // Rest the object on the ground without moving its origin.
        ground.position.y = box.min.y;
        const sphere = box.getBoundingSphere(new THREE.Sphere());
        const dist =
          (sphere.radius / Math.tan((camera.fov * Math.PI) / 360)) * 1.35;
        const dir = new THREE.Vector3(1, 0.55, 1.25).normalize();
        camera.position.copy(sphere.center).add(dir.multiplyScalar(dist));
        camera.near = Math.max(dist / 100, 0.01);
        camera.far = dist * 100;
        camera.updateProjectionMatrix();
        controls.target.copy(sphere.center);
        controls.update();
        const span = sphere.radius * 3;
        key.shadow.camera.left = -span;
        key.shadow.camera.right = span;
        key.shadow.camera.top = span;
        key.shadow.camera.bottom = -span;
        key.shadow.camera.updateProjectionMatrix();
      }
      scene.add(next);
    },

    dispose() {
      renderer.setAnimationLoop(null);
      ro.disconnect();
      controls.dispose();
      renderer.dispose();
      renderer.domElement.remove();
    },
  };
}
