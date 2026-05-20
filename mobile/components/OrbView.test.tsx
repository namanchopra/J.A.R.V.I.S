// ---------------------------------------------------------------------------
// OrbView smoke tests -- TASK-019 acceptance criteria.
// ---------------------------------------------------------------------------
// Coverage:
//  1. Renders without crashing in each of the 3 states (idle/listening/speaking)
//  2. Corner labels show "—" when prop undefined
//  3. Corner labels show the value when prop set
//  4. Sessions count renders even when undefined (defaults to 0)
//
// We mock react-native-reanimated with the library's own jest mock so
// useSharedValue / withRepeat / withTiming behave synchronously in tests.
// Without this, the worklets bridge tries to spin up a real UI thread and
// the test harness hangs.
// ---------------------------------------------------------------------------

import { render } from '@testing-library/react-native'

// Reanimated's official jest mock -- provides synchronous stubs for the
// shared value + animation primitives. Must be installed via jest.setup
// per the Reanimated docs; doing it here keeps the test self-contained.
jest.mock('react-native-reanimated', () =>
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  require('react-native-reanimated/mock'),
)

// react-native-svg renders fine in JSDOM-style Jest -- the components are
// just View wrappers in the test environment. No mock needed.

import { OrbView, type OrbState } from './OrbView'

describe('OrbView', () => {
  describe('renders without crashing', () => {
    const states: OrbState[] = ['idle', 'listening', 'speaking']
    it.each(states)('state = %s', (state) => {
      const { getByTestId } = render(<OrbView state={state} sessions={0} />)
      expect(getByTestId('orb-view')).toBeTruthy()
    })
  })

  describe('corner labels -- fallback to em-dash when undefined', () => {
    it('shows "—" for llmLabel, sttLabel, ttsLabel when props not supplied', () => {
      const { getByTestId } = render(<OrbView state="idle" />)
      expect(getByTestId('orb-label-llm-value')).toHaveTextContent('—')
      expect(getByTestId('orb-label-stt-value')).toHaveTextContent('—')
      expect(getByTestId('orb-label-tts-value')).toHaveTextContent('—')
    })

    it('shows "—" for whitespace-only prop strings', () => {
      const { getByTestId } = render(
        <OrbView state="idle" llmLabel="   " sttLabel="" ttsLabel="	" />,
      )
      expect(getByTestId('orb-label-llm-value')).toHaveTextContent('—')
      expect(getByTestId('orb-label-stt-value')).toHaveTextContent('—')
      expect(getByTestId('orb-label-tts-value')).toHaveTextContent('—')
    })
  })

  describe('corner labels -- show value when prop set', () => {
    it('renders supplied LLM/STT/TTS values verbatim', () => {
      const { getByTestId } = render(
        <OrbView
          state="idle"
          llmLabel="anthropic:claude-3.7"
          sttLabel="whisper:small.en"
          ttsLabel="vibevoice:en-Frank_man"
          sessions={11}
        />,
      )
      expect(getByTestId('orb-label-llm-value')).toHaveTextContent(
        'anthropic:claude-3.7',
      )
      expect(getByTestId('orb-label-stt-value')).toHaveTextContent(
        'whisper:small.en',
      )
      expect(getByTestId('orb-label-tts-value')).toHaveTextContent(
        'vibevoice:en-Frank_man',
      )
      expect(getByTestId('orb-label-sessions-value')).toHaveTextContent('11')
    })

    it('SESSIONS::N defaults to 0 when prop omitted', () => {
      const { getByTestId } = render(<OrbView state="idle" />)
      expect(getByTestId('orb-label-sessions-value')).toHaveTextContent('0')
    })
  })

  describe('prop-driven behaviour does not throw', () => {
    it('accepts audioLevel=0..1 without error in speaking state', () => {
      // We do not assert visual output (Reanimated mock is stubbed); we only
      // assert the component does not throw when given various audio levels.
      // This guards against accidental NaN scale values or out-of-range
      // shared value writes in the audio-level effect.
      const levels = [0, 0.25, 0.5, 0.75, 1, -0.5, 1.5] // includes clamping cases
      for (const level of levels) {
        const { unmount } = render(
          <OrbView state="speaking" audioLevel={level} />,
        )
        unmount()
      }
    })
  })
})
