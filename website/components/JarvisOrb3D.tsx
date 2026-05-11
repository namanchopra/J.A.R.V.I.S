'use client'

import { Suspense, useMemo, useRef } from 'react'
import { Canvas, useFrame } from '@react-three/fiber'
import {
  Float,
  MeshDistortMaterial,
  Sphere,
  Points,
  PointMaterial,
} from '@react-three/drei'
import * as THREE from 'three'

// ---------------------------------------------------------------------------
// Wireframe rings orbiting the core. Three rings tilted on different axes,
// each rotating at a different rate.
// ---------------------------------------------------------------------------
function Rings() {
  const ringRefs = [useRef<THREE.Mesh>(null), useRef<THREE.Mesh>(null), useRef<THREE.Mesh>(null)]

  useFrame((state) => {
    const t = state.clock.elapsedTime
    if (ringRefs[0].current) {
      ringRefs[0].current.rotation.x = t * 0.25
      ringRefs[0].current.rotation.y = t * 0.18
    }
    if (ringRefs[1].current) {
      ringRefs[1].current.rotation.x = -t * 0.20
      ringRefs[1].current.rotation.z = t * 0.30
    }
    if (ringRefs[2].current) {
      ringRefs[2].current.rotation.y = -t * 0.35
      ringRefs[2].current.rotation.z = -t * 0.15
    }
  })

  // Three torus rings at increasing radius. Slightly translucent so they
  // overlap visually without harsh edges.
  const ringSpecs: Array<[number, number, [number, number, number], number]> = [
    [1.6, 0.005, [Math.PI / 2.4, 0, 0], 0.7],
    [2.1, 0.005, [0, Math.PI / 3, Math.PI / 6], 0.45],
    [2.7, 0.004, [Math.PI / 2, Math.PI / 4, 0], 0.25],
  ]

  return (
    <>
      {ringSpecs.map(([radius, thickness, rot, opacity], i) => {
        const ref = ringRefs[i]
        return (
          <mesh key={i} ref={ref} rotation={rot as [number, number, number]}>
            <torusGeometry args={[radius, thickness, 8, 128]} />
            <meshBasicMaterial
              color="#22d3ee"
              transparent
              opacity={opacity}
              toneMapped={false}
            />
          </mesh>
        )
      })}
    </>
  )
}

// ---------------------------------------------------------------------------
// Particle field around the orb — slowly rotating cyan dust cloud.
// ---------------------------------------------------------------------------
function ParticleField({ count = 800 }: { count?: number }) {
  const positions = useMemo(() => {
    const arr = new Float32Array(count * 3)
    for (let i = 0; i < count; i++) {
      // Spherical shell around the orb with some thickness
      const r = 3.2 + Math.random() * 2.5
      const theta = Math.random() * Math.PI * 2
      const phi = Math.acos(2 * Math.random() - 1)
      arr[i * 3 + 0] = r * Math.sin(phi) * Math.cos(theta)
      arr[i * 3 + 1] = r * Math.sin(phi) * Math.sin(theta)
      arr[i * 3 + 2] = r * Math.cos(phi)
    }
    return arr
  }, [count])

  const pointsRef = useRef<THREE.Points>(null)
  useFrame((state) => {
    if (pointsRef.current) {
      pointsRef.current.rotation.y = state.clock.elapsedTime * 0.04
      pointsRef.current.rotation.x = state.clock.elapsedTime * 0.02
    }
  })

  return (
    <Points ref={pointsRef} positions={positions} stride={3} frustumCulled={false}>
      <PointMaterial
        transparent
        color="#22d3ee"
        size={0.018}
        sizeAttenuation
        depthWrite={false}
        opacity={0.7}
      />
    </Points>
  )
}

// ---------------------------------------------------------------------------
// Core orb. Distorted sphere with cyan emission + slow pulse.
// ---------------------------------------------------------------------------
function Core() {
  const meshRef = useRef<THREE.Mesh>(null)
  useFrame((state) => {
    if (meshRef.current) {
      const t = state.clock.elapsedTime
      const pulse = 1 + Math.sin(t * 1.4) * 0.04
      meshRef.current.scale.setScalar(pulse)
    }
  })

  return (
    <Float speed={1.6} rotationIntensity={0.6} floatIntensity={0.4}>
      <Sphere ref={meshRef} args={[1, 96, 96]}>
        <MeshDistortMaterial
          color="#06b6d4"
          emissive="#06b6d4"
          emissiveIntensity={0.6}
          distort={0.32}
          speed={1.8}
          roughness={0.4}
          metalness={0.55}
        />
      </Sphere>

      {/* Inner core highlight */}
      <Sphere args={[0.4, 32, 32]}>
        <meshBasicMaterial
          color="#a5f3fc"
          transparent
          opacity={0.35}
          toneMapped={false}
        />
      </Sphere>
    </Float>
  )
}

// ---------------------------------------------------------------------------
// Public component.
// ---------------------------------------------------------------------------
export default function JarvisOrb3D() {
  return (
    <div className="relative h-full w-full">
      <Canvas
        camera={{ position: [0, 0, 6], fov: 50 }}
        dpr={[1, 2]}
        gl={{ antialias: true, alpha: true }}
      >
        <Suspense fallback={null}>
          {/* Ambient + directional light so MeshDistortMaterial doesn't go
              entirely flat. Bias the directional toward camera-front so the
              cyan highlights catch the orb's near hemisphere. */}
          <ambientLight intensity={0.4} />
          <directionalLight position={[3, 3, 5]} intensity={0.8} color="#22d3ee" />
          <pointLight position={[-3, -2, -2]} intensity={1.2} color="#06b6d4" />

          <Core />
          <Rings />
          <ParticleField />
        </Suspense>
      </Canvas>

      {/* Radial vignette to fade canvas edges into the page background. */}
      <div
        className="pointer-events-none absolute inset-0"
        style={{
          background:
            'radial-gradient(circle at center, transparent 50%, var(--color-jarvis-bg) 95%)',
        }}
      />
    </div>
  )
}
