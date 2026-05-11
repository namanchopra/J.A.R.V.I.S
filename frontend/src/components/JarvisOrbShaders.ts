// ---------------------------------------------------------------------------
// JarvisOrbShaders.ts -- GLSL shaders for the Jarvis audio visualizer orb
//
// Technique: Displaced IcosahedronGeometry with simplex noise.
// Audio level amplifies vertex displacement along normals, creating
// dramatic spiky organic shapes that pulse with voice amplitude.
//
// Four visual modes driven by uState:
//   0 = idle      -- gentle breathing, minimal spikes, electric cyan
//   1 = listening  -- slightly more active, bright cyan
//   2 = thinking   -- swirling, deeper cyan-blue
//   3 = speaking   -- dramatic spikes driven by audio, bright white-cyan
//
// WebGL 1 compatible (no #version directive, no texelFetch, etc.)
// Two-layer rendering: wireframe outer shell + solid inner glow.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Shared: Simplex noise (Ashima Arts -- public domain, WebGL 1 safe)
// Extracted as a GLSL string to include in both vertex shaders.
// ---------------------------------------------------------------------------

const simplexNoiseGLSL = /* glsl */ `
vec3 mod289(vec3 x) { return x - floor(x * (1.0 / 289.0)) * 289.0; }
vec4 mod289(vec4 x) { return x - floor(x * (1.0 / 289.0)) * 289.0; }
vec4 permute(vec4 x) { return mod289(((x * 34.0) + 1.0) * x); }
vec4 taylorInvSqrt(vec4 r) { return 1.79284291400159 - 0.85373472095314 * r; }

float snoise(vec3 v) {
  const vec2 C = vec2(1.0 / 6.0, 1.0 / 3.0);
  const vec4 D = vec4(0.0, 0.5, 1.0, 2.0);

  vec3 i  = floor(v + dot(v, C.yyy));
  vec3 x0 = v - i + dot(i, C.xxx);

  vec3 g = step(x0.yzx, x0.xyz);
  vec3 l = 1.0 - g;
  vec3 i1 = min(g.xyz, l.zxy);
  vec3 i2 = max(g.xyz, l.zxy);

  vec3 x1 = x0 - i1 + C.xxx;
  vec3 x2 = x0 - i2 + C.yyy;
  vec3 x3 = x0 - D.yyy;

  i = mod289(i);
  vec4 p = permute(permute(permute(
    i.z + vec4(0.0, i1.z, i2.z, 1.0))
  + i.y + vec4(0.0, i1.y, i2.y, 1.0))
  + i.x + vec4(0.0, i1.x, i2.x, 1.0));

  float n_ = 0.142857142857;
  vec3  ns = n_ * D.wyz - D.xzx;

  vec4 j = p - 49.0 * floor(p * ns.z * ns.z);

  vec4 x_ = floor(j * ns.z);
  vec4 y_ = floor(j - 7.0 * x_);

  vec4 x = x_ * ns.x + ns.yyyy;
  vec4 y = y_ * ns.x + ns.yyyy;
  vec4 h = 1.0 - abs(x) - abs(y);

  vec4 b0 = vec4(x.xy, y.xy);
  vec4 b1 = vec4(x.zw, y.zw);

  vec4 s0 = floor(b0) * 2.0 + 1.0;
  vec4 s1 = floor(b1) * 2.0 + 1.0;
  vec4 sh = -step(h, vec4(0.0));

  vec4 a0 = b0.xzyw + s0.xzyw * sh.xxyy;
  vec4 a1 = b1.xzyw + s1.xzyw * sh.zzww;

  vec3 p0 = vec3(a0.xy, h.x);
  vec3 p1 = vec3(a0.zw, h.y);
  vec3 p2 = vec3(a1.xy, h.z);
  vec3 p3 = vec3(a1.zw, h.w);

  vec4 norm = taylorInvSqrt(vec4(dot(p0,p0), dot(p1,p1), dot(p2,p2), dot(p3,p3)));
  p0 *= norm.x;
  p1 *= norm.y;
  p2 *= norm.z;
  p3 *= norm.w;

  vec4 m = max(0.6 - vec4(dot(x0,x0), dot(x1,x1), dot(x2,x2), dot(x3,x3)), 0.0);
  m = m * m;
  return 42.0 * dot(m * m, vec4(dot(p0,x0), dot(p1,x1), dot(p2,x2), dot(p3,x3)));
}
`

// ---------------------------------------------------------------------------
// Outer shell vertex shader (wireframe icosahedron with audio displacement)
// ---------------------------------------------------------------------------

export const outerVertexShader = /* glsl */ `
uniform float uTime;
uniform float uState;
uniform float uAudioLevel;
uniform float uDistortion;
uniform float uTransition;
uniform float uPrevState;

varying vec3 vNormal;
varying vec3 vWorldPosition;
varying float vDisplacement;

${simplexNoiseGLSL}

// State-dependent distortion amount
float distortionForState(float stateId) {
  float s0 = 1.0 - step(0.5, abs(stateId - 0.0)); // idle
  float s1 = 1.0 - step(0.5, abs(stateId - 1.0)); // listening
  float s2 = 1.0 - step(0.5, abs(stateId - 2.0)); // thinking
  float s3 = 1.0 - step(0.5, abs(stateId - 3.0)); // speaking

  return s0 * 0.15
       + s1 * 0.25
       + s2 * 0.35
       + s3 * 0.5;
}

void main() {
  vec3 pos = position;
  vec3 norm = normalize(normal);

  // Multi-octave noise for organic displacement
  float noise1 = snoise(pos * 0.5 + vec3(0.0, 0.0, uTime * 0.3));
  float noise2 = snoise(pos * 1.2 + vec3(uTime * 0.2, 0.0, 0.0)) * 0.5;
  float noise3 = snoise(pos * 2.5 + vec3(0.0, uTime * 0.15, 0.0)) * 0.25;
  float combinedNoise = noise1 + noise2 + noise3;

  // Audio amplifies the displacement: 1x when silent, up to 4x at max audio
  float audioBoost = 1.0 + uAudioLevel * 3.0;

  // Constant breathing animation
  float breathe = 0.8 + 0.2 * sin(uTime * 2.0);

  // Blend distortion between previous and current state
  float prevDistortion = distortionForState(uPrevState);
  float currDistortion = distortionForState(uState);
  float baseDistortion = mix(prevDistortion, currDistortion, uTransition);

  // Final displacement
  float distortion = baseDistortion * breathe * audioBoost;
  float displacement = combinedNoise * distortion;
  pos += norm * displacement;

  vDisplacement = displacement;
  vNormal = normalize(normalMatrix * norm);
  vec4 worldPos = modelMatrix * vec4(pos, 1.0);
  vWorldPosition = worldPos.xyz;

  gl_Position = projectionMatrix * viewMatrix * worldPos;
}
`

// ---------------------------------------------------------------------------
// Outer shell fragment shader (wireframe with fresnel glow)
// ---------------------------------------------------------------------------

export const outerFragmentShader = /* glsl */ `
uniform float uTime;
uniform float uState;
uniform float uAudioLevel;
uniform float uTransition;
uniform float uPrevState;

varying vec3 vNormal;
varying vec3 vWorldPosition;
varying float vDisplacement;

// Color palettes per state -- ALL CYAN, no green
vec3 colorForState(float stateId) {
  float s0 = 1.0 - step(0.5, abs(stateId - 0.0));
  float s1 = 1.0 - step(0.5, abs(stateId - 1.0));
  float s2 = 1.0 - step(0.5, abs(stateId - 2.0));
  float s3 = 1.0 - step(0.5, abs(stateId - 3.0));

  // idle: electric cyan #00e5ff
  // listening: bright cyan #00d4ff
  // thinking: deeper cyan-blue #0099ff
  // speaking: bright white-cyan #66f0ff
  return s0 * vec3(0.0, 0.898, 1.0)
       + s1 * vec3(0.0, 0.831, 1.0)
       + s2 * vec3(0.0, 0.6, 1.0)
       + s3 * vec3(0.4, 0.94, 1.0);
}

void main() {
  vec3 viewDir = normalize(cameraPosition - vWorldPosition);
  vec3 norm = normalize(vNormal);

  // Fresnel glow -- edges glow brighter
  float fresnel = 1.0 - max(dot(norm, viewDir), 0.0);
  fresnel = pow(fresnel, 2.0);

  // Base color blended between prev and current state
  vec3 prevColor = colorForState(uPrevState);
  vec3 currColor = colorForState(uState);
  vec3 baseColor = mix(prevColor, currColor, uTransition);

  // Audio makes it brighter/whiter
  vec3 color = mix(baseColor, vec3(1.0), uAudioLevel * 0.4);

  // Displacement brightness boost -- spikier areas glow brighter
  float dispBrightness = 1.0 + abs(vDisplacement) * 2.0;
  color *= dispBrightness;

  // Fresnel adds edge glow
  color += baseColor * fresnel * 0.6;

  // Alpha: base + fresnel + audio contribution
  float alpha = 0.7 + fresnel * 0.3 + uAudioLevel * 0.2;

  // Clamp to avoid over-saturation
  alpha = clamp(alpha, 0.0, 1.0);

  gl_FragColor = vec4(color, alpha);
}
`

// ---------------------------------------------------------------------------
// Inner glow vertex shader (smooth icosahedron with subtle displacement)
// ---------------------------------------------------------------------------

export const innerVertexShader = /* glsl */ `
uniform float uTime;
uniform float uState;
uniform float uAudioLevel;
uniform float uTransition;
uniform float uPrevState;

varying vec3 vNormal;
varying vec3 vWorldPosition;

${simplexNoiseGLSL}

void main() {
  vec3 pos = position;
  vec3 norm = normalize(normal);

  // Subtle breathing displacement -- much gentler than outer shell
  float noise = snoise(pos * 0.8 + vec3(0.0, 0.0, uTime * 0.2));
  float breathe = 0.9 + 0.1 * sin(uTime * 1.5);
  float audioInfluence = 1.0 + uAudioLevel * 1.0; // gentler audio response

  float displacement = noise * 0.08 * breathe * audioInfluence;
  pos += norm * displacement;

  vNormal = normalize(normalMatrix * norm);
  vec4 worldPos = modelMatrix * vec4(pos, 1.0);
  vWorldPosition = worldPos.xyz;

  gl_Position = projectionMatrix * viewMatrix * worldPos;
}
`

// ---------------------------------------------------------------------------
// Inner glow fragment shader (soft glowing core with fresnel)
// ---------------------------------------------------------------------------

export const innerFragmentShader = /* glsl */ `
uniform float uTime;
uniform float uState;
uniform float uAudioLevel;
uniform float uTransition;
uniform float uPrevState;

varying vec3 vNormal;
varying vec3 vWorldPosition;

vec3 innerColorForState(float stateId) {
  float s0 = 1.0 - step(0.5, abs(stateId - 0.0));
  float s1 = 1.0 - step(0.5, abs(stateId - 1.0));
  float s2 = 1.0 - step(0.5, abs(stateId - 2.0));
  float s3 = 1.0 - step(0.5, abs(stateId - 3.0));

  return s0 * vec3(0.0, 0.898, 1.0)
       + s1 * vec3(0.0, 0.831, 1.0)
       + s2 * vec3(0.0, 0.6, 1.0)
       + s3 * vec3(0.4, 0.94, 1.0);
}

void main() {
  vec3 viewDir = normalize(cameraPosition - vWorldPosition);
  vec3 norm = normalize(vNormal);

  // Fresnel for inner glow -- softer power
  float fresnel = 1.0 - max(dot(norm, viewDir), 0.0);
  fresnel = pow(fresnel, 1.5);

  // Color
  vec3 prevColor = innerColorForState(uPrevState);
  vec3 currColor = innerColorForState(uState);
  vec3 color = mix(prevColor, currColor, uTransition);

  // Audio makes the inner core brighter
  color = mix(color, vec3(1.0), uAudioLevel * 0.2);

  // Add fresnel rim
  color += color * fresnel * 0.4;

  // Pulsing alpha -- soft glow
  float pulse = 0.25 + 0.05 * sin(uTime * 1.5);
  float alpha = pulse + fresnel * 0.15 + uAudioLevel * 0.1;
  alpha = clamp(alpha, 0.0, 0.6);

  gl_FragColor = vec4(color, alpha);
}
`

// ---------------------------------------------------------------------------
// Core sphere shaders (small bright center glow)
// ---------------------------------------------------------------------------

export const coreVertexShader = /* glsl */ `
varying vec3 vNormal;
varying vec3 vWorldPosition;

void main() {
  vNormal = normalize(normalMatrix * normal);
  vec4 worldPos = modelMatrix * vec4(position, 1.0);
  vWorldPosition = worldPos.xyz;
  gl_Position = projectionMatrix * viewMatrix * worldPos;
}
`

export const coreFragmentShader = /* glsl */ `
uniform float uTime;
uniform float uState;
uniform float uTransition;
uniform float uPrevState;
uniform float uAudioLevel;

varying vec3 vNormal;
varying vec3 vWorldPosition;

vec3 coreColorForState(float stateId) {
  float s0 = 1.0 - step(0.5, abs(stateId - 0.0));
  float s1 = 1.0 - step(0.5, abs(stateId - 1.0));
  float s2 = 1.0 - step(0.5, abs(stateId - 2.0));
  float s3 = 1.0 - step(0.5, abs(stateId - 3.0));

  return s0 * vec3(0.0, 0.898, 1.0)
       + s1 * vec3(0.0, 0.831, 1.0)
       + s2 * vec3(0.0, 0.6, 1.0)
       + s3 * vec3(0.4, 0.94, 1.0);
}

void main() {
  vec3 viewDir = normalize(cameraPosition - vWorldPosition);
  float fresnel = 1.0 - max(dot(vNormal, viewDir), 0.0);
  fresnel = pow(fresnel, 2.5);

  vec3 prevColor = coreColorForState(uPrevState);
  vec3 currColor = coreColorForState(uState);
  vec3 color = mix(prevColor, currColor, uTransition);

  // Bright glowing center
  float pulse = 0.15 + 0.05 * sin(uTime * 2.0);
  float alpha = pulse + fresnel * 0.25 + uAudioLevel * 0.15;
  alpha = clamp(alpha, 0.0, 0.5);

  // White-ish center glow
  color = mix(color, vec3(1.0), 0.3 + uAudioLevel * 0.2);

  gl_FragColor = vec4(color, alpha);
}
`
