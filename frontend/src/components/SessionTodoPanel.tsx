import { useCallback, useEffect, useRef, useState } from 'react'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface SessionTodo {
  id: string
  sessionId: string
  title: string
  status: string // "pending" | "done"
  sortOrder: number
  createdAt: string
}

// ---------------------------------------------------------------------------
// Wails binding helpers
// ---------------------------------------------------------------------------

function getAppBinding<T>(name: string): T | undefined {
  const w = window as unknown as Record<string, unknown>
  const goNs = w?.go as Record<string, unknown> | undefined
  const appObj = (goNs?.main as Record<string, unknown>)?.App as
    | Record<string, unknown>
    | undefined
  return appObj?.[name] as T | undefined
}

async function fetchTodos(sessionId: string): Promise<SessionTodo[]> {
  const fn = getAppBinding<(id: string) => Promise<SessionTodo[]>>(
    'GetSessionTodos',
  )
  if (!fn) return []
  try {
    const result = await fn(sessionId)
    return result ?? []
  } catch (err) {
    console.warn('GetSessionTodos failed:', err)
    return []
  }
}

async function createTodo(
  sessionId: string,
  title: string,
): Promise<SessionTodo | null> {
  const fn = getAppBinding<
    (sid: string, t: string) => Promise<SessionTodo>
  >('CreateSessionTodo')
  if (!fn) return null
  try {
    return await fn(sessionId, title)
  } catch (err) {
    console.warn('CreateSessionTodo failed:', err)
    return null
  }
}

async function updateTodo(id: string, status: string): Promise<void> {
  const fn = getAppBinding<(id: string, s: string) => Promise<void>>(
    'UpdateSessionTodo',
  )
  if (!fn) return
  try {
    await fn(id, status)
  } catch (err) {
    console.warn('UpdateSessionTodo failed:', err)
  }
}

async function deleteTodo(id: string): Promise<void> {
  const fn = getAppBinding<(id: string) => Promise<void>>(
    'DeleteSessionTodo',
  )
  if (!fn) return
  try {
    await fn(id)
  } catch (err) {
    console.warn('DeleteSessionTodo failed:', err)
  }
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const POLL_INTERVAL_MS = 5_000

// ---------------------------------------------------------------------------
// SessionTodoPanel
// ---------------------------------------------------------------------------

export function SessionTodoPanel({
  sessionId,
}: {
  sessionId: string
}): React.ReactElement {
  const [todos, setTodos] = useState<SessionTodo[]>([])
  const [inputValue, setInputValue] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  // ---- Fetch todos ----
  const refresh = useCallback(async (): Promise<void> => {
    const items = await fetchTodos(sessionId)
    setTodos(items)
  }, [sessionId])

  // Initial fetch + polling every 5s
  useEffect(() => {
    let cancelled = false

    async function poll(): Promise<void> {
      const items = await fetchTodos(sessionId)
      if (!cancelled) setTodos(items)
    }

    void poll()
    const id = setInterval(() => void poll(), POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [sessionId])

  // ---- Toggle status ----
  const handleToggle = useCallback(
    async (todo: SessionTodo): Promise<void> => {
      const nextStatus = todo.status === 'done' ? 'pending' : 'done'
      // Optimistic update
      setTodos((prev) =>
        prev.map((t) =>
          t.id === todo.id ? { ...t, status: nextStatus } : t,
        ),
      )
      await updateTodo(todo.id, nextStatus)
      await refresh()
    },
    [refresh],
  )

  // ---- Delete ----
  const handleDelete = useCallback(
    async (id: string): Promise<void> => {
      // Optimistic removal
      setTodos((prev) => prev.filter((t) => t.id !== id))
      await deleteTodo(id)
      await refresh()
    },
    [refresh],
  )

  // ---- Add ----
  const handleAdd = useCallback(
    async (e: React.FormEvent): Promise<void> => {
      e.preventDefault()
      const title = inputValue.trim()
      if (!title || isSubmitting) return

      setIsSubmitting(true)
      const created = await createTodo(sessionId, title)
      if (created) {
        setInputValue('')
        await refresh()
      }
      setIsSubmitting(false)
      inputRef.current?.focus()
    },
    [inputValue, isSubmitting, sessionId, refresh],
  )

  // ---- Render ----
  const pendingTodos = todos.filter((t) => t.status !== 'done')
  const doneTodos = todos.filter((t) => t.status === 'done')
  const sorted = [...pendingTodos, ...doneTodos]

  return (
    <div className="border-b border-border">
      {/* Header */}
      <div className="px-4 py-2 flex items-center justify-between">
        <h3 className="text-[11px] text-muted uppercase tracking-wider font-semibold">
          Todos
          {todos.length > 0 && (
            <span className="ml-1.5 text-secondary font-normal">
              {pendingTodos.length}/{todos.length}
            </span>
          )}
        </h3>
      </div>

      {/* List */}
      <div className="px-4 pb-2">
        {sorted.length === 0 && (
          <p className="text-xs text-muted py-1">No todos yet</p>
        )}

        {sorted.map((todo) => {
          const isDone = todo.status === 'done'
          return (
            <div
              key={todo.id}
              className="group flex items-center gap-2 py-1 rounded hover:bg-surface/50 transition-colors -mx-1 px-1"
            >
              {/* Checkbox */}
              <button
                type="button"
                onClick={() => void handleToggle(todo)}
                className={`w-3.5 h-3.5 rounded-sm border flex-shrink-0 flex items-center justify-center transition-colors ${
                  isDone
                    ? 'bg-acc-teal/80 border-acc-teal/60'
                    : 'border-border hover:border-secondary'
                }`}
                aria-label={isDone ? 'Mark as pending' : 'Mark as done'}
              >
                {isDone && (
                  <svg
                    className="w-2.5 h-2.5 text-white"
                    viewBox="0 0 12 12"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <path d="M2 6l3 3 5-5" />
                  </svg>
                )}
              </button>

              {/* Title */}
              <span
                className={`text-xs flex-1 min-w-0 truncate transition-colors ${
                  isDone ? 'line-through text-muted' : 'text-primary'
                }`}
              >
                {todo.title}
              </span>

              {/* Delete button */}
              <button
                type="button"
                onClick={() => void handleDelete(todo.id)}
                className="opacity-0 group-hover:opacity-100 text-muted hover:text-red-400 transition-all flex-shrink-0"
                aria-label={`Delete todo: ${todo.title}`}
              >
                <svg
                  className="w-3 h-3"
                  viewBox="0 0 12 12"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                >
                  <path d="M3 3l6 6M9 3l-6 6" />
                </svg>
              </button>
            </div>
          )
        })}

        {/* Add input */}
        <form onSubmit={(e) => void handleAdd(e)} className="mt-1">
          <input
            ref={inputRef}
            type="text"
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            placeholder="Add a todo..."
            disabled={isSubmitting}
            className="w-full text-xs bg-transparent text-primary placeholder:text-muted/60 outline-none py-1 border-b border-transparent focus:border-border transition-colors disabled:opacity-50"
          />
        </form>
      </div>
    </div>
  )
}
