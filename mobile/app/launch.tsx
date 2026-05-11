import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  FlatList,
  KeyboardAvoidingView,
  Platform,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import { Stack, router } from 'expo-router';
import FontAwesome from '@expo/vector-icons/FontAwesome';

import { type Repo, awmApi } from '../lib/api';
import { JARVIS } from '../constants/Colors';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const AGENT_TYPES = [
  { key: 'claude-code', label: 'Claude Code', icon: 'terminal' as const },
  { key: 'kiro', label: 'Kiro', icon: 'code' as const },
  { key: 'gemini', label: 'Gemini', icon: 'diamond' as const },
  { key: 'codex', label: 'Codex', icon: 'file-code-o' as const },
  { key: 'aider', label: 'Aider', icon: 'wrench' as const },
];

// ---------------------------------------------------------------------------
// Language badge colour mapping
// ---------------------------------------------------------------------------

const LANGUAGE_COLORS: Record<string, string> = {
  go: '#00ADD8',
  typescript: '#3178C6',
  javascript: '#F7DF1E',
  python: '#3776AB',
  rust: '#DEA584',
  java: '#B07219',
  ruby: '#CC342D',
  swift: '#FA7343',
  kotlin: '#A97BFF',
  dart: '#00B4AB',
};

function languageColor(lang: string): string {
  return LANGUAGE_COLORS[lang.toLowerCase()] ?? JARVIS.lightTextMuted;
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function AgentSelector({
  selected,
  onSelect,
}: {
  selected: string;
  onSelect: (agent: string) => void;
}) {
  return (
    <View style={styles.agentGrid}>
      {AGENT_TYPES.map(({ key, label, icon }) => {
        const isSelected = key === selected;
        return (
          <TouchableOpacity
            key={key}
            style={[styles.agentButton, isSelected && styles.agentButtonSelected]}
            onPress={() => onSelect(key)}
            accessibilityRole="radio"
            accessibilityState={{ selected: isSelected }}
            accessibilityLabel={`Select ${label} agent`}
          >
            <FontAwesome
              name={icon}
              size={16}
              color={isSelected ? JARVIS.cyan : JARVIS.lightTextMuted}
            />
            <Text
              style={[
                styles.agentButtonText,
                isSelected && styles.agentButtonTextSelected,
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

function RepoRow({
  repo,
  isSelected,
  onPress,
}: {
  repo: Repo;
  isSelected: boolean;
  onPress: () => void;
}) {
  return (
    <TouchableOpacity
      style={[styles.repoRow, isSelected && styles.repoRowSelected]}
      onPress={onPress}
      activeOpacity={0.7}
      accessibilityRole="radio"
      accessibilityState={{ selected: isSelected }}
      accessibilityLabel={`Select repo ${repo.name}`}
    >
      <View style={styles.repoRowLeft}>
        {isSelected ? (
          <FontAwesome name="check-circle" size={18} color={JARVIS.cyan} />
        ) : (
          <FontAwesome name="circle-o" size={18} color={JARVIS.lightTextSecondary} />
        )}
        <View style={styles.repoInfo}>
          <Text style={styles.repoName} numberOfLines={1}>
            {repo.name}
          </Text>
          <View style={styles.repoMeta}>
            {repo.language ? (
              <View style={styles.languageBadge}>
                <View
                  style={[
                    styles.languageDot,
                    { backgroundColor: languageColor(repo.language) },
                  ]}
                />
                <Text style={styles.languageText}>{repo.language}</Text>
              </View>
            ) : null}
            {repo.branch ? (
              <View style={styles.branchBadge}>
                <FontAwesome name="code-fork" size={10} color={JARVIS.lightTextMuted} />
                <Text style={styles.branchText}>{repo.branch}</Text>
              </View>
            ) : null}
          </View>
        </View>
      </View>
      {repo.hasAgent && (
        <View style={styles.activeIndicator}>
          <View style={styles.activeIndicatorDot} />
          <Text style={styles.activeIndicatorText}>Active</Text>
        </View>
      )}
    </TouchableOpacity>
  );
}

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function LaunchScreen() {
  // Repos
  const [repos, setRepos] = useState<Repo[]>([]);
  const [loadingRepos, setLoadingRepos] = useState(true);
  const [repoError, setRepoError] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState('');

  // Form state
  const [selectedRepo, setSelectedRepo] = useState<Repo | null>(null);
  const [selectedAgent, setSelectedAgent] = useState('claude-code');
  const [prompt, setPrompt] = useState('');
  const [launching, setLaunching] = useState(false);

  // -------------------------------------------------------------------------
  // Fetch repos
  // -------------------------------------------------------------------------

  const fetchRepos = useCallback(async () => {
    setLoadingRepos(true);
    setRepoError(null);
    try {
      const data = await awmApi.listRepos();
      setRepos(data);
    } catch (err: unknown) {
      setRepoError(err instanceof Error ? err.message : 'Failed to load repos');
    } finally {
      setLoadingRepos(false);
    }
  }, []);

  useEffect(() => {
    fetchRepos();
  }, [fetchRepos]);

  // -------------------------------------------------------------------------
  // Filtered repos
  // -------------------------------------------------------------------------

  const filteredRepos = useMemo(() => {
    if (!searchQuery.trim()) return repos;
    const q = searchQuery.toLowerCase();
    return repos.filter(
      (r) =>
        r.name.toLowerCase().includes(q) ||
        r.language?.toLowerCase().includes(q) ||
        r.project?.toLowerCase().includes(q),
    );
  }, [repos, searchQuery]);

  // -------------------------------------------------------------------------
  // Launch handler
  // -------------------------------------------------------------------------

  const handleLaunch = useCallback(async () => {
    if (!selectedRepo || launching) return;

    setLaunching(true);
    try {
      const session = await awmApi.launchSession(
        selectedAgent,
        selectedRepo.path,
        prompt.trim(),
      );
      // Navigate to the new session detail screen
      router.replace(`/sessions/${session.id}` as never);
    } catch (err: unknown) {
      Alert.alert(
        'Launch Failed',
        err instanceof Error ? err.message : 'Could not launch session',
      );
    } finally {
      setLaunching(false);
    }
  }, [selectedRepo, selectedAgent, prompt, launching]);

  // -------------------------------------------------------------------------
  // Render helpers
  // -------------------------------------------------------------------------

  const renderRepoItem = useCallback(
    ({ item }: { item: Repo }) => (
      <RepoRow
        repo={item}
        isSelected={selectedRepo?.path === item.path}
        onPress={() => setSelectedRepo(item)}
      />
    ),
    [selectedRepo],
  );

  const keyExtractor = useCallback((item: Repo) => item.path, []);

  const canLaunch = selectedRepo !== null && !launching;

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <>
      <Stack.Screen
        options={{
          title: 'Launch Session',
          headerBackTitle: 'Back',
        }}
      />

      <KeyboardAvoidingView
        style={styles.container}
        behavior={Platform.OS === 'ios' ? 'padding' : 'height'}
        keyboardVerticalOffset={Platform.OS === 'ios' ? 90 : 0}
      >
        {/* -- Agent type selector -- */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Agent</Text>
          <AgentSelector selected={selectedAgent} onSelect={setSelectedAgent} />
        </View>

        {/* -- Repo picker -- */}
        <View style={styles.sectionFlex}>
          <Text style={styles.sectionTitle}>Repository</Text>

          <View style={styles.searchContainer}>
            <FontAwesome name="search" size={14} color={JARVIS.lightTextFaint} style={styles.searchIcon} />
            <TextInput
              style={styles.searchInput}
              value={searchQuery}
              onChangeText={setSearchQuery}
              placeholder="Filter repos..."
              placeholderTextColor={JARVIS.lightTextSecondary}
              autoCapitalize="none"
              autoCorrect={false}
              accessibilityLabel="Search repositories"
            />
            {searchQuery.length > 0 && (
              <TouchableOpacity
                onPress={() => setSearchQuery('')}
                style={styles.searchClear}
                accessibilityRole="button"
                accessibilityLabel="Clear search"
              >
                <FontAwesome name="times-circle" size={16} color={JARVIS.lightTextFaint} />
              </TouchableOpacity>
            )}
          </View>

          {loadingRepos ? (
            <View style={styles.centeredMessage}>
              <ActivityIndicator size="large" color={JARVIS.cyan} />
              <Text style={styles.loadingText}>Loading repos...</Text>
            </View>
          ) : repoError ? (
            <View style={styles.centeredMessage}>
              <FontAwesome name="exclamation-triangle" size={24} color={JARVIS.lightError} />
              <Text style={styles.errorText}>{repoError}</Text>
              <TouchableOpacity style={styles.retryButton} onPress={fetchRepos}>
                <Text style={styles.retryButtonText}>Retry</Text>
              </TouchableOpacity>
            </View>
          ) : (
            <FlatList
              data={filteredRepos}
              renderItem={renderRepoItem}
              keyExtractor={keyExtractor}
              contentContainerStyle={
                filteredRepos.length === 0 ? styles.listEmpty : styles.listContent
              }
              ListEmptyComponent={
                <View style={styles.centeredMessage}>
                  <Text style={styles.emptyText}>
                    {searchQuery ? 'No repos match your search' : 'No repos found'}
                  </Text>
                </View>
              }
              keyboardShouldPersistTaps="handled"
            />
          )}
        </View>

        {/* -- Prompt input -- */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Prompt (optional)</Text>
          <TextInput
            style={styles.promptInput}
            value={prompt}
            onChangeText={setPrompt}
            placeholder="Initial prompt for the agent..."
            placeholderTextColor={JARVIS.lightTextSecondary}
            multiline
            numberOfLines={3}
            textAlignVertical="top"
            accessibilityLabel="Session prompt"
          />
        </View>

        {/* -- Launch button -- */}
        <View style={styles.launchSection}>
          {selectedRepo && (
            <Text style={styles.selectionSummary}>
              {selectedAgent} on {selectedRepo.name}
            </Text>
          )}
          <TouchableOpacity
            style={[styles.launchButton, !canLaunch && styles.launchButtonDisabled]}
            onPress={handleLaunch}
            disabled={!canLaunch}
            accessibilityRole="button"
            accessibilityLabel="Launch session"
          >
            {launching ? (
              <ActivityIndicator size="small" color={JARVIS.bg} />
            ) : (
              <>
                <FontAwesome name="rocket" size={16} color={JARVIS.bg} style={styles.launchIcon} />
                <Text style={styles.launchButtonText}>Launch Session</Text>
              </>
            )}
          </TouchableOpacity>
        </View>
      </KeyboardAvoidingView>
    </>
  );
}

// ---------------------------------------------------------------------------
// Styles
// ---------------------------------------------------------------------------

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: JARVIS.lightBg,
  },

  // Sections
  section: {
    paddingHorizontal: 16,
    paddingTop: 16,
    paddingBottom: 8,
  },
  sectionFlex: {
    flex: 1,
    paddingHorizontal: 16,
    paddingTop: 12,
    minHeight: 0,
  },
  sectionTitle: {
    fontSize: 13,
    fontWeight: '700',
    color: JARVIS.lightTextMuted,
    textTransform: 'uppercase',
    letterSpacing: 0.8,
    marginBottom: 8,
  },

  // Agent selector
  agentGrid: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 8,
  },
  agentButton: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.lightSurfaceAlt,
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderWidth: 2,
    borderColor: 'transparent',
    gap: 6,
  },
  agentButtonSelected: {
    backgroundColor: JARVIS.lightSelectedBorder,
    borderColor: JARVIS.cyan,
  },
  agentButtonText: {
    fontSize: 13,
    fontWeight: '600',
    color: JARVIS.lightTextSecondary,
  },
  agentButtonTextSelected: {
    color: JARVIS.cyan,
  },

  // Search
  searchContainer: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: JARVIS.lightSurfaceAlt,
    borderRadius: 8,
    paddingHorizontal: 10,
    marginBottom: 8,
  },
  searchIcon: {
    marginRight: 8,
  },
  searchInput: {
    flex: 1,
    paddingVertical: 10,
    fontSize: 14,
    color: JARVIS.lightText,
  },
  searchClear: {
    padding: 4,
  },

  // Repo list
  listContent: {
    paddingBottom: 8,
  },
  listEmpty: {
    flexGrow: 1,
    justifyContent: 'center',
    alignItems: 'center',
  },

  // Repo row
  repoRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    backgroundColor: JARVIS.lightSurface,
    borderRadius: 10,
    paddingVertical: 12,
    paddingHorizontal: 14,
    marginBottom: 6,
    borderWidth: 2,
    borderColor: JARVIS.lightBorder,
  },
  repoRowSelected: {
    borderColor: JARVIS.cyan,
    backgroundColor: JARVIS.lightSelectedBg,
  },
  repoRowLeft: {
    flexDirection: 'row',
    alignItems: 'center',
    flex: 1,
    gap: 10,
  },
  repoInfo: {
    flex: 1,
  },
  repoName: {
    fontSize: 15,
    fontWeight: '600',
    color: JARVIS.lightText,
    marginBottom: 3,
  },
  repoMeta: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
  },
  languageBadge: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 4,
  },
  languageDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  languageText: {
    fontSize: 11,
    color: JARVIS.lightTextPlaceholder,
    fontWeight: '500',
  },
  branchBadge: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 3,
  },
  branchText: {
    fontSize: 11,
    color: JARVIS.lightTextMuted,
  },
  activeIndicator: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 4,
    marginLeft: 8,
  },
  activeIndicatorDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    backgroundColor: JARVIS.lightGreen,
  },
  activeIndicatorText: {
    fontSize: 11,
    color: JARVIS.lightGreen,
    fontWeight: '600',
  },

  // Prompt input
  promptInput: {
    backgroundColor: JARVIS.lightSurface,
    borderWidth: 1,
    borderColor: JARVIS.lightBorderInput,
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 14,
    color: JARVIS.lightText,
    minHeight: 70,
    maxHeight: 120,
  },

  // Launch section
  launchSection: {
    paddingHorizontal: 16,
    paddingTop: 8,
    paddingBottom: 24,
    alignItems: 'center',
  },
  selectionSummary: {
    fontSize: 12,
    color: JARVIS.lightTextMuted,
    marginBottom: 8,
  },
  launchButton: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: JARVIS.cyan,
    borderRadius: 10,
    paddingVertical: 14,
    paddingHorizontal: 24,
    width: '100%',
    gap: 8,
  },
  launchButtonDisabled: {
    opacity: 0.4,
  },
  launchIcon: {
    marginRight: 2,
  },
  launchButtonText: {
    fontSize: 16,
    fontWeight: '700',
    color: JARVIS.bg,
  },

  // Shared
  centeredMessage: {
    alignItems: 'center',
    justifyContent: 'center',
    paddingVertical: 40,
    gap: 10,
  },
  loadingText: {
    fontSize: 14,
    color: JARVIS.lightTextMuted,
  },
  errorText: {
    fontSize: 14,
    color: JARVIS.lightError,
    textAlign: 'center',
  },
  emptyText: {
    fontSize: 14,
    color: JARVIS.lightTextDimmer,
  },
  retryButton: {
    backgroundColor: JARVIS.lightAccent,
    paddingHorizontal: 20,
    paddingVertical: 8,
    borderRadius: 8,
  },
  retryButtonText: {
    color: JARVIS.lightSurface,
    fontSize: 14,
    fontWeight: '600',
  },
});
