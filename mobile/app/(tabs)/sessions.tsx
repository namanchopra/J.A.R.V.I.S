import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Animated,
  FlatList,
  RefreshControl,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from 'react-native';

import { awmApi, Session } from '../../lib/api';
import { router } from 'expo-router';
import { JARVIS } from '../../constants/Colors';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type StatusFilter = 'all' | 'running' | 'stopped' | 'failed';

const FILTERS: { key: StatusFilter; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'running', label: 'Running' },
  { key: 'stopped', label: 'Stopped' },
  { key: 'failed', label: 'Failed' },
];

const POLL_INTERVAL_MS = 5000;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Extract the last path segment as the repo name. */
function repoName(repoPath: string): string {
  const segments = repoPath.replace(/\/+$/, '').split('/');
  return segments[segments.length - 1] || repoPath;
}

/** Format a duration between startedAt and now as "Xm Ys" or "Xh Ym". */
function formatDuration(startedAt: string): string {
  const startMs = new Date(startedAt).getTime();
  if (Number.isNaN(startMs)) return '--';

  const diffSec = Math.max(0, Math.floor((Date.now() - startMs) / 1000));

  if (diffSec < 60) {
    return `${diffSec}s`;
  }

  const hours = Math.floor(diffSec / 3600);
  const minutes = Math.floor((diffSec % 3600) / 60);
  const seconds = diffSec % 60;

  if (hours > 0) {
    return `${hours}h ${String(minutes).padStart(2, '0')}m`;
  }

  return `${minutes}m ${String(seconds).padStart(2, '0')}s`;
}

function emptyMessageForFilter(filter: StatusFilter): string {
  switch (filter) {
    case 'all':
      return 'No sessions found';
    case 'running':
      return 'No running sessions';
    case 'stopped':
      return 'No stopped sessions';
    case 'failed':
      return 'No failed sessions';
  }
}

// ---------------------------------------------------------------------------
// Status indicator dot
// ---------------------------------------------------------------------------

const STATUS_DOT_COLORS: Record<string, string> = {
  launching: JARVIS.cyan,
  running: JARVIS.green,
  paused: JARVIS.textMuted,
  completed: JARVIS.green,
  failed: JARVIS.red,
  'needs-input': JARVIS.amber,
};

/** Pulsing green dot for running sessions; solid dot for others. */
function StatusDot({ status }: { status: Session['status'] }) {
  const opacity = useRef(new Animated.Value(1)).current;

  useEffect(() => {
    if (status !== 'running') {
      opacity.setValue(1);
      return;
    }

    const animation = Animated.loop(
      Animated.sequence([
        Animated.timing(opacity, {
          toValue: 0.25,
          duration: 800,
          useNativeDriver: true,
        }),
        Animated.timing(opacity, {
          toValue: 1,
          duration: 800,
          useNativeDriver: true,
        }),
      ]),
    );
    animation.start();

    return () => {
      animation.stop();
    };
  }, [status, opacity]);

  return (
    <Animated.View
      style={[
        styles.statusDot,
        { backgroundColor: STATUS_DOT_COLORS[status] ?? JARVIS.textMuted, opacity },
      ]}
    />
  );
}

// ---------------------------------------------------------------------------
// Session row
// ---------------------------------------------------------------------------

const SessionRow = React.memo(function SessionRow({
  session,
}: {
  session: Session;
}) {
  const handlePress = useCallback(() => {
    router.push(`/sessions/${session.id}` as never);
  }, [session.id]);

  return (
    <TouchableOpacity
      style={styles.row}
      onPress={handlePress}
      activeOpacity={0.7}
      accessibilityRole="button"
      accessibilityLabel={`Session ${repoName(session.repoPath)}, status ${session.status}`}
    >
      <StatusDot status={session.status} />

      <View style={styles.rowBody}>
        <Text style={styles.rowTitle} numberOfLines={1}>
          {repoName(session.repoPath)}
        </Text>
        <View style={styles.rowMeta}>
          <View style={styles.agentBadge}>
            <Text style={styles.agentBadgeText}>{session.agentType}</Text>
          </View>
          <Text style={styles.duration}>{formatDuration(session.startedAt)}</Text>
        </View>
      </View>

      <Text style={styles.chevron}>{'>'}</Text>
    </TouchableOpacity>
  );
});

// ---------------------------------------------------------------------------
// Filter bar
// ---------------------------------------------------------------------------

function FilterBar({
  active,
  onChange,
}: {
  active: StatusFilter;
  onChange: (filter: StatusFilter) => void;
}) {
  return (
    <View style={styles.filterBar}>
      {FILTERS.map(({ key, label }) => {
        const isActive = key === active;
        return (
          <TouchableOpacity
            key={key}
            style={[styles.filterPill, isActive && styles.filterPillActive]}
            onPress={() => onChange(key)}
            accessibilityRole="button"
            accessibilityState={{ selected: isActive }}
            accessibilityLabel={`Filter by ${label}`}
          >
            <Text
              style={[
                styles.filterPillText,
                isActive && styles.filterPillTextActive,
              ]}
            >
              {label}
            </Text>
          </TouchableOpacity>
        );
      })}
    </View>
  );
}

// ---------------------------------------------------------------------------
// Empty state
// ---------------------------------------------------------------------------

function EmptyState({ filter }: { filter: StatusFilter }) {
  return (
    <View style={styles.emptyContainer}>
      <Text style={styles.emptyText}>{emptyMessageForFilter(filter)}</Text>
    </View>
  );
}

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function SessionsScreen() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [activeFilter, setActiveFilter] = useState<StatusFilter>('all');
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [initialLoad, setInitialLoad] = useState(true);

  // -------------------------------------------------------------------------
  // Fetch sessions
  // -------------------------------------------------------------------------

  const fetchSessions = useCallback(async () => {
    try {
      const data = await awmApi.listSessions();
      setSessions(data);
      setError(null);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to load sessions';
      setError(message);
    }
  }, []);

  // Initial load
  useEffect(() => {
    let mounted = true;

    async function load() {
      await fetchSessions();
      if (mounted) {
        setInitialLoad(false);
      }
    }

    load();

    return () => {
      mounted = false;
    };
  }, [fetchSessions]);

  // Auto-poll every 5s
  useEffect(() => {
    const interval = setInterval(fetchSessions, POLL_INTERVAL_MS);
    return () => clearInterval(interval);
  }, [fetchSessions]);

  // -------------------------------------------------------------------------
  // Pull-to-refresh
  // -------------------------------------------------------------------------

  const handleRefresh = useCallback(async () => {
    setRefreshing(true);
    await fetchSessions();
    setRefreshing(false);
  }, [fetchSessions]);

  // -------------------------------------------------------------------------
  // Filter logic
  // -------------------------------------------------------------------------

  const filtered = useMemo(() => {
    if (activeFilter === 'all') return sessions;

    return sessions.filter((s) => {
      if (activeFilter === 'running') {
        // needs-input sessions are still active, show them under Running
        return s.status === 'running' || s.status === 'needs-input';
      }
      return s.status === activeFilter;
    });
  }, [sessions, activeFilter]);

  // -------------------------------------------------------------------------
  // Render helpers
  // -------------------------------------------------------------------------

  const renderItem = useCallback(
    ({ item }: { item: Session }) => <SessionRow session={item} />,
    [],
  );

  const keyExtractor = useCallback((item: Session) => item.id, []);

  const listEmptyComponent = useMemo(
    () =>
      !initialLoad ? <EmptyState filter={activeFilter} /> : null,
    [activeFilter, initialLoad],
  );

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <View style={styles.container}>
      <FilterBar active={activeFilter} onChange={setActiveFilter} />

      {error !== null && !initialLoad && (
        <View style={styles.errorBanner}>
          <Text style={styles.errorText}>{error}</Text>
        </View>
      )}

      <FlatList
        data={filtered}
        renderItem={renderItem}
        keyExtractor={keyExtractor}
        contentContainerStyle={
          filtered.length === 0 ? styles.listEmpty : styles.listContent
        }
        ListEmptyComponent={listEmptyComponent}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            onRefresh={handleRefresh}
            tintColor={JARVIS.cyan}
            colors={[JARVIS.cyan]}
            progressBackgroundColor={JARVIS.surface}
          />
        }
      />
    </View>
  );
}

// ---------------------------------------------------------------------------
// Styles
// ---------------------------------------------------------------------------

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: JARVIS.bg,
  },

  // Filter bar
  filterBar: {
    flexDirection: 'row',
    paddingHorizontal: 16,
    paddingVertical: 12,
    gap: 8,
  },
  filterPill: {
    paddingHorizontal: 14,
    paddingVertical: 6,
    borderRadius: 16,
    backgroundColor: JARVIS.elevated,
    borderWidth: 1,
    borderColor: JARVIS.border,
  },
  filterPillActive: {
    backgroundColor: JARVIS.cyanDim,
    borderColor: JARVIS.cyan,
  },
  filterPillText: {
    fontSize: 13,
    fontWeight: '600',
    color: JARVIS.textSecondary,
  },
  filterPillTextActive: {
    color: JARVIS.cyan,
  },

  // Error banner
  errorBanner: {
    marginHorizontal: 16,
    marginBottom: 8,
    backgroundColor: JARVIS.redDim,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: JARVIS.red,
    paddingVertical: 10,
    paddingHorizontal: 14,
  },
  errorText: {
    color: JARVIS.red,
    fontSize: 13,
    fontWeight: '500',
  },

  // List
  listContent: {
    paddingHorizontal: 16,
    paddingBottom: 24,
  },
  listEmpty: {
    flexGrow: 1,
    justifyContent: 'center',
    alignItems: 'center',
  },

  // Session row
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.surface,
    borderRadius: 10,
    paddingVertical: 14,
    paddingHorizontal: 14,
    marginBottom: 8,
    borderWidth: 1,
    borderColor: JARVIS.border,
  },
  rowBody: {
    flex: 1,
    marginLeft: 12,
  },
  rowTitle: {
    fontSize: 16,
    fontWeight: '600',
    color: JARVIS.text,
    marginBottom: 4,
  },
  rowMeta: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
  },
  agentBadge: {
    backgroundColor: JARVIS.elevated,
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 2,
    borderWidth: 1,
    borderColor: JARVIS.border,
  },
  agentBadgeText: {
    fontSize: 11,
    fontWeight: '600',
    color: JARVIS.textSecondary,
  },
  duration: {
    fontSize: 12,
    color: JARVIS.textSecondary,
    fontFamily: 'SpaceMono',
  },
  chevron: {
    fontSize: 18,
    color: JARVIS.textMuted,
    marginLeft: 8,
  },

  // Status dot
  statusDot: {
    width: 10,
    height: 10,
    borderRadius: 5,
  },

  // Empty state
  emptyContainer: {
    alignItems: 'center',
    justifyContent: 'center',
    paddingVertical: 40,
  },
  emptyText: {
    fontSize: 15,
    color: JARVIS.textMuted,
  },
});
