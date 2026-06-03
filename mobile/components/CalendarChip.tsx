// ---------------------------------------------------------------------------
// CalendarChip -- compact "next event" pill rendered near the top of the
// Friday mobile screen. Mirrors the Mac HUD's CALENDAR panel but folded to
// a single line because phone screens don't have the vertical real estate
// for the upcoming list.
//
// Data source: awmApi.getNextCalendarEvent() which hits
// GET /calendar/next on the Mac. The handler returns a connected boolean
// and a NextEventSnapshot with a server-formatted RelativeTime ("in 14m" /
// "now" / "in 2h"), so the chip doesn't ship its own time math.
//
// Polling: 60s while mounted. Same cadence as the Mac panel so both views
// drift in lockstep.
//
// States:
//   - loading      : null (chip is invisible -- avoids a "loading…" flash
//                    on app start)
//   - disconnected : null (we don't want to nag the user about Google on
//                    every mobile screen; the connect CTA lives on Mac)
//   - empty        : null (no upcoming -- chip silently absent)
//   - has event    : pill with relative-time tag + title
// ---------------------------------------------------------------------------

import { useEffect, useState } from 'react'
import type { ReactElement } from 'react'
import { StyleSheet, Text, View } from 'react-native'

import { awmApi, type NextEventSnapshot } from '../lib/api'
import { colors, fontFamilies, spacing } from '../lib/hud-tokens'

export interface CalendarChipProps {
  /** Poll cadence in ms. Default 60s. Tests inject smaller values. */
  pollIntervalMs?: number
}

const DEFAULT_POLL_MS = 60_000

/** Format an RFC3339 string as "14:30" local time. */
function formatTime(raw?: string): string {
  if (!raw) return ''
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return ''
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${hh}:${mm}`
}

export function CalendarChip(
  { pollIntervalMs = DEFAULT_POLL_MS }: CalendarChipProps = {},
): ReactElement | null {
  const [event, setEvent] = useState<NextEventSnapshot | null>(null)

  useEffect(() => {
    let cancelled = false

    const refresh = async (): Promise<void> => {
      try {
        const resp = await awmApi.getNextCalendarEvent()
        if (cancelled) return
        if (!resp.connected || !resp.event) {
          setEvent(null)
          return
        }
        setEvent(resp.event)
      } catch {
        // Network glitches / unauthenticated tokens -- silently no event.
        // The pair screen handles the auth flow elsewhere; we don't want
        // a calendar fetch error to surface as a user-visible notice.
        if (!cancelled) setEvent(null)
      }
    }

    void refresh()
    const id = setInterval(() => void refresh(), pollIntervalMs)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [pollIntervalMs])

  if (!event) return null

  const time = formatTime(event.start)
  const rel = (event.relativeTime ?? '').toUpperCase()

  return (
    <View style={styles.chip} accessibilityRole="text" accessibilityLabel={`Next calendar event: ${event.title}`}>
      <Text style={styles.tag} numberOfLines={1}>
        NEXT{rel ? ` · ${rel}` : ''}{time ? ` · ${time}` : ''}
      </Text>
      <Text style={styles.title} numberOfLines={1}>
        {event.title ?? 'Untitled'}
      </Text>
    </View>
  )
}

const styles = StyleSheet.create({
  chip: {
    paddingHorizontal: spacing.s ?? 8,
    paddingVertical: 6,
    borderRadius: 999,
    borderWidth: 1,
    borderColor: colors.cyanDark,
    backgroundColor: colors.bgPanel,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
    alignSelf: 'center',
    maxWidth: '92%',
  },
  tag: {
    fontFamily: fontFamilies.monoBold,
    color: colors.textDim,
    fontSize: 9,
    letterSpacing: 1.4,
  },
  title: {
    fontFamily: fontFamilies.mono,
    color: colors.cyan,
    fontSize: 12,
    flexShrink: 1,
  },
})
