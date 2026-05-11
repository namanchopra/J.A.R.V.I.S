import { useRef, useMemo, useEffect } from 'react'
import { Canvas, useFrame } from '@react-three/fiber'
import * as THREE from 'three'
import {
  outerVertexShader,
  outerFragmentShader,
  innerVertexShader,
  innerFragmentShader,
  coreVertexShader,
  coreFragmentShader,
} from './JarvisOrbShaders'
import { EventsOn } from '../../wailsjs/runtime/runtime'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type JarvisState = 'idle' | 'listening' | 'thinking' | 'speaking'

interface JarvisOrbProps {
  /** Current Jarvis state -- drives orb visuals (color, displacement, animation). */
  state: JarvisState
  /** Audio level 0-1 -- drives displacement amplitude in listening/speaking modes. */
  audioLevel: number
  /** Additional CSS classes for the container div. */
  className?: string
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/** Maps JarvisState name to the integer uState uniform value. */
const STATE_INDEX: Record<JarvisState, number> = {
  idle: 0,
  listening: 1,
  thinking: 2,
  speaking: 3,
}

/** Duration (seconds) over which the transition uniform lerps 0 -> 1. */
const TRANSITION_DURATION = 0.3

/** Duration (ms) after which, if no audio_level events arrive, the level decays to the prop fallback. */
const AUDIO_EVENT_TIMEOUT_MS = 500

/** Interpolation speed for audio level changes (higher = faster response). */
const AUDIO_LERP_SPEED = 12

/** Auto-rotation base speed (radians per second). */
const ROTATION_SPEED = 0.1

// ---------------------------------------------------------------------------
// Inner scene component -- icosahedron orb
// ---------------------------------------------------------------------------

interface OrbSceneProps {
  state: JarvisState
  audioLevel: number
}

function OrbScene({ state, audioLevel }: OrbSceneProps): React.ReactElement {
  const outerMeshRef = useRef<THREE.Mesh>(null)
  const innerMeshRef = useRef<THREE.Mesh>(null)
  const coreMeshRef = useRef<THREE.Mesh>(null)
  const groupRef = useRef<THREE.Group>(null)

  // --- Uniforms for outer wireframe shell ---
  const outerUniforms = useMemo(() => {
    const stateIdx = STATE_INDEX[state]
    return {
      uTime: { value: 0 },
      uState: { value: stateIdx },
      uAudioLevel: { value: audioLevel },
      uDistortion: { value: 0.3 },
      uTransition: { value: 1 },
      uPrevState: { value: stateIdx },
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []) // Intentionally empty -- we mutate .value in useFrame / useEffect

  // --- Uniforms for inner glow sphere ---
  const innerUniforms = useMemo(() => {
    const stateIdx = STATE_INDEX[state]
    return {
      uTime: { value: 0 },
      uState: { value: stateIdx },
      uAudioLevel: { value: audioLevel },
      uTransition: { value: 1 },
      uPrevState: { value: stateIdx },
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // --- Uniforms for core center glow ---
  const coreUniforms = useMemo(() => {
    const stateIdx = STATE_INDEX[state]
    return {
      uTime: { value: 0 },
      uState: { value: stateIdx },
      uAudioLevel: { value: audioLevel },
      uTransition: { value: 1 },
      uPrevState: { value: stateIdx },
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Dispose Three.js geometries and materials on unmount to prevent GPU leaks.
  useEffect(() => {
    return () => {
      outerMeshRef.current?.geometry.dispose();
      (outerMeshRef.current?.material as THREE.ShaderMaterial)?.dispose();
      innerMeshRef.current?.geometry.dispose();
      (innerMeshRef.current?.material as THREE.ShaderMaterial)?.dispose();
      coreMeshRef.current?.geometry.dispose();
      (coreMeshRef.current?.material as THREE.ShaderMaterial)?.dispose();
    }
  }, [])

  // Track the transition progress (starts fully transitioned).
  const transitionRef = useRef<number>(1)

  // --- Real-time audio level from Wails events ---
  const audioLevelRef = useRef<number>(0)
  const lastAudioEventRef = useRef<number>(0)
  const smoothedAudioRef = useRef<number>(0)

  // Subscribe to Wails "jarvis" events for audio_level data.
  useEffect(() => {
    const cancel = EventsOn('jarvis', (...args: unknown[]) => {
      const event = args[0] as { type?: string; level?: number } | undefined
      if (event?.type === 'audio_level' && typeof event.level === 'number') {
        audioLevelRef.current = event.level
        lastAudioEventRef.current = performance.now()
      }
    })
    return () => { cancel() }
  }, [])

  // When the state prop changes, start a new transition.
  useEffect(() => {
    const newIdx = STATE_INDEX[state]
    const currentIdx = outerUniforms.uState.value
    if (newIdx !== currentIdx) {
      // Set previous state for blending
      outerUniforms.uPrevState.value = currentIdx
      outerUniforms.uState.value = newIdx
      innerUniforms.uPrevState.value = currentIdx
      innerUniforms.uState.value = newIdx
      coreUniforms.uPrevState.value = currentIdx
      coreUniforms.uState.value = newIdx
      transitionRef.current = 0
    }
  }, [state, outerUniforms, innerUniforms, coreUniforms])

  useFrame((_rootState, delta) => {
    // Advance time
    const time = outerUniforms.uTime.value + delta
    outerUniforms.uTime.value = time
    innerUniforms.uTime.value = time
    coreUniforms.uTime.value = time

    // Advance transition toward 1
    if (transitionRef.current < 1) {
      transitionRef.current = Math.min(1, transitionRef.current + delta / TRANSITION_DURATION)
      outerUniforms.uTransition.value = transitionRef.current
      innerUniforms.uTransition.value = transitionRef.current
      coreUniforms.uTransition.value = transitionRef.current
    }

    // Determine the target audio level
    const timeSinceLastEvent = performance.now() - lastAudioEventRef.current
    const hasRecentEvents = lastAudioEventRef.current > 0 && timeSinceLastEvent < AUDIO_EVENT_TIMEOUT_MS

    let targetLevel: number
    if (hasRecentEvents) {
      targetLevel = audioLevelRef.current
    } else if (lastAudioEventRef.current > 0 && timeSinceLastEvent < AUDIO_EVENT_TIMEOUT_MS * 2) {
      const decayProgress = (timeSinceLastEvent - AUDIO_EVENT_TIMEOUT_MS) / AUDIO_EVENT_TIMEOUT_MS
      targetLevel = audioLevelRef.current * (1 - decayProgress) + audioLevel * decayProgress
    } else {
      targetLevel = audioLevel
    }

    // Smooth interpolation toward the target each frame
    const lerpFactor = 1 - Math.exp(-AUDIO_LERP_SPEED * delta)
    smoothedAudioRef.current += (targetLevel - smoothedAudioRef.current) * lerpFactor

    // In idle state, add breathing so the orb visibly undulates
    const effectiveAudio =
      state === 'idle'
        ? Math.max(smoothedAudioRef.current, 0.1 + 0.05 * Math.sin(time * 0.8))
        : smoothedAudioRef.current

    outerUniforms.uAudioLevel.value = effectiveAudio
    innerUniforms.uAudioLevel.value = effectiveAudio
    coreUniforms.uAudioLevel.value = effectiveAudio

    // Auto-rotate -- speed increases when speaking or thinking
    if (groupRef.current) {
      const speed =
        state === 'speaking' ? 0.4
        : state === 'thinking' ? 0.3
        : state === 'listening' ? 0.15
        : ROTATION_SPEED
      groupRef.current.rotation.y += speed * delta
    }
  })

  return (
    <group ref={groupRef}>
      {/* Outer wireframe shell -- low-detail icosahedron for visible facets */}
      <mesh ref={outerMeshRef}>
        <icosahedronGeometry args={[1.8, 4]} />
        <shaderMaterial
          vertexShader={outerVertexShader}
          fragmentShader={outerFragmentShader}
          uniforms={outerUniforms}
          transparent
          wireframe
          depthWrite={false}
          side={THREE.DoubleSide}
        />
      </mesh>

      {/* Inner smooth glow -- high-detail icosahedron, solid, additive blending */}
      <mesh ref={innerMeshRef}>
        <icosahedronGeometry args={[1.2, 16]} />
        <shaderMaterial
          vertexShader={innerVertexShader}
          fragmentShader={innerFragmentShader}
          uniforms={innerUniforms}
          transparent
          depthWrite={false}
          blending={THREE.AdditiveBlending}
          side={THREE.FrontSide}
        />
      </mesh>

      {/* Core center glow sphere -- small bright center */}
      <mesh ref={coreMeshRef}>
        <sphereGeometry args={[0.35, 32, 32]} />
        <shaderMaterial
          vertexShader={coreVertexShader}
          fragmentShader={coreFragmentShader}
          uniforms={coreUniforms}
          transparent
          depthWrite={false}
          blending={THREE.AdditiveBlending}
          side={THREE.FrontSide}
        />
      </mesh>
    </group>
  )
}

// ---------------------------------------------------------------------------
// Public component
// ---------------------------------------------------------------------------

export function JarvisOrb({
  state,
  audioLevel,
  className,
}: JarvisOrbProps): React.ReactElement {
  return (
    <div className={className}>
      <Canvas
        frameloop="always"
        dpr={[1, 1.5]}
        gl={{
          antialias: true,
          alpha: true,
          powerPreference: 'default',
        }}
        camera={{ position: [0, 0, 5], fov: 45 }}
        style={{ background: 'transparent' }}
        onCreated={({ gl }) => {
          gl.setClearColor(0x000000, 0)
        }}
      >
        <OrbScene state={state} audioLevel={audioLevel} />
      </Canvas>
    </div>
  )
}
