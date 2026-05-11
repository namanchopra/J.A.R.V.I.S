// ---------------------------------------------------------------------------
// Shared Jarvis API — Wails binding wrappers
//
// The Go backend exposes these on `window.go.main.App`. We call through
// the runtime bridge so components work once the bindings are generated.
// Until then, each wrapper returns a sensible fallback.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type JarvisState = 'idle' | 'listening' | 'thinking' | 'speaking'

export interface JarvisMessage {
  role: string
  content: string
  timestamp: string | number
}

// ---------------------------------------------------------------------------
// Augment Window for the Wails runtime bridge
// ---------------------------------------------------------------------------

declare global {
  interface Window {
    go?: {
      main?: {
        App?: Record<string, unknown>
      }
    }
  }
}

// ---------------------------------------------------------------------------
// Binding wrappers
// ---------------------------------------------------------------------------

export async function getJarvisState(): Promise<JarvisState> {
  try {
    const fn = window?.go?.main?.App?.GetJarvisState as
      | (() => Promise<string>)
      | undefined
    if (fn) {
      const s = await fn()
      if (s === 'listening' || s === 'thinking' || s === 'speaking') return s
      return 'idle'
    }
  } catch {
    // binding not available yet
  }
  return 'idle'
}

export async function sendJarvisMessage(text: string): Promise<string> {
  try {
    const fn = window?.go?.main?.App?.SendJarvisMessage as
      | ((t: string) => Promise<string>)
      | undefined
    if (fn) return await fn(text)
  } catch {
    // binding not available yet
  }
  return '(Jarvis is not connected yet)'
}

export async function getJarvisHistory(): Promise<JarvisMessage[]> {
  try {
    const fn = window?.go?.main?.App?.GetJarvisHistory as
      | (() => Promise<JarvisMessage[]>)
      | undefined
    if (fn) return await fn()
  } catch {
    // binding not available yet
  }
  return []
}

export async function sendJarvisCommand(command: string): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.SendJarvisCommand as
      | ((t: string) => Promise<void>)
      | undefined
    if (fn) await fn(command)
  } catch {
    // binding not available yet
  }
}

export async function startJarvis(): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.StartJarvis as
      | (() => Promise<void>)
      | undefined
    if (fn) await fn()
  } catch {
    // binding not available yet
  }
}
