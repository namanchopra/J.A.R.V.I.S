import React, { useCallback, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  ScrollView,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from 'react-native';
import FontAwesome from '@expo/vector-icons/FontAwesome';
import { useFocusEffect } from 'expo-router';

import { ApiError, ApprovalRequest, awmApi } from '../lib/api';
import { JARVIS, jarvisStyles } from '../constants/Colors';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const POLL_INTERVAL_MS = 3_000;

/** Risk-level colors using the Jarvis palette */
const RISK_COLORS = {
  low: {
    border: JARVIS.cyan,
    badgeBg: JARVIS.cyanDim,
    badgeText: JARVIS.cyan,
  },
  medium: {
    border: JARVIS.amber,
    badgeBg: JARVIS.amberDim,
    badgeText: JARVIS.amber,
  },
  high: {
    border: JARVIS.red,
    badgeBg: JARVIS.redDim,
    badgeText: JARVIS.red,
  },
} as const;

// ---------------------------------------------------------------------------
// Per-card response state
// ---------------------------------------------------------------------------

type CardState =
  | { kind: 'idle' }
  | { kind: 'loading'; action: 'approve' | 'deny' }
  | { kind: 'already_responded' }
  | { kind: 'error'; message: string };

// ---------------------------------------------------------------------------
// ApprovalCard
// ---------------------------------------------------------------------------

function ApprovalCard({
  approval,
  onRespond,
}: {
  approval: ApprovalRequest;
  onRespond: (pid: number, response: 'y' | 'n') => void;
}) {
  const [cardState, setCardState] = useState<CardState>({ kind: 'idle' });
  const fadeAnim = useRef(new Animated.Value(1)).current;

  const risk = RISK_COLORS[approval.riskLevel];

  const handleRespond = useCallback(
    async (response: 'y' | 'n') => {
      const action = response === 'y' ? 'approve' : 'deny';
      setCardState({ kind: 'loading', action });

      try {
        await awmApi.respondToApproval(approval.pid, response);

        // Fade out then remove
        Animated.timing(fadeAnim, {
          toValue: 0,
          duration: 300,
          useNativeDriver: true,
        }).start(() => {
          onRespond(approval.pid, response);
        });
      } catch (err: unknown) {
        if (err instanceof ApiError && err.status === 404) {
          setCardState({ kind: 'already_responded' });
          // Auto-remove after a short delay
          setTimeout(() => {
            Animated.timing(fadeAnim, {
              toValue: 0,
              duration: 300,
              useNativeDriver: true,
            }).start(() => {
              onRespond(approval.pid, response);
            });
          }, 1500);
        } else {
          const message =
            err instanceof Error ? err.message : 'Something went wrong';
          setCardState({ kind: 'error', message });
        }
      }
    },
    [approval.pid, fadeAnim, onRespond],
  );

  const argsText = JSON.stringify(approval.args, null, 2);
  const isLoading = cardState.kind === 'loading';

  return (
    <Animated.View
      style={[
        styles.card,
        { opacity: fadeAnim, borderLeftColor: risk.border },
      ]}
    >
      {/* Tool name */}
      <Text style={styles.toolName}>{approval.toolName}</Text>

      {/* Risk badge */}
      <View style={[styles.riskBadge, { backgroundColor: risk.badgeBg }]}>
        <Text style={[styles.riskBadgeText, { color: risk.badgeText }]}>
          {approval.riskLevel.toUpperCase()}
        </Text>
      </View>

      {/* Args preview */}
      <View style={styles.argsContainer}>
        <Text style={styles.argsLabel}>Arguments</Text>
        <ScrollView
          style={styles.argsScroll}
          nestedScrollEnabled
          showsVerticalScrollIndicator
        >
          <Text style={styles.argsText}>{argsText}</Text>
        </ScrollView>
      </View>

      {/* Session ID */}
      <Text style={styles.sessionId}>Session: {approval.sessionId}</Text>

      {/* Already responded message */}
      {cardState.kind === 'already_responded' && (
        <View style={styles.alreadyRespondedBanner}>
          <FontAwesome name="info-circle" size={14} color={JARVIS.amber} />
          <Text style={styles.alreadyRespondedText}>
            Already responded to this request
          </Text>
        </View>
      )}

      {/* Error message */}
      {cardState.kind === 'error' && (
        <View style={styles.errorBanner}>
          <FontAwesome name="exclamation-triangle" size={14} color={JARVIS.red} />
          <Text style={styles.errorBannerText}>{cardState.message}</Text>
        </View>
      )}

      {/* Action buttons */}
      {cardState.kind !== 'already_responded' && (
        <View style={styles.actionsContainer}>
          <TouchableOpacity
            style={[
              styles.approveButton,
              isLoading && styles.buttonDisabled,
            ]}
            onPress={() => handleRespond('y')}
            disabled={isLoading}
            accessibilityRole="button"
            accessibilityLabel={`Approve ${approval.toolName}`}
          >
            {isLoading && cardState.action === 'approve' ? (
              <ActivityIndicator size="small" color={JARVIS.bg} />
            ) : (
              <Text style={styles.approveButtonText}>Approve</Text>
            )}
          </TouchableOpacity>

          <TouchableOpacity
            style={[
              styles.denyButton,
              isLoading && styles.buttonDisabled,
            ]}
            onPress={() => handleRespond('n')}
            disabled={isLoading}
            accessibilityRole="button"
            accessibilityLabel={`Deny ${approval.toolName}`}
          >
            {isLoading && cardState.action === 'deny' ? (
              <ActivityIndicator size="small" color={JARVIS.text} />
            ) : (
              <Text style={styles.denyButtonText}>Deny</Text>
            )}
          </TouchableOpacity>
        </View>
      )}
    </Animated.View>
  );
}

// ---------------------------------------------------------------------------
// Empty state
// ---------------------------------------------------------------------------

function EmptyState() {
  return (
    <View style={styles.emptyContainer}>
      <FontAwesome name="check-circle" size={64} color={JARVIS.green} />
      <Text style={styles.emptyTitle}>No pending approvals</Text>
      <Text style={styles.emptySubtitle}>
        You are all caught up. New requests will appear here automatically.
      </Text>
    </View>
  );
}

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function ApprovalsScreen() {
  const [approvals, setApprovals] = useState<ApprovalRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);

  // -------------------------------------------------------------------------
  // Fetch approvals
  // -------------------------------------------------------------------------

  const fetchApprovals = useCallback(async () => {
    try {
      const data = await awmApi.listApprovals();
      setApprovals(data);
      setFetchError(null);
    } catch (err: unknown) {
      const message =
        err instanceof Error ? err.message : 'Failed to load approvals';
      setFetchError(message);
    } finally {
      setLoading(false);
    }
  }, []);

  // -------------------------------------------------------------------------
  // Initial fetch + auto-poll every 3s while screen is focused
  // -------------------------------------------------------------------------

  useFocusEffect(
    useCallback(() => {
      // Fetch immediately when screen gains focus
      fetchApprovals();

      const interval = setInterval(fetchApprovals, POLL_INTERVAL_MS);

      return () => {
        clearInterval(interval);
      };
    }, [fetchApprovals]),
  );

  // -------------------------------------------------------------------------
  // Remove card from local list after respond
  // -------------------------------------------------------------------------

  const handleCardResponded = useCallback((pid: number) => {
    setApprovals((prev) => prev.filter((a) => a.pid !== pid));
  }, []);

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  if (loading) {
    return (
      <View style={styles.centerContainer}>
        <ActivityIndicator size="large" color={JARVIS.cyan} />
        <Text style={styles.loadingText}>Loading approvals...</Text>
      </View>
    );
  }

  if (fetchError !== null && approvals.length === 0) {
    return (
      <View style={styles.centerContainer}>
        <FontAwesome name="exclamation-triangle" size={48} color={JARVIS.red} />
        <Text style={styles.fetchErrorText}>{fetchError}</Text>
        <TouchableOpacity
          style={styles.retryButton}
          onPress={() => {
            setLoading(true);
            fetchApprovals();
          }}
          accessibilityRole="button"
          accessibilityLabel="Retry loading approvals"
        >
          <Text style={styles.retryButtonText}>Retry</Text>
        </TouchableOpacity>
      </View>
    );
  }

  if (approvals.length === 0) {
    return <EmptyState />;
  }

  return (
    <ScrollView
      style={styles.container}
      contentContainerStyle={styles.content}
    >
      {fetchError !== null && (
        <View style={styles.inlineFetchError}>
          <FontAwesome name="exclamation-circle" size={14} color={JARVIS.amber} />
          <Text style={styles.inlineFetchErrorText}>
            Refresh failed: {fetchError}
          </Text>
        </View>
      )}

      {approvals.map((approval) => (
        <ApprovalCard
          key={approval.pid}
          approval={approval}
          onRespond={handleCardResponded}
        />
      ))}
    </ScrollView>
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
  content: {
    padding: 16,
    paddingBottom: 40,
  },
  centerContainer: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: JARVIS.bg,
    padding: 32,
  },

  // Loading
  loadingText: {
    marginTop: 12,
    fontSize: 15,
    color: JARVIS.textMuted,
  },

  // Fetch error (full-screen)
  fetchErrorText: {
    marginTop: 16,
    fontSize: 15,
    color: JARVIS.red,
    textAlign: 'center',
  },
  retryButton: {
    marginTop: 20,
    borderWidth: 1,
    borderColor: JARVIS.cyan,
    borderRadius: 10,
    paddingVertical: 12,
    paddingHorizontal: 32,
    backgroundColor: 'transparent',
  },
  retryButtonText: {
    color: JARVIS.cyan,
    fontSize: 16,
    fontWeight: '600',
  },

  // Inline fetch error (when cards are already visible)
  inlineFetchError: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.amberDim,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(255, 184, 0, 0.3)',
    paddingVertical: 10,
    paddingHorizontal: 14,
    marginBottom: 12,
  },
  inlineFetchErrorText: {
    color: JARVIS.amber,
    fontSize: 13,
    fontWeight: '500',
    marginLeft: 8,
    flex: 1,
  },

  // Card
  card: {
    backgroundColor: JARVIS.surface,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: JARVIS.border,
    borderLeftWidth: 3,
    padding: 16,
    marginBottom: 16,
  },

  // Tool name
  toolName: {
    fontSize: 20,
    fontWeight: '700',
    color: JARVIS.text,
    fontFamily: 'SpaceMono',
    marginBottom: 8,
  },

  // Risk badge
  riskBadge: {
    alignSelf: 'flex-start',
    borderRadius: 6,
    paddingHorizontal: 10,
    paddingVertical: 4,
    marginBottom: 12,
  },
  riskBadgeText: {
    fontSize: 11,
    fontWeight: '700',
    letterSpacing: 0.5,
    fontFamily: 'SpaceMono',
  },

  // Args
  argsContainer: {
    marginBottom: 12,
  },
  argsLabel: {
    fontSize: 12,
    fontWeight: '600',
    color: JARVIS.textMuted,
    textTransform: 'uppercase',
    letterSpacing: 0.5,
    marginBottom: 6,
  },
  argsScroll: {
    maxHeight: 150,
    backgroundColor: JARVIS.elevated,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: JARVIS.border,
    padding: 10,
  },
  argsText: {
    fontFamily: 'SpaceMono',
    fontSize: 12,
    color: JARVIS.textSecondary,
    lineHeight: 18,
  },

  // Session ID
  sessionId: {
    fontSize: 12,
    color: JARVIS.textMuted,
    fontFamily: 'SpaceMono',
    marginBottom: 12,
  },

  // Already responded banner
  alreadyRespondedBanner: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.amberDim,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(255, 184, 0, 0.3)',
    paddingVertical: 10,
    paddingHorizontal: 14,
    marginBottom: 8,
  },
  alreadyRespondedText: {
    color: JARVIS.amber,
    fontSize: 13,
    fontWeight: '500',
    marginLeft: 8,
  },

  // Error banner
  errorBanner: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.redDim,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: 'rgba(255, 71, 87, 0.3)',
    paddingVertical: 10,
    paddingHorizontal: 14,
    marginBottom: 8,
  },
  errorBannerText: {
    color: JARVIS.red,
    fontSize: 13,
    fontWeight: '500',
    marginLeft: 8,
    flex: 1,
  },

  // Action buttons
  actionsContainer: {
    gap: 8,
    marginTop: 4,
  },
  approveButton: {
    backgroundColor: JARVIS.green,
    borderRadius: 10,
    paddingVertical: 14,
    alignItems: 'center',
  },
  denyButton: {
    backgroundColor: JARVIS.red,
    borderRadius: 10,
    paddingVertical: 14,
    alignItems: 'center',
  },
  approveButtonText: {
    color: JARVIS.bg,
    fontSize: 16,
    fontWeight: '700',
  },
  denyButtonText: {
    color: JARVIS.text,
    fontSize: 16,
    fontWeight: '700',
  },
  buttonDisabled: {
    opacity: 0.6,
  },

  // Empty state
  emptyContainer: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: JARVIS.bg,
    padding: 32,
  },
  emptyTitle: {
    fontSize: 20,
    fontWeight: '700',
    color: JARVIS.text,
    marginTop: 20,
  },
  emptySubtitle: {
    fontSize: 14,
    color: JARVIS.textMuted,
    textAlign: 'center',
    marginTop: 8,
    lineHeight: 20,
  },
});
