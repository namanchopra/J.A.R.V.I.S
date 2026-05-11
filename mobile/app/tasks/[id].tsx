import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  AppState,
  type AppStateStatus,
  type NativeScrollEvent,
  type NativeSyntheticEvent,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { Stack, useLocalSearchParams } from 'expo-router';
import FontAwesome from '@expo/vector-icons/FontAwesome';

import { type Task, type SessionIndicator, awmApi } from '../../lib/api';
import { storage } from '../../lib/storage';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const MAX_RECONNECT_ATTEMPTS = 3;
const AUTO_SCROLL_THRESHOLD = 40;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function repoName(repoPath: string): string {
  const parts = repoPath.replace(/\/+$/, '').split('/');
  return parts[parts.length - 1] ?? repoPath;
}

function statusColor(status: Task['status']): { bg: string; fg: string } {
  switch (status) {
    case 'running':    return { bg: '#d4edda', fg: '#155724' };
    case 'needs-input': return { bg: '#fff3cd', fg: '#856404' };
    case 'failed':     return { bg: '#f8d7da', fg: '#721c24' };
    case 'done':       return { bg: '#e2e3e5', fg: '#383d41' };
    default:           return { bg: '#e9ecef', fg: '#495057' };
  }
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function StatusPill({ status }: { status: Task['status'] }) {
  const { bg, fg } = statusColor(status);
  return (
    <View style={[styles.pill, { backgroundColor: bg }]}>
      <Text style={[styles.pillText, { color: fg }]}>
        {status.replace('-', ' ').replace(/\b\w/g, (c) => c.toUpperCase())}
      </Text>
    </View>
  );
}

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function TaskDetailScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();

  const [task, setTask] = useState<Task | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [lines, setLines] = useState<string[]>([]);
  const scrollRef = useRef<ScrollView>(null);
  const userScrolledUp = useRef(false);

  const wsRef = useRef<WebSocket | null>(null);
  const reconnectAttempts = useRef(0);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [wsConnected, setWsConnected] = useState(false);
  const [noPid, setNoPid] = useState(false);

  const appStateRef = useRef<AppStateStatus>(AppState.currentState);

  // -------------------------------------------------------------------------
  // Load task
  // -------------------------------------------------------------------------

  const fetchTask = useCallback(async () => {
    if (!id) return;
    try {
      const data = await awmApi.getTask(id);
      setTask(data);
    } catch (err: unknown) {
      setLoadError(err instanceof Error ? err.message : 'Failed to load task');
    }
  }, [id]);

  useEffect(() => {
    fetchTask();
  }, [fetchTask]);

  // -------------------------------------------------------------------------
  // Find PID from indicators and connect WebSocket
  // -------------------------------------------------------------------------

  const connectWs = useCallback(async (pid: number) => {
    if (wsRef.current) {
      wsRef.current.onopen = null;
      wsRef.current.onmessage = null;
      wsRef.current.onclose = null;
      wsRef.current.onerror = null;
      wsRef.current.close();
      wsRef.current = null;
    }

    const serverUrl = await storage.getServerUrl();
    const token = await storage.getToken();
    const wsUrl = serverUrl.replace(/^http/, 'ws').replace(/^https/, 'wss');
    const wsEndpoint = `${wsUrl}/ws/sessions/${pid}/output?token=${encodeURIComponent(token ?? '')}`;

    const ws = new WebSocket(wsEndpoint);
    wsRef.current = ws;

    ws.onopen = () => {
      setWsConnected(true);
      reconnectAttempts.current = 0;
    };

    ws.onmessage = (event: WebSocketMessageEvent) => {
      const text = typeof event.data === 'string' ? event.data : '';
      setLines((prev) => [...prev, text]);
    };

    ws.onclose = () => {
      setWsConnected(false);
      if (reconnectAttempts.current < MAX_RECONNECT_ATTEMPTS) {
        const delay = Math.pow(2, reconnectAttempts.current) * 1000;
        reconnectAttempts.current += 1;
        reconnectTimer.current = setTimeout(() => connectWs(pid), delay);
      }
    };

    ws.onerror = () => {
      // onclose fires after onerror and handles reconnect
    };
  }, []);

  const findPidAndConnect = useCallback(async (repoPath: string) => {
    try {
      const indicators: SessionIndicator[] = await awmApi.getIndicators();
      const match = indicators.find(
        (ind) => ind.cwd === repoPath || ind.cwd.startsWith(repoPath),
      );
      if (!match) {
        setNoPid(true);
        return;
      }
      await connectWs(match.pid);
    } catch {
      setNoPid(true);
    }
  }, [connectWs]);

  useEffect(() => {
    if (task) {
      findPidAndConnect(task.repoPath);
    }
  }, [task, findPidAndConnect]);

  // -------------------------------------------------------------------------
  // Reconnect on foreground
  // -------------------------------------------------------------------------

  useEffect(() => {
    const sub = AppState.addEventListener('change', async (next: AppStateStatus) => {
      const prev = appStateRef.current;
      appStateRef.current = next;
      if (prev.match(/inactive|background/) && next === 'active' && task && !wsConnected) {
        reconnectAttempts.current = 0;
        findPidAndConnect(task.repoPath);
      }
    });
    return () => sub.remove();
  }, [task, wsConnected, findPidAndConnect]);

  // -------------------------------------------------------------------------
  // Cleanup on unmount
  // -------------------------------------------------------------------------

  useEffect(() => {
    return () => {
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current);
      if (wsRef.current) wsRef.current.close();
    };
  }, []);

  // -------------------------------------------------------------------------
  // Auto-scroll
  // -------------------------------------------------------------------------

  const handleScroll = useCallback((e: NativeSyntheticEvent<NativeScrollEvent>) => {
    const { layoutMeasurement, contentOffset, contentSize } = e.nativeEvent;
    const distanceFromBottom =
      contentSize.height - contentOffset.y - layoutMeasurement.height;
    userScrolledUp.current = distanceFromBottom > AUTO_SCROLL_THRESHOLD;
  }, []);

  useEffect(() => {
    if (!userScrolledUp.current) {
      scrollRef.current?.scrollToEnd({ animated: false });
    }
  }, [lines]);

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  if (loadError) {
    return (
      <View style={styles.centered}>
        <FontAwesome name="exclamation-circle" size={32} color="#dc3545" />
        <Text style={styles.errorText}>{loadError}</Text>
      </View>
    );
  }

  if (!task) {
    return (
      <View style={styles.centered}>
        <ActivityIndicator size="large" color="#2f95dc" />
      </View>
    );
  }

  return (
    <>
      <Stack.Screen
        options={{
          title: task.name || repoName(task.repoPath),
          headerShown: true,
        }}
      />

      <View style={styles.container}>
        {/* Task info header */}
        <View style={styles.header}>
          <View style={styles.headerRow}>
            <Text style={styles.repoName} numberOfLines={1}>
              {repoName(task.repoPath)}
            </Text>
            <StatusPill status={task.status} />
          </View>
          {task.name ? (
            <Text style={styles.taskName} numberOfLines={2}>
              {task.name}
            </Text>
          ) : null}
          <View style={styles.agentRow}>
            <FontAwesome name="code" size={12} color="#888" />
            <Text style={styles.agentText}>{task.agentType}</Text>
            {wsConnected && (
              <View style={styles.liveIndicator}>
                <View style={styles.liveDot} />
                <Text style={styles.liveText}>Live</Text>
              </View>
            )}
          </View>
        </View>

        {/* Terminal output */}
        {noPid ? (
          <View style={styles.noPidContainer}>
            <FontAwesome name="terminal" size={32} color="#ccc" />
            <Text style={styles.noPidText}>Terminal not available</Text>
            <Text style={styles.noPidSubtext}>
              Process is not tracked by CMux or is no longer running.
            </Text>
          </View>
        ) : (
          <ScrollView
            ref={scrollRef}
            style={styles.terminal}
            contentContainerStyle={styles.terminalContent}
            onScroll={handleScroll}
            scrollEventThrottle={100}
          >
            {lines.length === 0 ? (
              <ActivityIndicator
                size="small"
                color="#666"
                style={styles.terminalLoader}
              />
            ) : (
              lines.map((line, i) => (
                <Text key={i} style={styles.terminalLine} selectable>
                  {line}
                </Text>
              ))
            )}
          </ScrollView>
        )}
      </View>
    </>
  );
}

// ---------------------------------------------------------------------------
// Styles
// ---------------------------------------------------------------------------

const styles = StyleSheet.create({
  centered: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: '#f8f9fa',
    gap: 12,
  },
  errorText: {
    color: '#dc3545',
    fontSize: 15,
    textAlign: 'center',
    paddingHorizontal: 24,
  },

  container: {
    flex: 1,
    backgroundColor: '#1e1e1e',
  },

  // Header
  header: {
    backgroundColor: '#fff',
    padding: 16,
    borderBottomWidth: 1,
    borderBottomColor: '#eee',
    gap: 6,
  },
  headerRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  repoName: {
    fontSize: 17,
    fontWeight: '700',
    color: '#333',
    flexShrink: 1,
    marginRight: 8,
  },
  taskName: {
    fontSize: 14,
    color: '#555',
  },
  agentRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
  },
  agentText: {
    fontSize: 12,
    color: '#888',
  },

  // Live indicator
  liveIndicator: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 4,
    marginLeft: 8,
  },
  liveDot: {
    width: 7,
    height: 7,
    borderRadius: 4,
    backgroundColor: '#28a745',
  },
  liveText: {
    fontSize: 11,
    fontWeight: '600',
    color: '#28a745',
  },

  // Status pill
  pill: {
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 3,
  },
  pillText: {
    fontSize: 12,
    fontWeight: '600',
  },

  // Terminal
  terminal: {
    flex: 1,
    backgroundColor: '#1e1e1e',
  },
  terminalContent: {
    padding: 12,
    paddingBottom: 40,
  },
  terminalLine: {
    fontFamily: 'SpaceMono',
    fontSize: 12,
    color: '#d4d4d4',
    lineHeight: 18,
  },
  terminalLoader: {
    marginTop: 40,
  },

  // No PID state
  noPidContainer: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    gap: 12,
    backgroundColor: '#1e1e1e',
  },
  noPidText: {
    fontSize: 16,
    color: '#888',
    fontWeight: '600',
  },
  noPidSubtext: {
    fontSize: 13,
    color: '#555',
    textAlign: 'center',
    paddingHorizontal: 32,
  },
});
