import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  AppState,
  type AppStateStatus,
  KeyboardAvoidingView,
  type NativeScrollEvent,
  type NativeSyntheticEvent,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import { Stack, useLocalSearchParams } from 'expo-router';
import FontAwesome from '@expo/vector-icons/FontAwesome';

import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { type Session, awmApi } from '../../lib/api';
import { storage } from '../../lib/storage';
import { JARVIS } from '../../constants/Colors';

// ---------------------------------------------------------------------------
// Command history
// ---------------------------------------------------------------------------

const MAX_HISTORY = 10;

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const MAX_RECONNECT_ATTEMPTS = 3;
const MAX_TERMINAL_LINES = 500;

// How close to the bottom (in points) the user must be for auto-scroll to
// remain active. A small threshold avoids fighting minor scroll jitter.
const AUTO_SCROLL_THRESHOLD = 40;

// ---------------------------------------------------------------------------
// Status pill colours
// ---------------------------------------------------------------------------

type SessionStatus = Session['status'];

const STATUS_CONFIG: Record<string, { bg: string; fg: string; label: string }> = {
  launching: { bg: JARVIS.cyanDim, fg: JARVIS.cyan, label: 'Launching' },
  running: { bg: JARVIS.greenDim, fg: JARVIS.green, label: 'Running' },
  paused: { bg: JARVIS.elevated, fg: JARVIS.textSecondary, label: 'Paused' },
  completed: { bg: JARVIS.greenDim, fg: JARVIS.green, label: 'Completed' },
  failed: { bg: JARVIS.redDim, fg: JARVIS.red, label: 'Failed' },
  'needs-input': { bg: JARVIS.amberDim, fg: JARVIS.amber, label: 'Needs Input' },
};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Extract the last path component as a short repo name. */
function repoName(repoPath: string): string {
  const parts = repoPath.replace(/\/+$/, '').split('/');
  return parts[parts.length - 1] ?? repoPath;
}

/** Human-readable relative time, e.g. "3m ago" or "2h 15m ago". */
function relativeTime(isoDate: string): string {
  const diffMs = Date.now() - new Date(isoDate).getTime();
  if (diffMs < 0) return 'just now';

  const totalSeconds = Math.floor(diffMs / 1000);
  if (totalSeconds < 60) return `${totalSeconds}s ago`;

  const totalMinutes = Math.floor(totalSeconds / 60);
  if (totalMinutes < 60) return `${totalMinutes}m ago`;

  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  return minutes > 0 ? `${hours}h ${minutes}m ago` : `${hours}h ago`;
}

/** Running duration string. */
function duration(isoStart: string): string {
  const diffMs = Date.now() - new Date(isoStart).getTime();
  const totalSeconds = Math.max(0, Math.floor(diffMs / 1000));
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.floor((totalSeconds % 3600) / 60);
  const s = totalSeconds % 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function StatusPill({ status }: { status: SessionStatus }) {
  const cfg = STATUS_CONFIG[status] ?? { bg: JARVIS.elevated, fg: JARVIS.textSecondary, label: status };
  return (
    <View style={[styles.pill, { backgroundColor: cfg.bg, borderColor: cfg.fg }]}>
      <View style={[styles.pillDot, { backgroundColor: cfg.fg }]} />
      <Text style={[styles.pillText, { color: cfg.fg }]}>{cfg.label}</Text>
    </View>
  );
}

function InfoChip({ icon, text }: { icon: React.ComponentProps<typeof FontAwesome>['name']; text: string }) {
  return (
    <View style={styles.infoChip}>
      <FontAwesome name={icon} size={12} color={JARVIS.textSecondary} style={styles.infoChipIcon} />
      <Text style={styles.infoChipText}>{text}</Text>
    </View>
  );
}

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function SessionDetailScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const insets = useSafeAreaInsets();

  // Session data
  const [session, setSession] = useState<Session | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [stopping, setStopping] = useState(false);

  // Terminal output
  const [lines, setLines] = useState<string[]>([]);
  const scrollRef = useRef<ScrollView>(null);
  const userScrolledUp = useRef(false);

  // WebSocket state
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectAttempts = useRef(0);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [wsConnected, setWsConnected] = useState(false);

  // Track the current AppState so we can reconnect when foregrounded
  const appStateRef = useRef<AppStateStatus>(AppState.currentState);

  // Command input state
  const [commandText, setCommandText] = useState('');
  const [sending, setSending] = useState(false);
  const [commandHistory, setCommandHistory] = useState<string[]>([]);
  const [showHistory, setShowHistory] = useState(false);
  const inputRef = useRef<TextInput>(null);

  // -------------------------------------------------------------------------
  // Fetch session on mount
  // -------------------------------------------------------------------------

  const fetchSession = useCallback(async () => {
    if (!id) return;
    try {
      setLoadError(null);
      const data = await awmApi.getSession(id);
      setSession(data);
    } catch (err: unknown) {
      setLoadError(err instanceof Error ? err.message : 'Failed to load session');
    }
  }, [id]);

  useEffect(() => {
    fetchSession();
  }, [fetchSession]);

  // -------------------------------------------------------------------------
  // WebSocket connection
  // -------------------------------------------------------------------------

  const connectWs = useCallback(async () => {
    if (!session) return;

    // Clean up any existing connection
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
    const wsEndpoint = `${wsUrl}/ws/sessions/${session.id}/output?token=${encodeURIComponent(token ?? '')}`;

    const ws = new WebSocket(wsEndpoint);
    wsRef.current = ws;

    ws.onopen = () => {
      setWsConnected(true);
      reconnectAttempts.current = 0;
    };

    ws.onmessage = (event: WebSocketMessageEvent) => {
      const text = typeof event.data === 'string' ? event.data : '';
      setLines((prev) => {
        const next = [...prev, text];
        return next.length > MAX_TERMINAL_LINES ? next.slice(-MAX_TERMINAL_LINES) : next;
      });
    };

    ws.onclose = () => {
      setWsConnected(false);
      scheduleReconnect();
    };

    ws.onerror = () => {
      // onclose will fire after onerror, which triggers reconnect
    };
  }, [session]);

  // -------------------------------------------------------------------------
  // Reconnect with exponential backoff
  // -------------------------------------------------------------------------

  const scheduleReconnect = useCallback(() => {
    if (reconnectAttempts.current >= MAX_RECONNECT_ATTEMPTS) return;

    const delay = 1000 * Math.pow(2, reconnectAttempts.current);
    reconnectAttempts.current += 1;

    reconnectTimer.current = setTimeout(() => {
      connectWs();
    }, delay);
  }, [connectWs]);

  // -------------------------------------------------------------------------
  // Connect WS when session is loaded
  // -------------------------------------------------------------------------

  useEffect(() => {
    if (!session) return;

    // Only connect for running / needs-input sessions
    if (session.status === 'running' || session.status === 'needs-input') {
      connectWs();
    }

    return () => {
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current);
        reconnectTimer.current = null;
      }
      if (wsRef.current) {
        wsRef.current.onopen = null;
        wsRef.current.onmessage = null;
        wsRef.current.onclose = null;
        wsRef.current.onerror = null;
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [session, connectWs]);

  // -------------------------------------------------------------------------
  // AppState listener -- reconnect when app returns to foreground
  // -------------------------------------------------------------------------

  useEffect(() => {
    const subscription = AppState.addEventListener('change', (nextState: AppStateStatus) => {
      const wasBg = appStateRef.current.match(/inactive|background/);
      appStateRef.current = nextState;

      if (wasBg && nextState === 'active') {
        // Reset reconnect counter and try again
        reconnectAttempts.current = 0;
        connectWs();
      }
    });

    return () => subscription.remove();
  }, [connectWs]);

  // -------------------------------------------------------------------------
  // Auto-scroll terminal to bottom
  // -------------------------------------------------------------------------

  const handleScroll = useCallback((event: NativeSyntheticEvent<NativeScrollEvent>) => {
    const { layoutMeasurement, contentSize, contentOffset } = event.nativeEvent;
    const distanceFromBottom = contentSize.height - layoutMeasurement.height - contentOffset.y;
    userScrolledUp.current = distanceFromBottom > AUTO_SCROLL_THRESHOLD;
  }, []);

  useEffect(() => {
    if (!userScrolledUp.current && lines.length > 0) {
      // requestAnimationFrame ensures layout has settled
      requestAnimationFrame(() => {
        scrollRef.current?.scrollToEnd({ animated: false });
      });
    }
  }, [lines.length]);

  // -------------------------------------------------------------------------
  // Stop session handler
  // -------------------------------------------------------------------------

  const handleStop = useCallback(async () => {
    if (!id || !session || stopping) return;

    setStopping(true);

    // Optimistic: show completed status immediately
    setSession((prev) => (prev ? { ...prev, status: 'completed' as const } : prev));

    try {
      await awmApi.stopSession(id);
    } catch {
      // Revert on failure -- refetch actual status
      await fetchSession();
    } finally {
      setStopping(false);
    }
  }, [id, session, stopping, fetchSession]);

  // -------------------------------------------------------------------------
  // Send command to session
  // -------------------------------------------------------------------------

  const handleSendCommand = useCallback(async () => {
    const trimmed = commandText.trim();
    if (!trimmed || !id || sending) return;

    setSending(true);
    try {
      await awmApi.sendToSession(id, trimmed);

      // Add to history (deduplicate, keep last MAX_HISTORY)
      setCommandHistory((prev) => {
        const filtered = prev.filter((cmd) => cmd !== trimmed);
        return [trimmed, ...filtered].slice(0, MAX_HISTORY);
      });

      setCommandText('');
      setShowHistory(false);
    } catch (err: unknown) {
      Alert.alert(
        'Send Failed',
        err instanceof Error ? err.message : 'Could not send command',
      );
    } finally {
      setSending(false);
    }
  }, [commandText, id, sending]);

  const handleHistorySelect = useCallback((cmd: string) => {
    setCommandText(cmd);
    setShowHistory(false);
    inputRef.current?.focus();
  }, []);

  // -------------------------------------------------------------------------
  // Loading / error states
  // -------------------------------------------------------------------------

  if (loadError) {
    return (
      <>
        <Stack.Screen options={{ title: 'Session', headerStyle: { backgroundColor: JARVIS.surface }, headerTintColor: JARVIS.cyan, headerTitleStyle: { color: JARVIS.text }, headerShadowVisible: false }} />
        <View style={styles.centeredContainer}>
          <FontAwesome name="exclamation-triangle" size={32} color={JARVIS.red} />
          <Text style={styles.errorText}>{loadError}</Text>
          <TouchableOpacity style={styles.retryButton} onPress={fetchSession}>
            <Text style={styles.retryButtonText}>Retry</Text>
          </TouchableOpacity>
        </View>
      </>
    );
  }

  if (!session) {
    return (
      <>
        <Stack.Screen options={{ title: 'Session', headerStyle: { backgroundColor: JARVIS.surface }, headerTintColor: JARVIS.cyan, headerTitleStyle: { color: JARVIS.text }, headerShadowVisible: false }} />
        <View style={styles.centeredContainer}>
          <ActivityIndicator size="large" color={JARVIS.cyan} />
          <Text style={styles.loadingText}>Loading session...</Text>
        </View>
      </>
    );
  }

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  const isActive = session.status === 'running' || session.status === 'needs-input';

  return (
    <>
      <Stack.Screen
        options={{
          title: repoName(session.repoPath),
          headerBackTitle: 'Back',
          headerStyle: { backgroundColor: JARVIS.surface },
          headerTintColor: JARVIS.cyan,
          headerTitleStyle: { color: JARVIS.text },
          headerShadowVisible: false,
        }}
      />

      <KeyboardAvoidingView
        style={styles.container}
        behavior={Platform.OS === 'ios' ? 'padding' : 'height'}
        keyboardVerticalOffset={Platform.OS === 'ios' ? 90 : 0}
      >
        {/* Header row: status pill + stop button */}
        <View style={styles.headerRow}>
          <StatusPill status={session.status} />
          {isActive && (
            <TouchableOpacity
              style={styles.stopButton}
              onPress={handleStop}
              disabled={stopping}
              accessibilityRole="button"
              accessibilityLabel="Stop session"
            >
              {stopping ? (
                <ActivityIndicator size="small" color={JARVIS.red} />
              ) : (
                <>
                  <FontAwesome name="stop" size={12} color={JARVIS.red} style={styles.stopIcon} />
                  <Text style={styles.stopButtonText}>Stop</Text>
                </>
              )}
            </TouchableOpacity>
          )}
        </View>

        {/* Session info row */}
        <View style={styles.infoRow}>
          <InfoChip icon="code" text={session.agentType} />
          <InfoChip icon="clock-o" text={relativeTime(session.startedAt)} />
          <InfoChip icon="hourglass-half" text={duration(session.startedAt)} />
        </View>

        {/* WebSocket connection indicator */}
        {isActive && (
          <View style={styles.wsIndicatorRow}>
            <View
              style={[
                styles.wsIndicatorDot,
                { backgroundColor: wsConnected ? JARVIS.green : JARVIS.red },
              ]}
            />
            <Text style={styles.wsIndicatorText}>
              {wsConnected ? 'Live' : 'Disconnected'}
            </Text>
          </View>
        )}

        {/* Terminal output pane */}
        <View style={styles.terminalContainer}>
          <ScrollView
            ref={scrollRef}
            style={styles.terminalScroll}
            contentContainerStyle={styles.terminalContent}
            onScroll={handleScroll}
            scrollEventThrottle={64}
          >
            {lines.length === 0 ? (
              <Text style={styles.terminalPlaceholder}>
                {isActive ? 'Waiting for output...' : 'No output captured.'}
              </Text>
            ) : (
              <Text style={styles.terminalText} selectable>
                {lines.join('\n')}
              </Text>
            )}
          </ScrollView>
        </View>

        {/* Command input bar -- visible only for active sessions */}
        {isActive && (
          <View style={[styles.inputBarWrapper, { paddingBottom: Math.max(insets.bottom, 8) }]}>
            {/* Command history suggestions */}
            {showHistory && commandHistory.length > 0 && (
              <ScrollView
                horizontal
                showsHorizontalScrollIndicator={false}
                style={styles.historyRow}
                contentContainerStyle={styles.historyContent}
                keyboardShouldPersistTaps="always"
              >
                {commandHistory.map((cmd, idx) => (
                  <TouchableOpacity
                    key={`${cmd}-${idx}`}
                    style={styles.historyChip}
                    onPress={() => handleHistorySelect(cmd)}
                    accessibilityRole="button"
                    accessibilityLabel={`Use previous command: ${cmd}`}
                  >
                    <FontAwesome name="history" size={10} color={JARVIS.textMuted} style={styles.historyChipIcon} />
                    <Text style={styles.historyChipText} numberOfLines={1}>
                      {cmd}
                    </Text>
                  </TouchableOpacity>
                ))}
              </ScrollView>
            )}

            <View style={styles.inputBar}>
              {/* History toggle */}
              {commandHistory.length > 0 && (
                <TouchableOpacity
                  style={styles.historyToggle}
                  onPress={() => setShowHistory((v) => !v)}
                  accessibilityRole="button"
                  accessibilityLabel="Toggle command history"
                >
                  <FontAwesome
                    name="clock-o"
                    size={16}
                    color={showHistory ? JARVIS.cyan : JARVIS.textMuted}
                  />
                </TouchableOpacity>
              )}

              <TextInput
                ref={inputRef}
                style={styles.commandInput}
                value={commandText}
                onChangeText={setCommandText}
                placeholder="Send command..."
                placeholderTextColor={JARVIS.textMuted}
                returnKeyType="send"
                onSubmitEditing={handleSendCommand}
                editable={!sending}
                autoCapitalize="none"
                autoCorrect={false}
                accessibilityLabel="Command input"
              />

              <TouchableOpacity
                style={[
                  styles.sendButton,
                  (!commandText.trim() || sending) && styles.sendButtonDisabled,
                ]}
                onPress={handleSendCommand}
                disabled={!commandText.trim() || sending}
                accessibilityRole="button"
                accessibilityLabel="Send command"
              >
                {sending ? (
                  <ActivityIndicator size="small" color={JARVIS.bg} />
                ) : (
                  <FontAwesome name="paper-plane" size={14} color={JARVIS.bg} />
                )}
              </TouchableOpacity>
            </View>
          </View>
        )}
      </KeyboardAvoidingView>
    </>
  );
}

// ---------------------------------------------------------------------------
// Styles
// ---------------------------------------------------------------------------

const styles = StyleSheet.create({
  // Layout
  container: {
    flex: 1,
    backgroundColor: JARVIS.bg,
  },
  centeredContainer: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: JARVIS.bg,
    padding: 20,
  },

  // Header row
  headerRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 16,
    paddingTop: 12,
    paddingBottom: 8,
  },

  // Status pill
  pill: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 12,
    paddingVertical: 5,
    borderRadius: 14,
    borderWidth: 1,
  },
  pillDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    marginRight: 6,
  },
  pillText: {
    fontSize: 13,
    fontWeight: '600',
  },

  // Stop button
  stopButton: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.redDim,
    borderWidth: 1,
    borderColor: JARVIS.red,
    paddingHorizontal: 14,
    paddingVertical: 7,
    borderRadius: 8,
  },
  stopIcon: {
    marginRight: 6,
  },
  stopButtonText: {
    color: JARVIS.red,
    fontSize: 14,
    fontWeight: '600',
  },

  // Info row
  infoRow: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    paddingHorizontal: 16,
    paddingBottom: 8,
    gap: 8,
  },
  infoChip: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.elevated,
    borderWidth: 1,
    borderColor: JARVIS.border,
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 12,
  },
  infoChipIcon: {
    marginRight: 5,
  },
  infoChipText: {
    fontSize: 12,
    color: JARVIS.textSecondary,
    fontWeight: '500',
  },

  // WebSocket indicator
  wsIndicatorRow: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 16,
    paddingBottom: 8,
  },
  wsIndicatorDot: {
    width: 7,
    height: 7,
    borderRadius: 3.5,
    marginRight: 6,
  },
  wsIndicatorText: {
    fontSize: 12,
    color: JARVIS.textSecondary,
    fontWeight: '500',
  },

  // Terminal pane
  terminalContainer: {
    flex: 1,
    marginHorizontal: 12,
    marginBottom: 12,
    borderRadius: 10,
    overflow: 'hidden',
    backgroundColor: JARVIS.surface,
    borderWidth: 1,
    borderColor: JARVIS.border,
  },
  terminalScroll: {
    flex: 1,
  },
  terminalContent: {
    padding: 12,
    paddingBottom: 20,
  },
  terminalText: {
    fontFamily: 'SpaceMono',
    fontSize: 12,
    lineHeight: 18,
    color: JARVIS.cyan,
  },
  terminalPlaceholder: {
    fontFamily: 'SpaceMono',
    fontSize: 12,
    color: JARVIS.textMuted,
    fontStyle: 'italic',
  },

  // Loading / error states
  loadingText: {
    marginTop: 12,
    fontSize: 15,
    color: JARVIS.textSecondary,
  },
  errorText: {
    marginTop: 12,
    fontSize: 15,
    color: JARVIS.red,
    textAlign: 'center',
  },
  retryButton: {
    marginTop: 16,
    backgroundColor: JARVIS.cyanDim,
    borderWidth: 1,
    borderColor: JARVIS.cyan,
    paddingHorizontal: 24,
    paddingVertical: 10,
    borderRadius: 8,
  },
  retryButtonText: {
    color: JARVIS.cyan,
    fontSize: 15,
    fontWeight: '600',
  },

  // Command input bar
  inputBarWrapper: {
    backgroundColor: JARVIS.surface,
    borderTopWidth: 1,
    borderTopColor: JARVIS.border,
  },
  inputBar: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 12,
    paddingVertical: 8,
    gap: 8,
  },
  historyToggle: {
    padding: 6,
  },
  commandInput: {
    flex: 1,
    backgroundColor: JARVIS.elevated,
    borderWidth: 1,
    borderColor: JARVIS.borderActive,
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 14,
    fontFamily: 'SpaceMono',
    color: JARVIS.text,
  },
  sendButton: {
    backgroundColor: JARVIS.cyan,
    width: 38,
    height: 38,
    borderRadius: 8,
    alignItems: 'center',
    justifyContent: 'center',
  },
  sendButtonDisabled: {
    opacity: 0.4,
  },

  // Command history
  historyRow: {
    maxHeight: 36,
    borderBottomWidth: 1,
    borderBottomColor: JARVIS.border,
  },
  historyContent: {
    paddingHorizontal: 12,
    paddingVertical: 6,
    gap: 8,
    alignItems: 'center',
  },
  historyChip: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.elevated,
    borderWidth: 1,
    borderColor: JARVIS.border,
    borderRadius: 12,
    paddingHorizontal: 10,
    paddingVertical: 4,
    marginRight: 6,
  },
  historyChipIcon: {
    marginRight: 5,
  },
  historyChipText: {
    fontSize: 12,
    fontFamily: 'SpaceMono',
    color: JARVIS.textSecondary,
    maxWidth: 140,
  },
});
