'use client'

import dynamic from 'next/dynamic'

// react-three-fiber is client-only. Skip SSR so Next doesn't try to render
// the canvas on the server (Three.js touches `window` at import time).
const JarvisOrb3D = dynamic(() => import('./JarvisOrb3D'), {
  ssr: false,
  loading: () => <div className="h-full w-full" />,
})

export default function OrbClient() {
  return <JarvisOrb3D />
}
