// ---------------------------------------------------------------------------
// HUD Animations -- hooks for triggering visual effects on data changes
// ---------------------------------------------------------------------------

import { useEffect, useRef, useState } from 'react'

/**
 * Returns `'hud-flash'` for 600ms whenever `trigger` changes, then `''`.
 *
 * Use this to apply a one-shot glow animation on a panel container
 * whenever its backing data changes.
 *
 * @param trigger - any value; a change (by reference / JSON length) fires the flash
 * @returns CSS class name string: `'hud-flash'` during flash, `''` otherwise
 */
export function useFlash(trigger: unknown): string {
  const [flash, setFlash] = useState('')
  const prevRef = useRef<unknown>(trigger)
  const firstRender = useRef(true)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    // Skip the initial mount -- only flash on subsequent changes
    if (firstRender.current) {
      firstRender.current = false
      prevRef.current = trigger
      return
    }

    // Only flash when the trigger actually changed
    if (trigger === prevRef.current) return
    prevRef.current = trigger

    // Clear any pending timer so overlapping changes restart the 600ms
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current)
    }

    setFlash('hud-flash')
    timerRef.current = setTimeout(() => {
      setFlash('')
      timerRef.current = null
    }, 600)

    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
    }
  }, [trigger])

  return flash
}
