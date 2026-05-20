// ---------------------------------------------------------------------------
// PushToTalkButton smoke tests -- TASK-021 acceptance criteria.
// ---------------------------------------------------------------------------
// Coverage:
//   1. Renders without crashing in the default (undetermined-permission) state
//   2. Renders the OrbView underneath (testID="orb-view" is present)
//   3. onAudioChunk is a function-typed optional prop -- compile-time check
//   4. Permission denied -> Pressable disabled + permission banner shown +
//      orb still rendered (the orb is the press target but non-interactive)
//   5. Permission granted on probe -> banner is NOT shown
//
// We mock `expo-av` here because expo-av's native module isn't available in
// the Jest environment; we also mock reanimated via its official mock (same
// pattern OrbView.test.tsx uses) so animation primitives are synchronous.
// ---------------------------------------------------------------------------

import { act, render } from '@testing-library/react-native'

// Reanimated mock -- shared with OrbView.test.tsx. Without this the orb's
// useSharedValue / withRepeat calls try to spin up a real UI thread and
// hang the test.
jest.mock('react-native-reanimated', () =>
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  require('react-native-reanimated/mock'),
)

// ---- expo-av mock ---------------------------------------------------------
// We default to undetermined permission so the component renders its
// "press-to-start" path. Individual tests override `mockGetPermission` /
// `mockRequestPermission` to flip into granted / denied.
const mockGetPermission = jest.fn(async () => ({
  granted: false,
  canAskAgain: true,
  status: 'undetermined',
}))
const mockRequestPermission = jest.fn(async () => ({
  granted: true,
  canAskAgain: true,
  status: 'granted',
}))

jest.mock('expo-av', () => {
  const RecordingMock = jest.fn().mockImplementation(() => ({
    prepareToRecordAsync: jest.fn(async () => ({})),
    startAsync: jest.fn(async () => ({})),
    stopAndUnloadAsync: jest.fn(async () => ({})),
    getURI: jest.fn(() => null),
  }))
  return {
    Audio: {
      getPermissionsAsync: () => mockGetPermission(),
      requestPermissionsAsync: () => mockRequestPermission(),
      setAudioModeAsync: jest.fn(async () => undefined),
      Recording: RecordingMock,
      // Enum-like constants the component imports inline at module load.
      AndroidOutputFormat: { MPEG_4: 2 },
      AndroidAudioEncoder: { AAC: 3 },
      IOSAudioQuality: { LOW: 32 },
    },
  }
})

// Import AFTER the mock so the component picks up the stubbed Audio module.
// eslint-disable-next-line import/first
import { PushToTalkButton, type PushToTalkButtonProps } from './PushToTalkButton'

// ---- Helpers --------------------------------------------------------------
// Wait one microtask + one macrotask so the useEffect-triggered
// `getPermissionsAsync` promise settles and the state update flushes
// before assertions run. Jest's default timer doesn't auto-advance promises
// so we do this explicitly.

async function flushAsync(): Promise<void> {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

// ---- Tests ----------------------------------------------------------------

beforeEach(() => {
  mockGetPermission.mockReset().mockResolvedValue({
    granted: false,
    canAskAgain: true,
    status: 'undetermined',
  })
  mockRequestPermission.mockReset().mockResolvedValue({
    granted: true,
    canAskAgain: true,
    status: 'granted',
  })
})

describe('PushToTalkButton', () => {
  describe('renders without crashing', () => {
    it('mounts with default props (no callbacks, undetermined permission)', async () => {
      const { getByTestId } = render(<PushToTalkButton />)
      await flushAsync()
      expect(getByTestId('ptt-container')).toBeTruthy()
      expect(getByTestId('ptt-pressable')).toBeTruthy()
      // The OrbView lives inside the pressable -- testID="orb-view" comes
      // from the OrbView component itself. This guards against accidental
      // refactors that drop the orb (e.g. someone replacing with a button).
      expect(getByTestId('orb-view')).toBeTruthy()
    })

    it('mounts with sessions + label props forwarded to the orb', async () => {
      const { getByTestId } = render(
        <PushToTalkButton
          sessions={11}
          llmLabel="anthropic:claude-3.7"
          sttLabel="whisper:small.en"
          ttsLabel="vibevoice:en-Frank_man"
        />,
      )
      await flushAsync()
      expect(getByTestId('orb-label-sessions-value')).toHaveTextContent('11')
      expect(getByTestId('orb-label-llm-value')).toHaveTextContent(
        'anthropic:claude-3.7',
      )
      expect(getByTestId('orb-label-stt-value')).toHaveTextContent(
        'whisper:small.en',
      )
      expect(getByTestId('orb-label-tts-value')).toHaveTextContent(
        'vibevoice:en-Frank_man',
      )
    })
  })

  describe('onAudioChunk prop is function-typed (compile-time)', () => {
    // This test is a static-typing assertion via TypeScript narrowing rather
    // than a runtime behaviour check. If the prop type ever drifts away from
    // `(Uint8Array) => void | Promise<void>` the file will fail to compile.
    it('accepts an async (Uint8Array) => Promise<void> handler', () => {
      const handler: NonNullable<PushToTalkButtonProps['onAudioChunk']> =
        async (chunk: Uint8Array) => {
          // Reference the param so the lint doesn't flag it unused. We don't
          // need the value -- only that the type is what we expect.
          expect(chunk).toBeDefined()
        }
      expect(typeof handler).toBe('function')
    })

    it('accepts a sync (Uint8Array) => void handler', () => {
      const handler: NonNullable<PushToTalkButtonProps['onAudioChunk']> = (
        chunk: Uint8Array,
      ): void => {
        expect(chunk).toBeDefined()
      }
      expect(typeof handler).toBe('function')
    })
  })

  describe('mic permission denied', () => {
    beforeEach(() => {
      mockGetPermission.mockResolvedValue({
        granted: false,
        canAskAgain: false,
        status: 'denied',
      })
    })

    it('still renders the orb (non-interactive)', async () => {
      const { getByTestId } = render(<PushToTalkButton sessions={0} />)
      await flushAsync()
      // Orb still present -- the press is the no-op, not the render path.
      expect(getByTestId('orb-view')).toBeTruthy()
    })

    it('shows the permission banner with an Open Settings CTA', async () => {
      const { getByTestId } = render(<PushToTalkButton />)
      await flushAsync()
      expect(getByTestId('ptt-permission-banner')).toBeTruthy()
      expect(getByTestId('ptt-settings-button')).toBeTruthy()
    })

    it('disables the Pressable so onPressIn is a no-op', async () => {
      const onPressStart = jest.fn()
      const { getByTestId } = render(
        <PushToTalkButton onPressStart={onPressStart} />,
      )
      await flushAsync()
      const pressable = getByTestId('ptt-pressable')
      // RN's Pressable propagates `disabled` to accessibilityState; checking
      // the prop directly is the cleanest assertion the press handlers
      // won't fire. (Calling fireEvent.press on a disabled Pressable does
      // nothing in RNTL anyway.)
      expect(pressable.props.accessibilityState?.disabled).toBe(true)
    })
  })

  describe('mic permission granted on probe', () => {
    beforeEach(() => {
      mockGetPermission.mockResolvedValue({
        granted: true,
        canAskAgain: true,
        status: 'granted',
      })
    })

    it('does NOT show the permission banner', async () => {
      const { queryByTestId } = render(<PushToTalkButton />)
      await flushAsync()
      expect(queryByTestId('ptt-permission-banner')).toBeNull()
    })

    it('Pressable is enabled', async () => {
      const { getByTestId } = render(<PushToTalkButton />)
      await flushAsync()
      const pressable = getByTestId('ptt-pressable')
      // Either `accessibilityState.disabled` is false or the prop is
      // absent; both indicate "enabled". RN's Pressable defaults to setting
      // accessibilityState.disabled=false when the prop is undefined.
      const disabled = pressable.props.accessibilityState?.disabled
      expect(disabled === false || disabled === undefined).toBe(true)
    })
  })
})
