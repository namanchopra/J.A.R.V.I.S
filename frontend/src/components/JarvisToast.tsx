import { useState, useEffect, useRef } from 'react'
import { EventsOn } from '../../wailsjs/runtime/runtime'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface Toast {
  id: number
  text: string
  exiting: boolean
}

// ---------------------------------------------------------------------------
// JarvisToastContainer
// ---------------------------------------------------------------------------

/**
 * Floating toast notifications for Jarvis spoken responses.
 *
 * Listens for `jarvis` Wails events of type `response` with role `jarvis`,
 * and shows the response text as a brief overlay that auto-dismisses after 6s.
 * Max 3 toasts stacked from the bottom-right corner.
 */
export function JarvisToastContainer(): React.ReactElement {
  const [toasts, setToasts] = useState<Toast[]>([])
  const nextId = useRef(0)

  useEffect(() => {
    const cancel = EventsOn('jarvis', (event: unknown) => {
      const evt = event as {
        type?: string
        text?: string
        role?: string
      } | undefined

      // Show toast for Jarvis responses (not transcripts, not state changes)
      if (evt?.type === 'response' && evt?.text && evt?.role === 'jarvis') {
        const text = evt.text
        // Skip very short responses (greetings, acknowledgments)
        if (text.length < 20) return

        const id = nextId.current++
        setToasts((prev) => {
          // Keep max 3 toasts
          const updated = [...prev, { id, text, exiting: false }]
          return updated.slice(-3)
        })

        // Auto-dismiss after 6 seconds
        setTimeout(() => {
          setToasts((prev) =>
            prev.map((t) => (t.id === id ? { ...t, exiting: true } : t)),
          )
          // Remove from DOM after fade-out animation completes
          setTimeout(() => {
            setToasts((prev) => prev.filter((t) => t.id !== id))
          }, 300)
        }, 6000)
      }
    })
    return () => {
      cancel()
    }
  }, [])

  if (toasts.length === 0) return <></>

  return (
    <div className="fixed bottom-6 right-6 z-50 flex flex-col gap-2 max-w-md">
      {toasts.map((toast) => (
        <div
          key={toast.id}
          className={`holo-panel px-4 py-3 text-sm text-[#e8f4ff] ${
            toast.exiting ? 'fade-out' : 'fade-in-up'
          }`}
        >
          <div className="flex items-start gap-2">
            <span className="text-[#00e5ff] text-xs font-mono mt-0.5 shrink-0">
              JARVIS
            </span>
            <p className="leading-relaxed m-0">
              {toast.text.length > 200
                ? toast.text.slice(0, 200) + '...'
                : toast.text}
            </p>
          </div>
        </div>
      ))}
    </div>
  )
}
