// ---------------------------------------------------------------------------
// TranscriptView smoke tests -- TASK-024 acceptance criteria.
// ---------------------------------------------------------------------------
// Coverage:
//   1. Component renders empty without crashing when no events have fired
//   2. Emitting a `transcript` event via the mock WS adds a bubble
//   3. The 7th event evicts the oldest (caps at maxTurns)
//   4. User vs assistant bubbles get the correct alignment style
//
// MockJarvisWS is a stub that only implements `.on('transcript', listener)`
// and exposes `.emitTranscript(payload)` so tests can drive the component
// like the real JarvisWS would. The component is typed against a structural
// `TranscriptWSLike` (see TranscriptView.tsx) precisely so this kind of
// minimal stub satisfies the prop type without `any`.
//
// Reanimated mock is required because `FadeIn` / `FadeOut` would otherwise
// try to spin up the UI thread. Pattern mirrors OrbView.test.tsx exactly.
// ---------------------------------------------------------------------------

import { act, render } from '@testing-library/react-native'

// Reanimated's official jest mock -- same pattern OrbView.test.tsx uses.
// Without this, the FadeIn/FadeOut animation primitives try to access the
// worklets bridge which doesn't exist in node.
jest.mock('react-native-reanimated', () =>
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  require('react-native-reanimated/mock'),
)

// Import AFTER the mock so the component picks up the stubbed Reanimated.
// eslint-disable-next-line import/first
import { TranscriptView, type TranscriptWSLike } from './TranscriptView'

// ---- MockJarvisWS --------------------------------------------------------
// Stub class implementing just enough of `JarvisWS` for this component:
//   - `.on('transcript', listener)` registers a callback + returns a disposer
//   - `.emitTranscript(payload)` synchronously invokes every registered
//     `transcript` listener with the payload
//
// We intentionally don't simulate any other events -- the component only
// subscribes to 'transcript', so adding support for 'state' / 'error' here
// would be dead code. If TranscriptView ever starts listening to more
// events, extending this mock is a 4-line change.

type TranscriptListener = (payload: {
  role: 'user' | 'assistant'
  text: string
}) => void

class MockJarvisWS implements TranscriptWSLike {
  private transcriptListeners: Set<TranscriptListener> = new Set()

  on: TranscriptWSLike['on'] = ((event, listener) => {
    // Narrow to 'transcript' -- any other event subscription returns a
    // no-op disposer so the component code doesn't crash even if it
    // accidentally subscribes to something we don't simulate.
    if (event === 'transcript') {
      const fn = listener as TranscriptListener
      this.transcriptListeners.add(fn)
      return () => {
        this.transcriptListeners.delete(fn)
      }
    }
    return () => undefined
  }) as TranscriptWSLike['on']

  /** Synchronously fan out a transcript payload to all subscribers. */
  emitTranscript(payload: { role: 'user' | 'assistant'; text: string }): void {
    for (const fn of this.transcriptListeners) {
      fn(payload)
    }
  }
}

// ---- Tests ----------------------------------------------------------------

describe('TranscriptView', () => {
  it('renders empty without crashing when no events have fired', () => {
    const ws = new MockJarvisWS()
    const { getByTestId, queryByTestId } = render(<TranscriptView ws={ws} />)
    // Container ScrollView is always mounted; no bubbles yet.
    expect(getByTestId('transcript-view')).toBeTruthy()
    expect(queryByTestId('transcript-bubble-user')).toBeNull()
    expect(queryByTestId('transcript-bubble-assistant')).toBeNull()
  })

  it('adds a bubble when a transcript event is emitted', () => {
    const ws = new MockJarvisWS()
    const { getByTestId, queryAllByTestId } = render(
      <TranscriptView ws={ws} />,
    )

    act(() => {
      ws.emitTranscript({ role: 'user', text: 'hello jarvis' })
    })

    expect(getByTestId('transcript-bubble-user')).toBeTruthy()
    // Exactly one bubble after a single emission -- guards against a
    // regression where the listener double-registers (a common React
    // strict-mode foot-gun).
    expect(queryAllByTestId('transcript-bubble-user')).toHaveLength(1)
    // Bubble contains two Text leaves (role prefix + body); use a regex so
    // the matcher does substring matching against the flattened text rather
    // than exact equality (which would require us to know whether the role
    // prefix concatenates with or without a separator).
    expect(getByTestId('transcript-bubble-user')).toHaveTextContent(
      /hello jarvis/,
    )
    // Role label is the `YOU::` prefix specific to the user side.
    expect(getByTestId('transcript-bubble-user')).toHaveTextContent(/YOU::/)
  })

  it('caps at maxTurns: the 7th event evicts the oldest', () => {
    const ws = new MockJarvisWS()
    const { queryAllByTestId, queryAllByText } = render(
      <TranscriptView ws={ws} maxTurns={6} />,
    )

    // Emit 7 distinct events. We alternate roles so we can verify which
    // bubble got evicted purely from the rendered text content.
    act(() => {
      for (let i = 1; i <= 7; i++) {
        ws.emitTranscript({
          role: i % 2 === 1 ? 'user' : 'assistant',
          text: `turn-${i}`,
        })
      }
    })

    // Total bubble count stays at 6 (maxTurns). Sum across both
    // role buckets -- the maxTurns cap is global, not per-role.
    const allBubbles = [
      ...queryAllByTestId('transcript-bubble-user'),
      ...queryAllByTestId('transcript-bubble-assistant'),
    ]
    expect(allBubbles).toHaveLength(6)

    // turn-1 must be evicted (oldest, FIFO).
    expect(queryAllByText('turn-1')).toHaveLength(0)

    // turn-2 .. turn-7 must each still be mounted exactly once. Using
    // `queryAllByText` is the most direct way to assert a Text leaf with
    // the given content exists -- it walks the rendered tree and matches
    // exact strings (no regex), which is what we want for the body text.
    for (let i = 2; i <= 7; i++) {
      expect(queryAllByText(`turn-${i}`)).toHaveLength(1)
    }
  })

  it('user vs assistant bubbles get the correct alignment style', () => {
    const ws = new MockJarvisWS()
    const { getByTestId } = render(<TranscriptView ws={ws} />)

    act(() => {
      ws.emitTranscript({ role: 'user', text: 'hey' })
      ws.emitTranscript({ role: 'assistant', text: 'yes sir' })
    })

    const userBubble = getByTestId('transcript-bubble-user')
    const assistantBubble = getByTestId('transcript-bubble-assistant')

    // RN's StyleSheet.create yields numeric IDs at runtime, so the prop
    // `style` is an array like [baseStyle, alignmentStyle]. We normalise to
    // a flat object via recursive flattening so the alignSelf check works
    // regardless of how RN packs the array.
    const flattenStyle = (style: unknown): Record<string, unknown> => {
      if (Array.isArray(style)) {
        return style.reduce<Record<string, unknown>>((acc, s) => {
          return { ...acc, ...(flattenStyle(s) as Record<string, unknown>) }
        }, {})
      }
      if (style && typeof style === 'object') {
        return style as Record<string, unknown>
      }
      return {}
    }

    expect(flattenStyle(userBubble.props.style).alignSelf).toBe('flex-end')
    expect(flattenStyle(assistantBubble.props.style).alignSelf).toBe(
      'flex-start',
    )
  })
})
