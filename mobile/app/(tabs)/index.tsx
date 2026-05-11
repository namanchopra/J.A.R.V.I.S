import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  Easing,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from 'react-native';
import FontAwesome from '@expo/vector-icons/FontAwesome';
import { router, useFocusEffect } from 'expo-router';

import {
  awmApi,
  DashboardResponse,
  DashboardStats,
  Task,
} from '../../lib/api';
import { JARVIS, jarvisStyles } from '@/constants/Colors';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const POLL_INTERVAL_MS = 5000;

const STAT_CARDS: {
  key: keyof DashboardStats;
  label: string;
  color: string;
  bgColor: string;
  icon: React.ComponentProps<typeof FontAwesome>['name'];
}[] = [
  { key: 'running', label: 'Running', color: JARVIS.green, bgColor: JARVIS.greenDim, icon: 'play-circle' },
  { key: 'needsInput', label: 'Needs Input', color: JARVIS.amber, bgColor: JARVIS.amberDim, icon: 'question-circle' },
  { key: 'pending', label: 'Pending', color: JARVIS.cyan, bgColor: JARVIS.cyanDim, icon: 'clock-o' },
  { key: 'done', label: 'Done', color: JARVIS.textMuted, bgColor: JARVIS.elevated, icon: 'check-circle' },
  { key: 'failed', label: 'Failed', color: JARVIS.red, bgColor: JARVIS.redDim, icon: 'times-circle' },
];

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Extract the last path segment from a repo path (e.g., "/Users/me/project" -> "project"). */
function repoName(repoPath: string): string {
  const segments = repoPath.replace(/\/+$/, '').split('/');
  return segments[segments.length - 1] || repoPath;
}

/** Format elapsed time from an ISO date string to now (e.g., "2m 34s" or "1h 05m"). */
function formatDuration(isoDate: string): string {
  const startMs = new Date(isoDate).getTime();
  const nowMs = Date.now();
  const diffSec = Math.max(0, Math.floor((nowMs - startMs) / 1000));

  const hours = Math.floor(diffSec / 3600);
  const minutes = Math.floor((diffSec % 3600) / 60);
  const seconds = diffSec % 60;

  if (hours > 0) {
    return `${hours}h ${String(minutes).padStart(2, '0')}m`;
  }
  return `${minutes}m ${String(seconds).padStart(2, '0')}s`;
}

/** Map task status to a dot color. */
function statusDotColor(status: Task['status']): string {
  switch (status) {
    case 'running':
      return JARVIS.green;
    case 'needs-input':
      return JARVIS.amber;
    case 'failed':
      return JARVIS.red;
    case 'done':
      return JARVIS.textMuted;
    case 'pending':
      return JARVIS.cyan;
    default:
      return JARVIS.textMuted;
  }
}

/** Map task status to a left-border color for list items. */
function statusBorderColor(status: Task['status']): string {
  switch (status) {
    case 'running':
      return JARVIS.green;
    case 'needs-input':
      return JARVIS.amber;
    case 'failed':
      return JARVIS.red;
    case 'pending':
      return JARVIS.cyan;
    default:
      return JARVIS.border;
  }
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function StatCard({
  label,
  value,
  color,
  bgColor,
  icon,
}: {
  label: string;
  value: number;
  color: string;
  bgColor: string;
  icon: React.ComponentProps<typeof FontAwesome>['name'];
}) {
  return (
    <View
      style={[styles.statCard, { backgroundColor: bgColor, borderColor: color }]}
      accessibilityLabel={`${label}: ${value}`}
    >
      <FontAwesome name={icon} size={16} color={color} style={styles.statIcon} />
      <Text style={[styles.statValue, { color }]}>{value}</Text>
      <Text style={[styles.statLabel, { color }]} numberOfLines={1}>
        {label}
      </Text>
    </View>
  );
}

function PulsingDot({ color }: { color: string }) {
  const opacity = useRef(new Animated.Value(1)).current;

  useEffect(() => {
    const animation = Animated.loop(
      Animated.sequence([
        Animated.timing(opacity, {
          toValue: 0.3,
          duration: 800,
          easing: Easing.inOut(Easing.ease),
          useNativeDriver: true,
        }),
        Animated.timing(opacity, {
          toValue: 1,
          duration: 800,
          easing: Easing.inOut(Easing.ease),
          useNativeDriver: true,
        }),
      ]),
    );
    animation.start();
    return () => animation.stop();
  }, [opacity]);

  return (
    <Animated.View
      style={[styles.statusDot, { backgroundColor: color, opacity }]}
    />
  );
}

function StatusDot({ status }: { status: Task['status'] }) {
  const color = statusDotColor(status);
  if (status === 'running') {
    return <PulsingDot color={color} />;
  }
  return <View style={[styles.statusDot, { backgroundColor: color }]} />;
}

function AgentBadge({ agentType }: { agentType: string }) {
  return (
    <View style={styles.agentBadge}>
      <Text style={styles.agentBadgeText}>{agentType.toUpperCase()}</Text>
    </View>
  );
}

function TaskRow({ task }: { task: Task }) {
  const handlePress = useCallback(() => {
    router.push(`/tasks/${task.id}`);
  }, [task.id]);

  return (
    <TouchableOpacity
      style={[styles.sessionRow, { borderLeftColor: statusBorderColor(task.status) }]}
      onPress={handlePress}
      activeOpacity={0.7}
      accessibilityRole="button"
      accessibilityLabel={`Task ${task.name}, status ${task.status}`}
    >
      <StatusDot status={task.status} />
      <View style={styles.sessionInfo}>
        <View style={styles.sessionTopRow}>
          <Text style={styles.sessionRepoName} numberOfLines={1}>
            {task.name || repoName(task.repoPath)}
          </Text>
          <AgentBadge agentType={task.agentType} />
        </View>
        <Text style={styles.sessionDuration}>
          {repoName(task.repoPath)} · {formatDuration(task.createdAt)}
        </Text>
      </View>
      <FontAwesome name="chevron-right" size={14} color={JARVIS.textMuted} />
    </TouchableOpacity>
  );
}

function EmptyState() {
  return (
    <View style={styles.emptyContainer}>
      <FontAwesome name="terminal" size={48} color={JARVIS.textMuted} />
      <Text style={styles.emptyText}>No active tasks</Text>
    </View>
  );
}

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function DashboardScreen() {
  const [stats, setStats] = useState<DashboardStats>({
    total: 0,
    running: 0,
    pending: 0,
    needsInput: 0,
    done: 0,
    failed: 0,
  });
  const [activeTasks, setActiveTasks] = useState<Task[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // -------------------------------------------------------------------------
  // Fetch dashboard data
  // -------------------------------------------------------------------------

  const fetchDashboard = useCallback(async (isRefresh = false) => {
    if (isRefresh) {
      setRefreshing(true);
    }

    try {
      const data: DashboardResponse = await awmApi.getDashboard();
      setStats(data.stats);
      setActiveTasks(data.activeTasks ?? []);
      setError(null);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to load dashboard';
      setError(message);
    } finally {
      setLoading(false);
      if (isRefresh) {
        setRefreshing(false);
      }
    }
  }, []);

  // -------------------------------------------------------------------------
  // Initial load
  // -------------------------------------------------------------------------

  useEffect(() => {
    fetchDashboard();
  }, [fetchDashboard]);

  // -------------------------------------------------------------------------
  // Auto-poll every 5s while focused
  // -------------------------------------------------------------------------

  useFocusEffect(
    useCallback(() => {
      const intervalId = setInterval(() => {
        fetchDashboard();
      }, POLL_INTERVAL_MS);

      return () => clearInterval(intervalId);
    }, [fetchDashboard]),
  );

  // -------------------------------------------------------------------------
  // Pull-to-refresh handler
  // -------------------------------------------------------------------------

  const handleRefresh = useCallback(() => {
    fetchDashboard(true);
  }, [fetchDashboard]);

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  if (loading) {
    return (
      <View style={styles.centered}>
        <ActivityIndicator size="large" color={JARVIS.cyan} />
      </View>
    );
  }

  return (
    <ScrollView
      style={styles.container}
      contentContainerStyle={styles.content}
      refreshControl={
        <RefreshControl
          refreshing={refreshing}
          onRefresh={handleRefresh}
          tintColor={JARVIS.cyan}
          colors={[JARVIS.cyan]}
        />
      }
    >
      {/* Error banner */}
      {error !== null && (
        <View style={styles.errorBanner}>
          <FontAwesome name="exclamation-triangle" size={14} color={JARVIS.red} />
          <Text style={styles.errorText}>{error}</Text>
        </View>
      )}

      {/* Stat cards */}
      <View style={styles.statsRow}>
        {STAT_CARDS.map((card) => (
          <StatCard
            key={card.key}
            label={card.label}
            value={stats[card.key]}
            color={card.color}
            bgColor={card.bgColor}
            icon={card.icon}
          />
        ))}
      </View>

      {/* Active tasks section */}
      <Text style={styles.sectionHeader}>Active Tasks</Text>

      {activeTasks.length === 0 ? (
        <EmptyState />
      ) : (
        activeTasks.map((task) => (
          <TaskRow key={task.id} task={task} />
        ))
      )}
    </ScrollView>
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
  content: {
    padding: 16,
    paddingBottom: 40,
  },
  centered: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: JARVIS.bg,
  },

  // Error banner
  errorBanner: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.redDim,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: JARVIS.red,
    paddingVertical: 10,
    paddingHorizontal: 14,
    marginBottom: 16,
  },
  errorText: {
    color: JARVIS.red,
    fontSize: 14,
    fontWeight: '500',
    marginLeft: 8,
    flex: 1,
  },

  // Stat cards row
  statsRow: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    justifyContent: 'space-between',
    marginBottom: 24,
    gap: 8,
  },
  statCard: {
    alignItems: 'center',
    borderRadius: 8,
    borderWidth: 1,
    paddingVertical: 12,
    paddingHorizontal: 8,
    minWidth: 62,
    flex: 1,
  },
  statIcon: {
    marginBottom: 4,
  },
  statValue: {
    fontSize: 22,
    fontWeight: '700',
    fontFamily: 'SpaceMono',
  },
  statLabel: {
    fontSize: 10,
    fontWeight: '700',
    marginTop: 2,
    letterSpacing: 0.5,
    textTransform: 'uppercase',
  },

  // Section header
  sectionHeader: {
    fontSize: 11,
    fontWeight: '700',
    color: JARVIS.cyan,
    textTransform: 'uppercase',
    letterSpacing: 1.5,
    marginBottom: 12,
    fontFamily: 'SpaceMono',
  },

  // Session rows
  sessionRow: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.surface,
    borderRadius: 8,
    paddingVertical: 14,
    paddingHorizontal: 14,
    marginBottom: 10,
    borderWidth: 1,
    borderColor: JARVIS.border,
    borderLeftWidth: 3,
    borderLeftColor: JARVIS.border,
  },
  statusDot: {
    width: 10,
    height: 10,
    borderRadius: 5,
    marginRight: 12,
  },
  sessionInfo: {
    flex: 1,
    marginRight: 8,
  },
  sessionTopRow: {
    flexDirection: 'row',
    alignItems: 'center',
    marginBottom: 4,
  },
  sessionRepoName: {
    fontSize: 16,
    fontWeight: '600',
    color: JARVIS.text,
    flexShrink: 1,
    marginRight: 8,
  },
  sessionDuration: {
    fontSize: 13,
    color: JARVIS.textSecondary,
  },

  // Agent badge
  agentBadge: {
    backgroundColor: JARVIS.cyanDim,
    borderRadius: 6,
    borderWidth: 1,
    borderColor: JARVIS.border,
    paddingHorizontal: 8,
    paddingVertical: 2,
  },
  agentBadgeText: {
    fontSize: 10,
    fontWeight: '700',
    color: JARVIS.cyan,
    letterSpacing: 0.5,
    fontFamily: 'SpaceMono',
  },

  // Empty state
  emptyContainer: {
    alignItems: 'center',
    justifyContent: 'center',
    paddingVertical: 60,
  },
  emptyText: {
    fontSize: 16,
    color: JARVIS.textSecondary,
    marginTop: 12,
    fontFamily: 'SpaceMono',
  },
});
