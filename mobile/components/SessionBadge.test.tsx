// ---------------------------------------------------------------------------
// SessionBadge smoke tests.
// ---------------------------------------------------------------------------
// Coverage:
//   1. Renders with count + SESSIONS:: prefix
//   2. Shows the alert dot only when hasApprovals is true
//   3. onPress fires when tapped (with hitSlop a non-tight target)
//   4. Negative / non-finite counts are coerced to 0 rather than crashing
//   5. accessibilityLabel reflects approvals state
// ---------------------------------------------------------------------------

import { fireEvent, render } from '@testing-library/react-native'

import { SessionBadge } from './SessionBadge'

describe('SessionBadge', () => {
  it('renders SESSIONS::N label with the supplied count', () => {
    const { getByTestId } = render(
      <SessionBadge count={3} hasApprovals={false} />,
    )
    expect(getByTestId('session-badge')).toBeTruthy()
    expect(getByTestId('session-badge-label')).toHaveTextContent('SESSIONS::3')
  })

  it('renders count of 0 when none active', () => {
    const { getByTestId } = render(
      <SessionBadge count={0} hasApprovals={false} />,
    )
    expect(getByTestId('session-badge-label')).toHaveTextContent('SESSIONS::0')
  })

  it('does not render the alert dot when hasApprovals is false', () => {
    const { queryByTestId } = render(
      <SessionBadge count={2} hasApprovals={false} />,
    )
    expect(queryByTestId('session-badge-alert-dot')).toBeNull()
  })

  it('renders the alert dot when hasApprovals is true', () => {
    const { getByTestId } = render(
      <SessionBadge count={2} hasApprovals={true} />,
    )
    expect(getByTestId('session-badge-alert-dot')).toBeTruthy()
  })

  it('fires onPress when tapped', () => {
    const onPress = jest.fn()
    const { getByTestId } = render(
      <SessionBadge count={1} hasApprovals={false} onPress={onPress} />,
    )
    fireEvent.press(getByTestId('session-badge'))
    expect(onPress).toHaveBeenCalledTimes(1)
  })

  it('does not crash when tapped without an onPress handler', () => {
    const { getByTestId } = render(
      <SessionBadge count={1} hasApprovals={false} />,
    )
    expect(() => {
      fireEvent.press(getByTestId('session-badge'))
    }).not.toThrow()
  })

  it('coerces negative counts to 0', () => {
    const { getByTestId } = render(
      <SessionBadge count={-5} hasApprovals={false} />,
    )
    expect(getByTestId('session-badge-label')).toHaveTextContent('SESSIONS::0')
  })

  it('coerces non-finite counts to 0', () => {
    const { getByTestId } = render(
      <SessionBadge count={Number.NaN} hasApprovals={false} />,
    )
    expect(getByTestId('session-badge-label')).toHaveTextContent('SESSIONS::0')
  })

  it('floors fractional counts', () => {
    const { getByTestId } = render(
      <SessionBadge count={3.9} hasApprovals={false} />,
    )
    expect(getByTestId('session-badge-label')).toHaveTextContent('SESSIONS::3')
  })

  it('sets an accessibilityLabel that reflects approvals state', () => {
    const { getByTestId, rerender } = render(
      <SessionBadge count={2} hasApprovals={false} />,
    )
    expect(getByTestId('session-badge').props.accessibilityLabel).toBe(
      'Sessions 2',
    )

    rerender(<SessionBadge count={2} hasApprovals={true} />)
    expect(getByTestId('session-badge').props.accessibilityLabel).toBe(
      'Sessions 2, approvals pending',
    )
  })
})
