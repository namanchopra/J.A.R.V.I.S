import { useEffect, useState } from 'react'

/**
 * Hook that returns a human-readable elapsed duration string,
 * updating every second.
 *
 * @param startedAtMs - epoch milliseconds when the timer started
 * @returns formatted duration string (e.g. "12s", "3m 12s", "1h 5m")
 */
export function useDuration(startedAtMs: number): string {
  const [, setTick] = useState(0)
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 1000)
    return () => clearInterval(id)
  }, [])

  const secs = Math.floor((Date.now() - startedAtMs) / 1000)
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${secs % 60}s`
  return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`
}
