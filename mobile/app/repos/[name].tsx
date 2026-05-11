import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import { Stack, useLocalSearchParams } from 'expo-router';
import FontAwesome from '@expo/vector-icons/FontAwesome';

import { type DiffFile, type DiffResult, type RepoInfo, awmApi } from '../../lib/api';
import { JARVIS } from '../../constants/Colors';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function shortPath(filePath: string): string {
  // Show last 3 segments at most for readability
  const parts = filePath.split('/');
  return parts.length > 3 ? '.../' + parts.slice(-3).join('/') : filePath;
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function BranchChip({ branch }: { branch: string }) {
  return (
    <View style={styles.branchChip}>
      <FontAwesome name="code-fork" size={12} color={JARVIS.cyan} />
      <Text style={styles.branchChipText}>{branch}</Text>
    </View>
  );
}

function DiffFileRow({ file }: { file: DiffFile }) {
  return (
    <View style={styles.fileRow}>
      <Text style={styles.filePath} numberOfLines={1}>
        {shortPath(file.path)}
      </Text>
      <View style={styles.fileStats}>
        {file.insertions > 0 && (
          <Text style={styles.insertionText}>+{file.insertions}</Text>
        )}
        {file.deletions > 0 && (
          <Text style={styles.deletionText}>-{file.deletions}</Text>
        )}
        {file.insertions === 0 && file.deletions === 0 && (
          <Text style={styles.noChangeText}>~</Text>
        )}
      </View>
    </View>
  );
}

function DiffSummaryCard({ diff }: { diff: DiffResult }) {
  const files = diff.files ?? [];
  const { filesChanged, insertions, deletions } = diff.stats;

  return (
    <View style={styles.card}>
      <View style={styles.cardHeader}>
        <FontAwesome name="files-o" size={14} color={JARVIS.cyan} />
        <Text style={styles.cardTitle}>Changes</Text>
        <View style={styles.statsBadge}>
          <Text style={styles.statsBadgeText}>
            {filesChanged} file{filesChanged !== 1 ? 's' : ''}
          </Text>
        </View>
      </View>

      {files.length === 0 ? (
        <View style={styles.emptyDiff}>
          <FontAwesome name="check-circle" size={20} color={JARVIS.textMuted} />
          <Text style={styles.emptyDiffText}>Working tree is clean</Text>
        </View>
      ) : (
        <>
          <View style={styles.fileList}>
            {files.map((file) => (
              <DiffFileRow key={file.path} file={file} />
            ))}
          </View>

          {/* Totals */}
          <View style={styles.totalsRow}>
            <Text style={styles.totalsLabel}>Total:</Text>
            <Text style={styles.totalInsertions}>+{insertions}</Text>
            <Text style={styles.totalDeletions}>-{deletions}</Text>
          </View>
        </>
      )}
    </View>
  );
}

function ActionButton({
  icon,
  label,
  onPress,
  loading,
  disabled,
  variant = 'default',
}: {
  icon: React.ComponentProps<typeof FontAwesome>['name'];
  label: string;
  onPress: () => void;
  loading?: boolean;
  disabled?: boolean;
  variant?: 'default' | 'primary' | 'success';
}) {
  const bgColor =
    variant === 'primary'
      ? JARVIS.cyan
      : variant === 'success'
        ? JARVIS.green
        : JARVIS.elevated;

  const textColor =
    variant === 'primary' || variant === 'success'
      ? JARVIS.bg
      : JARVIS.text;

  return (
    <TouchableOpacity
      style={[
        styles.actionButton,
        { backgroundColor: bgColor },
        disabled && styles.actionButtonDisabled,
      ]}
      onPress={onPress}
      disabled={disabled || loading}
      activeOpacity={0.7}
      accessibilityRole="button"
      accessibilityLabel={label}
    >
      {loading ? (
        <ActivityIndicator size="small" color={textColor} />
      ) : (
        <FontAwesome name={icon} size={16} color={textColor} style={styles.actionIcon} />
      )}
      <Text style={[styles.actionLabel, { color: textColor }]}>{label}</Text>
    </TouchableOpacity>
  );
}

function Toast({ message, type }: { message: string; type: 'success' | 'error' }) {
  const bg = type === 'success' ? JARVIS.greenDim : JARVIS.redDim;
  const fg = type === 'success' ? JARVIS.green : JARVIS.red;
  const icon = type === 'success' ? 'check-circle' : 'times-circle';

  return (
    <View style={[styles.toast, { backgroundColor: bg }]}>
      <FontAwesome name={icon} size={14} color={fg} />
      <Text style={[styles.toastText, { color: fg }]}>{message}</Text>
    </View>
  );
}

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function RepoDetailScreen() {
  const { name } = useLocalSearchParams<{ name: string }>();
  const decodedName = name ? decodeURIComponent(name) : '';

  // Data state
  const [repoInfo, setRepoInfo] = useState<RepoInfo | null>(null);
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Action states
  const [staging, setStaging] = useState(false);
  const [committing, setCommitting] = useState(false);
  const [pushing, setPushing] = useState(false);

  // Commit message input
  const [commitMessage, setCommitMessage] = useState('');
  const [showCommitInput, setShowCommitInput] = useState(false);

  // Toast
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);
  const toastTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Cleanup toast timer on unmount
  useEffect(() => {
    return () => {
      if (toastTimerRef.current !== undefined) clearTimeout(toastTimerRef.current);
    };
  }, []);

  // -------------------------------------------------------------------------
  // Toast helper
  // -------------------------------------------------------------------------

  const showToast = useCallback((message: string, type: 'success' | 'error') => {
    setToast({ message, type });
    if (toastTimerRef.current !== undefined) clearTimeout(toastTimerRef.current);
    toastTimerRef.current = setTimeout(() => setToast(null), 3000);
  }, []);

  // -------------------------------------------------------------------------
  // Fetch data
  // -------------------------------------------------------------------------

  const fetchData = useCallback(
    async (isRefresh = false) => {
      if (!decodedName) return;

      if (isRefresh) {
        setRefreshing(true);
      }

      try {
        const [info, diffResult] = await Promise.all([
          awmApi.getRepoInfo(decodedName),
          awmApi.getRepoDiff(decodedName),
        ]);
        setRepoInfo(info);
        setDiff(diffResult);
        setError(null);
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : 'Failed to load repo';
        setError(message);
      } finally {
        setLoading(false);
        if (isRefresh) {
          setRefreshing(false);
        }
      }
    },
    [decodedName],
  );

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  // -------------------------------------------------------------------------
  // Actions
  // -------------------------------------------------------------------------

  const handleStage = useCallback(() => {
    Alert.alert('Stage All Changes', 'Stage all modified files for commit?', [
      { text: 'Cancel', style: 'cancel' },
      {
        text: 'Stage All',
        onPress: async () => {
          setStaging(true);
          try {
            await awmApi.gitStage(decodedName);
            showToast('All changes staged', 'success');
            fetchData();
          } catch (err: unknown) {
            showToast(err instanceof Error ? err.message : 'Failed to stage', 'error');
          } finally {
            setStaging(false);
          }
        },
      },
    ]);
  }, [decodedName, showToast, fetchData]);

  const handleCommit = useCallback(async () => {
    const message = commitMessage.trim();
    if (message.length === 0) {
      Alert.alert('Commit message required', 'Please enter a commit message before committing.');
      return;
    }

    setCommitting(true);
    try {
      await awmApi.gitCommit(decodedName, message);
      showToast('Changes committed', 'success');
      setCommitMessage('');
      setShowCommitInput(false);
      // Refresh diff after commit
      fetchData();
    } catch (err: unknown) {
      showToast(err instanceof Error ? err.message : 'Failed to commit', 'error');
    } finally {
      setCommitting(false);
    }
  }, [decodedName, commitMessage, showToast, fetchData]);

  const handlePush = useCallback(() => {
    Alert.alert('Push to Remote', 'Push committed changes to remote?', [
      { text: 'Cancel', style: 'cancel' },
      {
        text: 'Push',
        onPress: async () => {
          setPushing(true);
          try {
            await awmApi.gitPush(decodedName);
            showToast('Pushed to remote', 'success');
          } catch (err: unknown) {
            showToast(err instanceof Error ? err.message : 'Failed to push', 'error');
          } finally {
            setPushing(false);
          }
        },
      },
    ]);
  }, [decodedName, showToast]);

  // -------------------------------------------------------------------------
  // Loading / error states
  // -------------------------------------------------------------------------

  if (loading) {
    return (
      <>
        <Stack.Screen options={{ title: decodedName || 'Repo' }} />
        <View style={styles.centered}>
          <ActivityIndicator size="large" color={JARVIS.cyan} />
          <Text style={styles.loadingText}>Loading repo...</Text>
        </View>
      </>
    );
  }

  if (error) {
    return (
      <>
        <Stack.Screen options={{ title: decodedName || 'Repo' }} />
        <View style={styles.centered}>
          <FontAwesome name="exclamation-triangle" size={32} color={JARVIS.red} />
          <Text style={styles.errorText}>{error}</Text>
          <TouchableOpacity style={styles.retryButton} onPress={() => fetchData()}>
            <Text style={styles.retryButtonText}>Retry</Text>
          </TouchableOpacity>
        </View>
      </>
    );
  }

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <>
      <Stack.Screen
        options={{
          title: decodedName,
          headerStyle: { backgroundColor: JARVIS.bg },
          headerTintColor: JARVIS.text,
          headerTitleStyle: { fontWeight: '700' },
        }}
      />

      <View style={styles.container}>
        {/* Toast */}
        {toast && <Toast message={toast.message} type={toast.type} />}

        <ScrollView
          style={styles.scrollView}
          contentContainerStyle={styles.content}
          refreshControl={
            <RefreshControl
              refreshing={refreshing}
              onRefresh={() => fetchData(true)}
              tintColor={JARVIS.cyan}
            />
          }
          keyboardShouldPersistTaps="handled"
        >
          {/* Branch info */}
          {repoInfo && (
            <View style={styles.headerInfo}>
              <View style={styles.headerTopRow}>
                <Text style={styles.repoTitle} numberOfLines={1}>
                  {decodedName}
                </Text>
                <BranchChip branch={repoInfo.branch} />
              </View>
              {repoInfo.lastCommitMessage ? (
                <Text style={styles.lastCommit} numberOfLines={2}>
                  {repoInfo.lastCommitMessage}
                </Text>
              ) : null}
              <View style={styles.repoStatsRow}>
                <Text style={styles.repoStat}>
                  {repoInfo.uncommittedFiles} uncommitted file{repoInfo.uncommittedFiles !== 1 ? 's' : ''}
                </Text>
                {repoInfo.isClean && (
                  <View style={styles.cleanBadge}>
                    <FontAwesome name="check" size={10} color={JARVIS.green} />
                    <Text style={styles.cleanBadgeText}>Clean</Text>
                  </View>
                )}
              </View>
            </View>
          )}

          {/* Diff summary */}
          {diff && <DiffSummaryCard diff={diff} />}

          {/* Actions */}
          <View style={styles.actionsSection}>
            <Text style={styles.sectionLabel}>Actions</Text>

            <ActionButton
              icon="plus-square"
              label="Stage All"
              onPress={handleStage}
              loading={staging}
              variant="default"
            />

            {/* Commit: toggle input visibility */}
            {!showCommitInput ? (
              <ActionButton
                icon="check-square"
                label="Commit"
                onPress={() => setShowCommitInput(true)}
                variant="primary"
              />
            ) : (
              <View style={styles.commitSection}>
                <TextInput
                  style={styles.commitInput}
                  value={commitMessage}
                  onChangeText={setCommitMessage}
                  placeholder="Commit message..."
                  placeholderTextColor={JARVIS.textMuted}
                  multiline
                  numberOfLines={3}
                  autoFocus
                  returnKeyType="done"
                />
                <View style={styles.commitActions}>
                  <TouchableOpacity
                    style={styles.commitCancelButton}
                    onPress={() => {
                      setShowCommitInput(false);
                      setCommitMessage('');
                    }}
                  >
                    <Text style={styles.commitCancelText}>Cancel</Text>
                  </TouchableOpacity>
                  <TouchableOpacity
                    style={[
                      styles.commitConfirmButton,
                      commitMessage.trim().length === 0 && styles.commitConfirmDisabled,
                    ]}
                    onPress={handleCommit}
                    disabled={committing || commitMessage.trim().length === 0}
                  >
                    {committing ? (
                      <ActivityIndicator size="small" color={JARVIS.bg} />
                    ) : (
                      <Text style={styles.commitConfirmText}>Commit</Text>
                    )}
                  </TouchableOpacity>
                </View>
              </View>
            )}

            <ActionButton
              icon="cloud-upload"
              label="Push"
              onPress={handlePush}
              loading={pushing}
              variant="success"
            />
          </View>
        </ScrollView>
      </View>
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
  scrollView: {
    flex: 1,
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
    gap: 12,
    padding: 24,
  },
  loadingText: {
    color: JARVIS.textSecondary,
    fontSize: 15,
    marginTop: 4,
  },
  errorText: {
    color: JARVIS.red,
    fontSize: 15,
    textAlign: 'center',
  },
  retryButton: {
    backgroundColor: JARVIS.cyan,
    paddingHorizontal: 24,
    paddingVertical: 10,
    borderRadius: 8,
    marginTop: 4,
  },
  retryButtonText: {
    color: JARVIS.bg,
    fontSize: 15,
    fontWeight: '600',
  },

  // Header info
  headerInfo: {
    backgroundColor: JARVIS.surface,
    borderRadius: 12,
    padding: 16,
    marginBottom: 16,
    borderWidth: 1,
    borderColor: JARVIS.border,
  },
  headerTopRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: 8,
  },
  repoTitle: {
    fontSize: 18,
    fontWeight: '700',
    color: JARVIS.text,
    flexShrink: 1,
    marginRight: 10,
  },
  lastCommit: {
    fontSize: 13,
    color: JARVIS.textSecondary,
    marginBottom: 8,
    lineHeight: 18,
  },
  repoStatsRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
  },
  repoStat: {
    fontSize: 12,
    color: JARVIS.textMuted,
  },
  cleanBadge: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 4,
    backgroundColor: JARVIS.greenDim,
    paddingHorizontal: 8,
    paddingVertical: 2,
    borderRadius: 6,
  },
  cleanBadgeText: {
    fontSize: 11,
    fontWeight: '600',
    color: JARVIS.green,
  },

  // Branch chip
  branchChip: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 5,
    backgroundColor: JARVIS.cyanDim,
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 8,
  },
  branchChipText: {
    fontSize: 12,
    fontWeight: '600',
    color: JARVIS.cyan,
  },

  // Diff card
  card: {
    backgroundColor: JARVIS.surface,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: JARVIS.border,
    marginBottom: 16,
    overflow: 'hidden',
  },
  cardHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
    padding: 14,
    borderBottomWidth: 1,
    borderBottomColor: JARVIS.border,
  },
  cardTitle: {
    fontSize: 14,
    fontWeight: '600',
    color: JARVIS.text,
    flex: 1,
  },
  statsBadge: {
    backgroundColor: JARVIS.cyanDim,
    paddingHorizontal: 8,
    paddingVertical: 2,
    borderRadius: 6,
  },
  statsBadgeText: {
    fontSize: 11,
    fontWeight: '600',
    color: JARVIS.cyan,
  },

  // File list
  fileList: {
    paddingHorizontal: 14,
    paddingTop: 8,
    paddingBottom: 4,
  },
  fileRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingVertical: 8,
    borderBottomWidth: 1,
    borderBottomColor: JARVIS.border,
  },
  filePath: {
    fontFamily: 'SpaceMono',
    fontSize: 12,
    color: JARVIS.textSecondary,
    flex: 1,
    marginRight: 12,
  },
  fileStats: {
    flexDirection: 'row',
    gap: 8,
    alignItems: 'center',
  },
  insertionText: {
    fontFamily: 'SpaceMono',
    fontSize: 12,
    fontWeight: '600',
    color: JARVIS.green,
  },
  deletionText: {
    fontFamily: 'SpaceMono',
    fontSize: 12,
    fontWeight: '600',
    color: JARVIS.red,
  },
  noChangeText: {
    fontFamily: 'SpaceMono',
    fontSize: 12,
    color: JARVIS.textMuted,
  },

  // Totals
  totalsRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderTopWidth: 1,
    borderTopColor: JARVIS.border,
    backgroundColor: JARVIS.elevated,
  },
  totalsLabel: {
    fontSize: 12,
    fontWeight: '600',
    color: JARVIS.textSecondary,
    flex: 1,
  },
  totalInsertions: {
    fontFamily: 'SpaceMono',
    fontSize: 13,
    fontWeight: '700',
    color: JARVIS.green,
  },
  totalDeletions: {
    fontFamily: 'SpaceMono',
    fontSize: 13,
    fontWeight: '700',
    color: JARVIS.red,
  },

  // Empty diff
  emptyDiff: {
    alignItems: 'center',
    justifyContent: 'center',
    paddingVertical: 24,
    gap: 8,
  },
  emptyDiffText: {
    fontSize: 14,
    color: JARVIS.textMuted,
  },

  // Actions section
  actionsSection: {
    gap: 10,
  },
  sectionLabel: {
    fontSize: 13,
    fontWeight: '600',
    color: JARVIS.textMuted,
    textTransform: 'uppercase',
    letterSpacing: 0.5,
    marginBottom: 4,
  },
  actionButton: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    paddingVertical: 14,
    paddingHorizontal: 20,
    borderRadius: 10,
    gap: 10,
  },
  actionButtonDisabled: {
    opacity: 0.5,
  },
  actionIcon: {
    width: 20,
    textAlign: 'center',
  },
  actionLabel: {
    fontSize: 15,
    fontWeight: '600',
  },

  // Commit section
  commitSection: {
    backgroundColor: JARVIS.surface,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: JARVIS.border,
    overflow: 'hidden',
  },
  commitInput: {
    fontFamily: 'SpaceMono',
    fontSize: 14,
    color: JARVIS.text,
    padding: 14,
    minHeight: 80,
    textAlignVertical: 'top',
  },
  commitActions: {
    flexDirection: 'row',
    justifyContent: 'flex-end',
    gap: 10,
    paddingHorizontal: 14,
    paddingBottom: 12,
  },
  commitCancelButton: {
    paddingHorizontal: 16,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: JARVIS.elevated,
  },
  commitCancelText: {
    fontSize: 14,
    fontWeight: '600',
    color: JARVIS.textSecondary,
  },
  commitConfirmButton: {
    paddingHorizontal: 20,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: JARVIS.cyan,
  },
  commitConfirmDisabled: {
    opacity: 0.4,
  },
  commitConfirmText: {
    fontSize: 14,
    fontWeight: '600',
    color: JARVIS.bg,
  },

  // Toast
  toast: {
    position: 'absolute',
    top: 8,
    left: 16,
    right: 16,
    zIndex: 10,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
    paddingVertical: 10,
    paddingHorizontal: 14,
    borderRadius: 10,
  },
  toastText: {
    fontSize: 14,
    fontWeight: '600',
    flex: 1,
  },
});
