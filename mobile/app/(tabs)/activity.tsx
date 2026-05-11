import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  FlatList,
  RefreshControl,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from 'react-native';

import { awmApi, ActivityEvent } from '../../lib/api';
import { JARVIS, jarvisStyles } from '../../constants/Colors';
import { router } from 'expo-router';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const PAGE_SIZE = 20;
const TIMESTAMP_REFRESH_INTERVAL_MS = 30_000;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function relativeTime(isoString: string): string {
  const diff = Date.now() - new Date(isoString).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

function iconForEventType(type: string): string {
  switch (type) {
    case 'session_started':
      return '\u25B6';
    case 'session_stopped':
      return '\u23F9';
    case 'session_failed':
      return '\u2717';
    case 'workspace_created':
      return '+';
    case 'approval_requested':
      return '?';
    default:
      return '\u2022';
  }
}

/** Map event type to a left-border accent color */
function borderColorForEventType(type: string): string {
  switch (type) {
    case 'created':
    case 'session_started':
    case 'workspace_created':
      return JARVIS.cyan;
    case 'completed':
    case 'session_stopped':
      return JARVIS.green;
    case 'failed':
    case 'session_failed':
      return JARVIS.red;
    case 'status_changed':
    case 'approval_requested':
      return JARVIS.amber;
    default:
      return JARVIS.border;
  }
}

/** Map event type to icon tint */
function iconColorForEventType(type: string): string {
  switch (type) {
    case 'created':
    case 'session_started':
    case 'workspace_created':
      return JARVIS.cyan;
    case 'completed':
    case 'session_stopped':
      return JARVIS.green;
    case 'failed':
    case 'session_failed':
      return JARVIS.red;
    case 'status_changed':
    case 'approval_requested':
      return JARVIS.amber;
    default:
      return JARVIS.textMuted;
  }
}

// ---------------------------------------------------------------------------
// Row component
// ---------------------------------------------------------------------------

const ActivityRow = React.memo(function ActivityRow({
  event,
  timestampTick: _timestampTick,
}: {
  event: ActivityEvent;
  timestampTick: number;
}) {
  const hasSession = Boolean(event.sessionId);
  const accentColor = borderColorForEventType(event.type);
  const iconTint = iconColorForEventType(event.type);

  const handlePress = useCallback(() => {
    if (event.sessionId) {
      router.push(`/sessions/${event.sessionId}`);
    }
  }, [event.sessionId]);

  const icon = iconForEventType(event.type);

  const content = (
    <View style={[styles.row, { borderLeftColor: accentColor }]}>
      <Text style={[styles.icon, { color: iconTint }]}>{icon}</Text>
      <View style={styles.rowContent}>
        <Text style={styles.description} numberOfLines={2}>
          {event.description}
        </Text>
        <Text style={styles.timestamp}>{relativeTime(event.createdAt)}</Text>
      </View>
      {hasSession && <Text style={styles.chevron}>{'\u203A'}</Text>}
    </View>
  );

  if (hasSession) {
    return (
      <TouchableOpacity
        onPress={handlePress}
        activeOpacity={0.6}
        accessibilityRole="button"
        accessibilityLabel={`${event.description}, ${relativeTime(event.createdAt)}`}
      >
        {content}
      </TouchableOpacity>
    );
  }

  return content;
});

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function ActivityScreen() {
  const [events, setEvents] = useState<ActivityEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [hasMore, setHasMore] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Tick counter that increments every 30s to force timestamp re-renders
  const [timestampTick, setTimestampTick] = useState(0);

  // Track whether a load-more fetch is in flight to prevent duplicate calls
  const loadingMoreRef = useRef(false);

  // -------------------------------------------------------------------------
  // Fetch first page
  // -------------------------------------------------------------------------

  const fetchFirstPage = useCallback(async () => {
    try {
      setError(null);
      const data = await awmApi.getActivity(PAGE_SIZE);
      setEvents(data);
      setHasMore(data.length >= PAGE_SIZE);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to load activity';
      setError(message);
    }
  }, []);

  // -------------------------------------------------------------------------
  // Initial load
  // -------------------------------------------------------------------------

  useEffect(() => {
    let mounted = true;

    async function init() {
      setLoading(true);
      try {
        setError(null);
        const data = await awmApi.getActivity(PAGE_SIZE);
        if (!mounted) return;
        setEvents(data);
        setHasMore(data.length >= PAGE_SIZE);
      } catch (err: unknown) {
        if (!mounted) return;
        const message = err instanceof Error ? err.message : 'Failed to load activity';
        setError(message);
      } finally {
        if (mounted) setLoading(false);
      }
    }

    init();

    return () => {
      mounted = false;
    };
  }, []);

  // -------------------------------------------------------------------------
  // Timestamp refresh every 30s
  // -------------------------------------------------------------------------

  useEffect(() => {
    const interval = setInterval(() => {
      setTimestampTick((prev) => prev + 1);
    }, TIMESTAMP_REFRESH_INTERVAL_MS);

    return () => clearInterval(interval);
  }, []);

  // -------------------------------------------------------------------------
  // Pull-to-refresh
  // -------------------------------------------------------------------------

  const handleRefresh = useCallback(async () => {
    setRefreshing(true);
    await fetchFirstPage();
    setRefreshing(false);
  }, [fetchFirstPage]);

  // -------------------------------------------------------------------------
  // Load more (infinite scroll)
  // -------------------------------------------------------------------------

  const handleLoadMore = useCallback(async () => {
    if (!hasMore || loadingMoreRef.current || events.length === 0) return;

    loadingMoreRef.current = true;
    setLoadingMore(true);

    const lastEvent = events[events.length - 1];

    try {
      const data = await awmApi.getActivity(PAGE_SIZE, lastEvent.id);
      setEvents((prev) => [...prev, ...data]);
      setHasMore(data.length >= PAGE_SIZE);
    } catch {
      // Silently fail on load-more -- user can scroll again to retry
    } finally {
      setLoadingMore(false);
      loadingMoreRef.current = false;
    }
  }, [hasMore, events]);

  // -------------------------------------------------------------------------
  // Render helpers
  // -------------------------------------------------------------------------

  const renderItem = useCallback(
    ({ item }: { item: ActivityEvent }) => (
      <ActivityRow event={item} timestampTick={timestampTick} />
    ),
    [timestampTick],
  );

  const keyExtractor = useCallback((item: ActivityEvent) => item.id, []);

  const renderFooter = useCallback(() => {
    if (!loadingMore) return null;
    return (
      <View style={styles.footer}>
        <ActivityIndicator size="small" color={JARVIS.cyan} />
      </View>
    );
  }, [loadingMore]);

  const renderEmpty = useCallback(() => {
    if (loading) return null;
    return (
      <View style={styles.emptyContainer}>
        <Text style={styles.emptyText}>No activity yet</Text>
      </View>
    );
  }, [loading]);

  // -------------------------------------------------------------------------
  // Loading state
  // -------------------------------------------------------------------------

  if (loading) {
    return (
      <View style={styles.centered}>
        <ActivityIndicator size="large" color={JARVIS.cyan} />
      </View>
    );
  }

  // -------------------------------------------------------------------------
  // Error state
  // -------------------------------------------------------------------------

  if (error && events.length === 0) {
    return (
      <View style={styles.centered}>
        <Text style={styles.errorText}>{error}</Text>
        <TouchableOpacity
          style={styles.retryButton}
          onPress={fetchFirstPage}
          accessibilityRole="button"
          accessibilityLabel="Retry loading activity"
        >
          <Text style={styles.retryButtonText}>Retry</Text>
        </TouchableOpacity>
      </View>
    );
  }

  // -------------------------------------------------------------------------
  // Main render
  // -------------------------------------------------------------------------

  return (
    <FlatList
      style={styles.container}
      contentContainerStyle={events.length === 0 ? styles.emptyList : undefined}
      data={events}
      renderItem={renderItem}
      keyExtractor={keyExtractor}
      onEndReached={handleLoadMore}
      onEndReachedThreshold={0.4}
      ListFooterComponent={renderFooter}
      ListEmptyComponent={renderEmpty}
      refreshControl={
        <RefreshControl
          refreshing={refreshing}
          onRefresh={handleRefresh}
          tintColor={JARVIS.cyan}
          colors={[JARVIS.cyan]}
        />
      }
      ItemSeparatorComponent={Separator}
    />
  );
}

// ---------------------------------------------------------------------------
// Separator
// ---------------------------------------------------------------------------

function Separator() {
  return <View style={styles.separator} />;
}

// ---------------------------------------------------------------------------
// Styles
// ---------------------------------------------------------------------------

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: JARVIS.bg,
  },
  centered: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: JARVIS.bg,
    padding: 20,
  },
  emptyList: {
    flexGrow: 1,
  },

  // Row
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 16,
    paddingVertical: 14,
    backgroundColor: JARVIS.surface,
    borderLeftWidth: 3,
    borderLeftColor: JARVIS.border,
  },
  icon: {
    fontSize: 18,
    width: 28,
    textAlign: 'center',
    color: JARVIS.cyan,
  },
  rowContent: {
    flex: 1,
    marginLeft: 8,
  },
  description: {
    fontSize: 15,
    color: JARVIS.text,
    lineHeight: 20,
  },
  timestamp: {
    fontSize: 12,
    color: JARVIS.textMuted,
    marginTop: 3,
    fontFamily: 'SpaceMono',
  },
  chevron: {
    fontSize: 22,
    color: JARVIS.textMuted,
    marginLeft: 8,
  },

  // Separator
  separator: {
    height: StyleSheet.hairlineWidth,
    backgroundColor: JARVIS.border,
    marginLeft: 52,
  },

  // Footer (loading more)
  footer: {
    paddingVertical: 16,
    alignItems: 'center',
  },

  // Empty state
  emptyContainer: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
  },
  emptyText: {
    fontSize: 16,
    color: JARVIS.textMuted,
  },

  // Error state
  errorText: {
    fontSize: 15,
    color: JARVIS.red,
    textAlign: 'center',
    marginBottom: 16,
  },
  retryButton: {
    borderWidth: 1,
    borderColor: JARVIS.cyan,
    borderRadius: 10,
    paddingVertical: 12,
    paddingHorizontal: 24,
    backgroundColor: 'transparent',
  },
  retryButtonText: {
    color: JARVIS.cyan,
    fontSize: 16,
    fontWeight: '600',
  },
});
