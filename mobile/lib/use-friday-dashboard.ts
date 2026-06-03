import { useEffect, useState } from 'react';
import { JarvisWS, type NextEventSnapshot, type StatsSnapshot } from './jarvis-ws';

/**
 * Snapshot of dashboard counters the Friday home screen renders.
 *
 * All counts default to 0 (not null/undefined) so the UI never has to guard
 * against missing data: a fresh / offline state simply renders zeros, and
 * the WS push fills them in once the Go server starts broadcasting.
 */
export interface FridayDashboardSnapshot {
  /** Number of active (running / needs-input) sessions on the Mac. */
  activeSessions: number;
  /** Number of pending approval prompts waiting on sir's answer. */
  pendingApprovals: number;
  /** Number of currently running tasks. */
  runningTasks: number;
  /** Number of activity events emitted today (rough proxy for "busy-ness"). */
  eventsToday: number;
  /** Most recent activity event message, trimmed to one short line. */
  latestActivity: string;
  /**
   * Next upcoming Google Calendar event, or null when the calendar is
   * empty, not connected, or errored. Index.tsx renders ``relativeTime``
   * as the tile value and ``title`` as the caption; null state renders
   * a dash with "nothing soon" caption.
   */
  nextEvent: NextEventSnapshot | null;
  /** Last successful update timestamp (ms epoch). 0 when never updated. */
  lastUpdatedAt: number;
}

const INITIAL_SNAPSHOT: FridayDashboardSnapshot = {
  activeSessions: 0,
  pendingApprovals: 0,
  runningTasks: 0,
  eventsToday: 0,
  latestActivity: '',
  nextEvent: null,
  lastUpdatedAt: 0,
};

interface UseFridayDashboardOptions {
  /** Shared WS instance to subscribe on. Required. */
  ws: JarvisWS;
}

/**
 * Subscribes to ``stats_snapshot`` WS events pushed by the Go server every
 * ~5 seconds. Replaces the previous REST poller -- Expo Go on the latest
 * SDK no longer auto-bypasses iOS App Transport Security for plain http://
 * so REST fetches all failed with ``Network request failed`` while the WS
 * to the same host worked fine. The Go server now fetches the snapshot
 * in-process from the App struct and pushes it over the already-authorised
 * mobile WS, sidestepping ATS entirely.
 *
 * Memory: only one listener is installed regardless of how many components
 * call this hook (the underlying ``ws.on`` returns its own disposer so
 * unmount cleanup is clean).
 */
export function useFridayDashboard(
  options: UseFridayDashboardOptions,
): FridayDashboardSnapshot {
  const { ws } = options;
  const [snapshot, setSnapshot] = useState<FridayDashboardSnapshot>(
    INITIAL_SNAPSHOT,
  );

  useEffect(() => {
    console.log('[useFridayDashboard] subscribing to statsSnapshot');
    const off = ws.on('statsSnapshot', (stats: StatsSnapshot) => {
      console.log('[useFridayDashboard] statsSnapshot listener fired', stats);
      setSnapshot({
        activeSessions: stats.activeSessions,
        pendingApprovals: stats.pendingApprovals,
        runningTasks: stats.runningTasks,
        eventsToday: stats.eventsToday,
        latestActivity: stats.latestActivity,
        nextEvent: stats.nextEvent,
        lastUpdatedAt: Date.now(),
      });
    });
    return off;
  }, [ws]);

  return snapshot;
}
