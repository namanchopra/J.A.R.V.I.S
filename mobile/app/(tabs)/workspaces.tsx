import React, { useCallback, useEffect, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  FlatList,
  RefreshControl,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from 'react-native';
import FontAwesome from '@expo/vector-icons/FontAwesome';

import { awmApi, Workspace } from '../../lib/api';
import { JARVIS, jarvisStyles } from '../../constants/Colors';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Format ISO date string as "Apr 14, 2026" */
function formatDate(iso: string): string {
  const date = new Date(iso);
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  });
}

/** Extract last path segment, e.g. "/Users/foo/api-service" -> "api-service" */
function lastSegment(repoPath: string): string {
  const trimmed = repoPath.replace(/\/+$/, '');
  const parts = trimmed.split('/');
  return parts[parts.length - 1] || repoPath;
}

/** Truncate to maxLen chars, appending "..." if truncated */
function truncate(text: string, maxLen: number): string {
  if (text.length <= maxLen) return text;
  return text.slice(0, maxLen) + '...';
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function EmptyState() {
  return (
    <View style={styles.emptyContainer}>
      <FontAwesome name="folder-open-o" size={48} color={JARVIS.textMuted} style={styles.emptyIcon} />
      <Text style={styles.emptyText}>No workspaces yet</Text>
    </View>
  );
}

function RepoPathPill({ label }: { label: string }) {
  return (
    <View style={styles.repoPathPill}>
      <Text style={styles.repoPathPillText}>{label}</Text>
    </View>
  );
}

interface WorkspaceCardProps {
  workspace: Workspace;
  onDelete: (ws: Workspace) => void;
}

function WorkspaceCard({ workspace, onDelete }: WorkspaceCardProps) {
  const repoCount = workspace.repoPaths.length;

  return (
    <View style={[jarvisStyles.holoPanel, styles.card]}>
      {/* Header row: name + delete button */}
      <View style={styles.cardHeader}>
        <Text style={styles.cardName} numberOfLines={1}>
          {workspace.name}
        </Text>
        <TouchableOpacity
          onPress={() => onDelete(workspace)}
          hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
          accessibilityRole="button"
          accessibilityLabel={`Delete workspace ${workspace.name}`}
        >
          <FontAwesome name="trash-o" size={18} color={JARVIS.red} />
        </TouchableOpacity>
      </View>

      {/* Metadata row: repo count badge + date */}
      <View style={styles.metaRow}>
        <View style={styles.repoBadge}>
          <FontAwesome name="code-fork" size={12} color={JARVIS.cyan} style={styles.repoBadgeIcon} />
          <Text style={styles.repoBadgeText}>
            {repoCount} {repoCount === 1 ? 'repo' : 'repos'}
          </Text>
        </View>
        <Text style={styles.dateText}>{formatDate(workspace.createdAt)}</Text>
      </View>

      {/* Repo path pills */}
      {repoCount > 0 && (
        <View style={styles.pillsRow}>
          {workspace.repoPaths.map((rp) => (
            <RepoPathPill key={rp} label={lastSegment(rp)} />
          ))}
        </View>
      )}

      {/* Prompt preview */}
      {workspace.prompt != null && workspace.prompt.length > 0 && (
        <Text style={styles.promptPreview} numberOfLines={2}>
          {truncate(workspace.prompt, 80)}
        </Text>
      )}
    </View>
  );
}

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function WorkspacesScreen() {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // -------------------------------------------------------------------------
  // Fetch workspaces
  // -------------------------------------------------------------------------

  const fetchWorkspaces = useCallback(async () => {
    try {
      const data = await awmApi.listWorkspaces();
      setWorkspaces(data);
      setError(null);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to load workspaces';
      setError(message);
    }
  }, []);

  // Initial load
  useEffect(() => {
    let mounted = true;

    async function load() {
      setLoading(true);
      try {
        const data = await awmApi.listWorkspaces();
        if (!mounted) return;
        setWorkspaces(data);
        setError(null);
      } catch (err: unknown) {
        if (!mounted) return;
        const message = err instanceof Error ? err.message : 'Failed to load workspaces';
        setError(message);
      } finally {
        if (mounted) setLoading(false);
      }
    }

    load();
    return () => {
      mounted = false;
    };
  }, []);

  // Pull-to-refresh handler
  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    await fetchWorkspaces();
    setRefreshing(false);
  }, [fetchWorkspaces]);

  // -------------------------------------------------------------------------
  // Delete handler
  // -------------------------------------------------------------------------

  const handleDeleteConfirm = useCallback((ws: Workspace) => {
    Alert.alert(
      'Delete Workspace',
      `Delete "${ws.name}"?`,
      [
        { text: 'Cancel', style: 'cancel' },
        {
          text: 'Delete',
          style: 'destructive',
          onPress: () => {
            // Optimistic update: remove from list immediately
            setWorkspaces((prev) => prev.filter((w) => w.id !== ws.id));

            // Fire-and-forget the server call; alert on error
            awmApi.deleteWorkspace(ws.id).catch(() => {
              Alert.alert('Error', 'Failed to delete workspace');
              // Re-add the workspace back on failure
              setWorkspaces((prev) => {
                // Avoid duplicates if user rapidly retries
                if (prev.some((w) => w.id === ws.id)) return prev;
                return [...prev, ws];
              });
            });
          },
        },
      ],
    );
  }, []);

  // -------------------------------------------------------------------------
  // Render helpers
  // -------------------------------------------------------------------------

  const renderItem = useCallback(
    ({ item }: { item: Workspace }) => (
      <WorkspaceCard workspace={item} onDelete={handleDeleteConfirm} />
    ),
    [handleDeleteConfirm],
  );

  const keyExtractor = useCallback((item: Workspace) => item.id, []);

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
  // Error state (with retry)
  // -------------------------------------------------------------------------

  if (error !== null && workspaces.length === 0) {
    return (
      <View style={styles.centered}>
        <FontAwesome name="exclamation-triangle" size={36} color={JARVIS.red} />
        <Text style={styles.errorText}>{error}</Text>
        <TouchableOpacity
          style={styles.retryButton}
          onPress={fetchWorkspaces}
          accessibilityRole="button"
          accessibilityLabel="Retry loading workspaces"
        >
          <Text style={styles.retryButtonText}>Retry</Text>
        </TouchableOpacity>
      </View>
    );
  }

  // -------------------------------------------------------------------------
  // Main list (or empty state)
  // -------------------------------------------------------------------------

  return (
    <FlatList
      data={workspaces}
      renderItem={renderItem}
      keyExtractor={keyExtractor}
      style={styles.container}
      contentContainerStyle={
        workspaces.length === 0 ? styles.emptyListContent : styles.listContent
      }
      ListEmptyComponent={EmptyState}
      refreshControl={
        <RefreshControl
          refreshing={refreshing}
          onRefresh={onRefresh}
          tintColor={JARVIS.cyan}
          colors={[JARVIS.cyan]}
        />
      }
    />
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
  listContent: {
    padding: 16,
    paddingBottom: 32,
  },
  emptyListContent: {
    flexGrow: 1,
    justifyContent: 'center',
    alignItems: 'center',
    padding: 16,
  },

  // Centered states (loading, error)
  centered: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    backgroundColor: JARVIS.bg,
    padding: 20,
  },

  // Empty state
  emptyContainer: {
    alignItems: 'center',
  },
  emptyIcon: {
    marginBottom: 12,
  },
  emptyText: {
    fontSize: 16,
    color: JARVIS.textMuted,
    fontWeight: '500',
  },

  // Error state
  errorText: {
    fontSize: 15,
    color: JARVIS.red,
    marginTop: 12,
    marginBottom: 16,
    textAlign: 'center',
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
    fontSize: 15,
    fontWeight: '600',
  },

  // Card — merges with jarvisStyles.holoPanel via style array
  card: {
    marginBottom: 12,
  },
  cardHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginBottom: 8,
  },
  cardName: {
    fontSize: 18,
    fontWeight: '700',
    color: JARVIS.text,
    flex: 1,
    marginRight: 12,
  },

  // Metadata row
  metaRow: {
    flexDirection: 'row',
    alignItems: 'center',
    marginBottom: 10,
  },
  repoBadge: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.cyanDim,
    borderRadius: 10,
    paddingHorizontal: 8,
    paddingVertical: 3,
    marginRight: 10,
  },
  repoBadgeIcon: {
    marginRight: 4,
  },
  repoBadgeText: {
    fontSize: 12,
    fontWeight: '600',
    color: JARVIS.textSecondary,
  },
  dateText: {
    fontSize: 13,
    color: JARVIS.textMuted,
  },

  // Repo path pills
  pillsRow: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    marginBottom: 8,
    gap: 6,
  },
  repoPathPill: {
    backgroundColor: JARVIS.elevated,
    borderRadius: 8,
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderWidth: 1,
    borderColor: JARVIS.border,
  },
  repoPathPillText: {
    fontSize: 12,
    color: JARVIS.textSecondary,
    fontWeight: '500',
    fontFamily: 'SpaceMono',
  },

  // Prompt preview
  promptPreview: {
    fontSize: 13,
    color: JARVIS.textMuted,
    fontStyle: 'italic',
    lineHeight: 18,
  },
});
