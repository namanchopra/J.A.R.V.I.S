// ---------------------------------------------------------------------------
// HudStateBar smoke tests.
// ---------------------------------------------------------------------------
// Coverage:
//   1. Renders without crashing in each of the 4 states
//   2. The displayed label matches the state (IDLE/LISTENING/THINKING/SPEAKING)
//   3. The correct indicator variant is mounted per state
//
// Reanimated mock is required so `useSharedValue`, `withRepeat`, and
// `withTiming` resolve synchronously in node. Pattern mirrors
// OrbView.test.tsx / TranscriptView.test.tsx exactly.
// ---------------------------------------------------------------------------

import { render } from '@testing-library/react-native'

jest.mock('react-native-reanimated', () =>
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  require('react-native-reanimated/mock'),
)

// eslint-disable-next-line import/first
import { HudStateBar, type HudState } from './HudStateBar'

describe('HudStateBar', () => {
  describe('renders without crashing', () => {
    const states: HudState[] = ['idle', 'listening', 'thinking', 'speaking']
    it.each(states)('state = %s', (state) => {
      const { getByTestId } = render(<HudStateBar state={state} />)
      expect(getByTestId('hud-state-bar')).toBeTruthy()
    })
  })

  describe('label reflects the current state', () => {
    const cases: Array<[HudState, string]> = [
      ['idle', 'IDLE'],
      ['listening', 'LISTENING'],
      ['thinking', 'THINKING'],
      ['speaking', 'SPEAKING'],
    ]
    it.each(cases)('state = %s shows label %s', (state, expected) => {
      const { getByTestId } = render(<HudStateBar state={state} />)
      expect(getByTestId('hud-state-bar-label')).toHaveTextContent(expected)
    })
  })

  describe('mounts the correct indicator per state', () => {
    it('idle mounts the static idle dot', () => {
      const { getByTestId, queryByTestId } = render(
        <HudStateBar state="idle" />,
      )
      expect(getByTestId('hud-state-indicator-idle')).toBeTruthy()
      expect(queryByTestId('hud-state-indicator-listening')).toBeNull()
      expect(queryByTestId('hud-state-indicator-thinking')).toBeNull()
      expect(queryByTestId('hud-state-indicator-speaking')).toBeNull()
    })

    it('listening mounts the pulsing dot', () => {
      const { getByTestId, queryByTestId } = render(
        <HudStateBar state="listening" />,
      )
      expect(getByTestId('hud-state-indicator-listening')).toBeTruthy()
      expect(queryByTestId('hud-state-indicator-idle')).toBeNull()
    })

    it('thinking mounts the 3-dot wave', () => {
      const { getByTestId, queryByTestId } = render(
        <HudStateBar state="thinking" />,
      )
      expect(getByTestId('hud-state-indicator-thinking')).toBeTruthy()
      expect(queryByTestId('hud-state-indicator-idle')).toBeNull()
    })

    it('speaking mounts the waveform bars', () => {
      const { getByTestId, queryByTestId } = render(
        <HudStateBar state="speaking" />,
      )
      expect(getByTestId('hud-state-indicator-speaking')).toBeTruthy()
      expect(queryByTestId('hud-state-indicator-idle')).toBeNull()
    })
  })
})
