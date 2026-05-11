/**
 * Augment JSX intrinsic elements with React Three Fiber's Three.js element types.
 *
 * R3F v8 augments the global `JSX.IntrinsicElements`, but when using newer
 * @types/react (v19+) the canonical location is `React.JSX.IntrinsicElements`.
 * This bridge file ensures TypeScript recognises <mesh>, <pointLight>, etc.
 * inside R3F's <Canvas> tree.
 */
import type { ThreeElements } from '@react-three/fiber'

declare module 'react' {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace JSX {
    interface IntrinsicElements extends ThreeElements {}
  }
}
