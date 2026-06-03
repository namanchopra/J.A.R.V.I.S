// ---------------------------------------------------------------------------
// TranscriptChip smoke tests.
// ---------------------------------------------------------------------------
// Coverage:
//   1. Renders nothing when both props are null
//   2. Renders user prefix + body when only userText is set
//   3. Renders assistant prefix + body when only assistantText is set
//   4. Switching props swaps the displayed role/text
//   5. onPress fires when the chip is tapped
//
// Reanimated mock keeps useSharedValue / withTiming synchronous.
// ---------------------------------------------------------------------------

import { fireEvent, render } from '@testing-library/react-native'

jest.mock('react-native-reanimated', () =>
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  require('react-native-reanimated/mock'),
)

// eslint-disable-next-line import/first
import { TranscriptChip } from './TranscriptChip'

describe('TranscriptChip', () => {
  it('renders nothing when both props are null', () => {
    const { queryByTestId } = render(
      <TranscriptChip userText={null} assistantText={null} />,
    )
    expect(queryByTestId('transcript-chip')).toBeNull()
  })

  it('renders nothing when both props are undefined', () => {
    const { queryByTestId } = render(<TranscriptChip />)
    expect(queryByTestId('transcript-chip')).toBeNull()
  })

  it('renders user prefix + body when only userText is set', () => {
    const { getByTestId } = render(
      <TranscriptChip userText="hello jarvis" assistantText={null} />,
    )
    const chip = getByTestId('transcript-chip')
    expect(chip).toBeTruthy()
    expect(chip).toHaveTextContent(/YOU >/)
    expect(chip).toHaveTextContent(/hello jarvis/)
  })

  it('renders assistant prefix + body when only assistantText is set', () => {
    const { getByTestId } = render(
      <TranscriptChip userText={null} assistantText="affirmative, sir" />,
    )
    const chip = getByTestId('transcript-chip')
    expect(chip).toBeTruthy()
    expect(chip).toHaveTextContent(/JARVIS >/)
    expect(chip).toHaveTextContent(/affirmative, sir/)
  })

  it('updates the displayed text when assistantText changes', () => {
    const { getByTestId, rerender } = render(
      <TranscriptChip userText={null} assistantText="first reply" />,
    )
    expect(getByTestId('transcript-chip-current')).toHaveTextContent(
      /first reply/,
    )

    rerender(<TranscriptChip userText={null} assistantText="second reply" />)
    // The "current" layer is always the most recent value.
    expect(getByTestId('transcript-chip-current')).toHaveTextContent(
      /second reply/,
    )
  })

  it('switches role when the other prop changes', () => {
    const { getByTestId, rerender } = render(
      <TranscriptChip userText="ask" assistantText={null} />,
    )
    expect(getByTestId('transcript-chip-current')).toHaveTextContent(/YOU >/)

    rerender(<TranscriptChip userText="ask" assistantText="answer" />)
    // assistantText changed -> the current layer now shows the JARVIS prefix.
    expect(getByTestId('transcript-chip-current')).toHaveTextContent(
      /JARVIS >/,
    )
    expect(getByTestId('transcript-chip-current')).toHaveTextContent(/answer/)
  })

  it('fires onPress when tapped', () => {
    const onPress = jest.fn()
    const { getByTestId } = render(
      <TranscriptChip userText="hello" onPress={onPress} />,
    )
    fireEvent.press(getByTestId('transcript-chip'))
    expect(onPress).toHaveBeenCalledTimes(1)
  })

  it('does not crash when pressed with no onPress handler', () => {
    const { getByTestId } = render(<TranscriptChip userText="hello" />)
    // Pressable is disabled when onPress is omitted, so this is a no-op,
    // but it should never throw.
    expect(() => {
      fireEvent.press(getByTestId('transcript-chip'))
    }).not.toThrow()
  })
})
