import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import FontAwesome from '@expo/vector-icons/FontAwesome';

import { awmApi } from '../../lib/api';
import { storage } from '../../lib/storage';
import { JARVIS, jarvisStyles } from '../../constants/Colors';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const TAILSCALE_IP_REGEX = /^100\.\d{1,3}\.\d{1,3}\.\d{1,3}$/;
const DEFAULT_PORT = '4422';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ConnectionState = 'unknown' | 'testing' | 'connected' | 'disconnected';
type TailscaleTestState = 'idle' | 'testing' | 'connected' | 'failed';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function isTailscaleUrl(url: string): boolean {
  try {
    const host = url.replace(/^https?:\/\//, '').split(':')[0] ?? '';
    return TAILSCALE_IP_REGEX.test(host);
  } catch {
    return false;
  }
}

function extractHostFromUrl(url: string): string {
  try {
    return url.replace(/^https?:\/\//, '').split(':')[0] ?? '';
  } catch {
    return '';
  }
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function ConnectionStatusPill({ state }: { state: ConnectionState }) {
  const isConnected = state === 'connected';
  const isTesting = state === 'testing';
  const label = isTesting
    ? 'Testing...'
    : isConnected
      ? 'Connected'
      : state === 'disconnected'
        ? 'Disconnected'
        : 'Unknown';

  const pillBg = isConnected
    ? JARVIS.greenDim
    : state === 'disconnected'
      ? JARVIS.redDim
      : JARVIS.elevated;

  const pillBorder = isConnected
    ? JARVIS.green
    : state === 'disconnected'
      ? JARVIS.red
      : JARVIS.border;

  const textColor = isConnected
    ? JARVIS.green
    : state === 'disconnected'
      ? JARVIS.red
      : JARVIS.textMuted;

  const dotColor = isConnected
    ? JARVIS.green
    : state === 'disconnected'
      ? JARVIS.red
      : JARVIS.textMuted;

  return (
    <View style={[styles.pill, { backgroundColor: pillBg, borderColor: pillBorder }]}>
      {isTesting ? (
        <ActivityIndicator size="small" color={textColor} style={styles.pillIcon} />
      ) : (
        <View style={[styles.statusDot, { backgroundColor: dotColor }]} />
      )}
      <Text style={[styles.pillText, { color: textColor }]}>{label}</Text>
    </View>
  );
}

function SectionHeader({ title }: { title: string }) {
  return <Text style={[jarvisStyles.sectionHeader, styles.sectionHeader]}>{title}</Text>;
}

function SetupGuideStep({ number, text }: { number: number; text: string }) {
  return (
    <View style={styles.guideStep}>
      <View style={styles.guideStepNumber}>
        <Text style={styles.guideStepNumberText}>{number}</Text>
      </View>
      <Text style={styles.guideStepText}>{text}</Text>
    </View>
  );
}

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function SettingsScreen() {
  // Form state
  const [serverUrl, setServerUrl] = useState('');
  const [token, setToken] = useState('');
  const [tokenRevealed, setTokenRevealed] = useState(false);

  // Connection state
  const [connectionState, setConnectionState] = useState<ConnectionState>('unknown');

  // Save feedback
  const [saveMessage, setSaveMessage] = useState<string | null>(null);

  // Test connection feedback
  const [testResult, setTestResult] = useState<'success' | 'failure' | null>(null);
  const [testPending, setTestPending] = useState(false);

  // Tailscale state
  const [tailscaleIp, setTailscaleIp] = useState('');
  const [tailscaleTestState, setTailscaleTestState] = useState<TailscaleTestState>('idle');
  const [guideExpanded, setGuideExpanded] = useState(false);

  // Timer refs for cleanup
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const tailscaleSaveTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Cleanup timers on unmount
  useEffect(() => {
    return () => {
      if (saveTimerRef.current !== undefined) clearTimeout(saveTimerRef.current);
      if (tailscaleSaveTimerRef.current !== undefined) clearTimeout(tailscaleSaveTimerRef.current);
    };
  }, []);

  // -------------------------------------------------------------------------
  // Load stored values on mount
  // -------------------------------------------------------------------------

  useEffect(() => {
    let mounted = true;

    async function loadStoredValues() {
      const [storedUrl, storedToken] = await Promise.all([
        storage.getServerUrl(),
        storage.getToken(),
      ]);

      if (!mounted) return;
      setServerUrl(storedUrl);
      setToken(storedToken ?? '');

      // Pre-fill Tailscale IP if current URL is already a Tailscale address
      if (isTailscaleUrl(storedUrl)) {
        setTailscaleIp(extractHostFromUrl(storedUrl));
      }
    }

    loadStoredValues();

    return () => {
      mounted = false;
    };
  }, []);

  // -------------------------------------------------------------------------
  // Test connection helper
  // -------------------------------------------------------------------------

  const testConnection = useCallback(async () => {
    setConnectionState('testing');
    setTestResult(null);
    setTestPending(true);

    try {
      await awmApi.ping();
      setConnectionState('connected');
      setTestResult('success');
    } catch {
      setConnectionState('disconnected');
      setTestResult('failure');
    } finally {
      setTestPending(false);
    }
  }, []);

  // -------------------------------------------------------------------------
  // Auto-test on mount (after values are loaded)
  // -------------------------------------------------------------------------

  useEffect(() => {
    // Only test once the server URL has been loaded from storage.
    // The default state is '' (before load), so wait for a non-empty value.
    if (serverUrl.length > 0) {
      testConnection();
    }
    // We only want this to run once after the initial load, not on every
    // serverUrl keystroke. The serverUrl dep is fine because on first load
    // it transitions from '' -> stored value exactly once, and testConnection
    // is stable (useCallback with no deps).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverUrl.length > 0 ? 'loaded' : 'pending']);

  // -------------------------------------------------------------------------
  // Save handler
  // -------------------------------------------------------------------------

  const handleSave = useCallback(async () => {
    await Promise.all([
      storage.setServerUrl(serverUrl.trim()),
      storage.setToken(token),
    ]);

    setSaveMessage('Saved');
    saveTimerRef.current = setTimeout(() => setSaveMessage(null), 2000);

    // Re-test connection with new values
    testConnection();
  }, [serverUrl, token, testConnection]);

  // -------------------------------------------------------------------------
  // Tailscale handlers
  // -------------------------------------------------------------------------

  const handleUseTailscale = useCallback(async () => {
    const ip = tailscaleIp.trim();
    if (!TAILSCALE_IP_REGEX.test(ip)) {
      return;
    }

    const newUrl = `http://${ip}:${DEFAULT_PORT}`;
    setServerUrl(newUrl);

    // Persist immediately
    await storage.setServerUrl(newUrl);

    setSaveMessage('Saved');
    tailscaleSaveTimerRef.current = setTimeout(() => setSaveMessage(null), 2000);

    // Test the new connection
    testConnection();
  }, [tailscaleIp, testConnection]);

  const handleTestTailscale = useCallback(async () => {
    setTailscaleTestState('testing');

    try {
      await awmApi.ping();
      setTailscaleTestState('connected');
    } catch {
      setTailscaleTestState('failed');
    }
  }, []);

  // -------------------------------------------------------------------------
  // Derived state
  // -------------------------------------------------------------------------

  const currentHost = extractHostFromUrl(serverUrl);
  const isCurrentlyTailscale = isTailscaleUrl(serverUrl);
  const isTailscaleIpValid = TAILSCALE_IP_REGEX.test(tailscaleIp.trim());

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <ScrollView
      style={styles.container}
      contentContainerStyle={styles.content}
      keyboardShouldPersistTaps="handled"
    >
      {/* Connection status pill */}
      <View style={styles.pillContainer}>
        <ConnectionStatusPill state={connectionState} />
      </View>

      {/* Server address section */}
      <View style={styles.section}>
        <SectionHeader title="Server Address" />
        <TextInput
          style={styles.input}
          value={serverUrl}
          onChangeText={setServerUrl}
          placeholder="http://192.168.1.100:4422"
          placeholderTextColor={JARVIS.textMuted}
          autoCapitalize="none"
          autoCorrect={false}
          keyboardType="url"
          returnKeyType="done"
        />
      </View>

      {/* Token section */}
      <View style={styles.section}>
        <SectionHeader title="API Token" />
        <View style={styles.tokenRow}>
          <TextInput
            style={[styles.input, styles.tokenInput]}
            value={token}
            onChangeText={setToken}
            placeholder="Enter token"
            placeholderTextColor={JARVIS.textMuted}
            secureTextEntry={!tokenRevealed}
            autoCapitalize="none"
            autoCorrect={false}
            returnKeyType="done"
          />
          <TouchableOpacity
            style={styles.revealButton}
            onPress={() => setTokenRevealed((prev) => !prev)}
            accessibilityLabel={tokenRevealed ? 'Hide token' : 'Reveal token'}
            accessibilityRole="button"
          >
            <FontAwesome
              name={tokenRevealed ? 'eye-slash' : 'eye'}
              size={20}
              color={JARVIS.textSecondary}
            />
          </TouchableOpacity>
        </View>
      </View>

      {/* Save button */}
      <TouchableOpacity
        style={styles.saveButton}
        onPress={handleSave}
        accessibilityRole="button"
        accessibilityLabel="Save settings"
      >
        <Text style={styles.saveButtonText}>Save</Text>
      </TouchableOpacity>

      {/* Save confirmation */}
      {saveMessage !== null && (
        <View style={styles.saveConfirmation}>
          <FontAwesome name="check" size={14} color={JARVIS.green} />
          <Text style={styles.saveConfirmationText}>{saveMessage}</Text>
        </View>
      )}

      {/* Test connection button */}
      <TouchableOpacity
        style={styles.testButton}
        onPress={testConnection}
        disabled={testPending}
        accessibilityRole="button"
        accessibilityLabel="Test connection"
      >
        {testPending ? (
          <ActivityIndicator size="small" color={JARVIS.bg} />
        ) : (
          <Text style={styles.testButtonText}>Test Connection</Text>
        )}
      </TouchableOpacity>

      {/* Test result feedback */}
      {testResult === 'success' && (
        <View style={styles.testResultSuccess}>
          <FontAwesome name="check-circle" size={16} color={JARVIS.green} />
          <Text style={styles.testResultSuccessText}>Connected</Text>
        </View>
      )}
      {testResult === 'failure' && (
        <View style={styles.testResultFailure}>
          <FontAwesome name="times-circle" size={16} color={JARVIS.red} />
          <Text style={styles.testResultFailureText}>
            Cannot connect -- check IP and token
          </Text>
        </View>
      )}

      {/* ----------------------------------------------------------------- */}
      {/* Remote Access (Tailscale) section                                  */}
      {/* ----------------------------------------------------------------- */}

      <View style={styles.divider} />

      <View style={styles.tailscaleHeader}>
        <FontAwesome name="globe" size={16} color={JARVIS.cyan} />
        <Text style={styles.tailscaleTitle}>Remote Access (Tailscale)</Text>
      </View>

      {/* Current connection info */}
      <View style={styles.tailscaleInfoCard}>
        <View style={styles.tailscaleInfoRow}>
          <Text style={styles.tailscaleInfoLabel}>Current server</Text>
          <Text style={styles.tailscaleInfoValue} numberOfLines={1}>
            {serverUrl || 'Not set'}
          </Text>
        </View>
        <View style={styles.tailscaleInfoRow}>
          <Text style={styles.tailscaleInfoLabel}>Connection type</Text>
          <View style={styles.connectionTypeBadge}>
            <View
              style={[
                styles.connectionTypeDot,
                {
                  backgroundColor: isCurrentlyTailscale ? JARVIS.cyan : JARVIS.textMuted,
                },
              ]}
            />
            <Text
              style={[
                styles.connectionTypeText,
                {
                  color: isCurrentlyTailscale ? JARVIS.cyan : JARVIS.textMuted,
                },
              ]}
            >
              {isCurrentlyTailscale ? 'Tailscale' : 'Local network'}
            </Text>
          </View>
        </View>
        {isCurrentlyTailscale && (
          <View style={styles.tailscaleInfoRow}>
            <Text style={styles.tailscaleInfoLabel}>Tailscale IP</Text>
            <Text style={styles.tailscaleInfoValue}>{currentHost}</Text>
          </View>
        )}
      </View>

      {/* Tailscale IP input */}
      <View style={styles.section}>
        <SectionHeader title="Tailscale IP" />
        <View style={styles.tailscaleInputRow}>
          <TextInput
            style={[styles.input, styles.tailscaleInput]}
            value={tailscaleIp}
            onChangeText={setTailscaleIp}
            placeholder="100.x.x.x"
            placeholderTextColor={JARVIS.textMuted}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="numeric"
            returnKeyType="done"
          />
          <TouchableOpacity
            style={[
              styles.useTailscaleButton,
              !isTailscaleIpValid && styles.useTailscaleButtonDisabled,
            ]}
            onPress={handleUseTailscale}
            disabled={!isTailscaleIpValid}
            accessibilityRole="button"
            accessibilityLabel="Use Tailscale IP"
          >
            <Text style={styles.useTailscaleButtonText}>Use Tailscale</Text>
          </TouchableOpacity>
        </View>
      </View>

      {/* Tailscale IP validation hint */}
      {tailscaleIp.length > 0 && !isTailscaleIpValid && (
        <Text style={styles.validationHint}>
          Enter a valid Tailscale IP (100.x.x.x)
        </Text>
      )}

      {/* Connection test for Tailscale */}
      <TouchableOpacity
        style={styles.tailscaleTestButton}
        onPress={handleTestTailscale}
        disabled={tailscaleTestState === 'testing'}
        accessibilityRole="button"
        accessibilityLabel="Test Tailscale connection"
      >
        {tailscaleTestState === 'testing' ? (
          <ActivityIndicator size="small" color={JARVIS.bg} />
        ) : (
          <>
            <FontAwesome name="plug" size={14} color={JARVIS.bg} style={styles.tailscaleTestIcon} />
            <Text style={styles.tailscaleTestButtonText}>Test Connection</Text>
          </>
        )}
      </TouchableOpacity>

      {/* Tailscale test result */}
      {tailscaleTestState === 'connected' && (
        <View style={styles.testResultSuccess}>
          <FontAwesome name="check-circle" size={16} color={JARVIS.green} />
          <Text style={styles.testResultSuccessText}>Connected via Tailscale</Text>
        </View>
      )}
      {tailscaleTestState === 'failed' && (
        <View style={styles.testResultFailure}>
          <FontAwesome name="times-circle" size={16} color={JARVIS.red} />
          <Text style={styles.testResultFailureText}>
            Cannot connect -- check Tailscale is running
          </Text>
        </View>
      )}

      {/* Setup guide */}
      <TouchableOpacity
        style={styles.guideToggle}
        onPress={() => setGuideExpanded((prev) => !prev)}
        activeOpacity={0.7}
        accessibilityRole="button"
        accessibilityLabel={guideExpanded ? 'Collapse setup guide' : 'Expand setup guide'}
      >
        <FontAwesome name="info-circle" size={14} color={JARVIS.cyan} />
        <Text style={styles.guideToggleText}>Setup Guide</Text>
        <FontAwesome
          name={guideExpanded ? 'chevron-up' : 'chevron-down'}
          size={12}
          color={JARVIS.textMuted}
        />
      </TouchableOpacity>

      {guideExpanded && (
        <View style={styles.guideContainer}>
          <SetupGuideStep
            number={1}
            text="Install Tailscale on your Mac and iPhone"
          />
          <SetupGuideStep
            number={2}
            text="Sign in with the same account on both"
          />
          <SetupGuideStep
            number={3}
            text="Find your Mac's Tailscale IP (100.x.x.x) in Tailscale app"
          />
          <SetupGuideStep
            number={4}
            text="Enter it above and tap 'Use Tailscale'"
          />
        </View>
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
    padding: 20,
    paddingBottom: 40,
  },

  // Sections wrap inputs in a holo panel
  section: {
    ...jarvisStyles.holoPanel,
    marginBottom: 16,
  },

  // Connection status pill
  pillContainer: {
    alignItems: 'center',
    marginBottom: 24,
  },
  pill: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: 14,
    paddingVertical: 6,
    borderRadius: 16,
    borderWidth: 1,
  },
  pillIcon: {
    marginRight: 8,
  },
  pillText: {
    fontSize: 13,
    fontWeight: '600',
    fontFamily: 'SpaceMono',
    letterSpacing: 0.5,
  },
  statusDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    marginRight: 8,
  },

  // Section headers
  sectionHeader: {
    marginBottom: 10,
    marginTop: 2,
  },

  // Text inputs
  input: {
    backgroundColor: JARVIS.elevated,
    borderWidth: 1,
    borderColor: JARVIS.border,
    borderRadius: 6,
    paddingHorizontal: 14,
    paddingVertical: 12,
    fontSize: 16,
    color: JARVIS.text,
    marginBottom: 0,
  },

  // Token row (input + reveal button)
  tokenRow: {
    flexDirection: 'row',
    alignItems: 'center',
  },
  tokenInput: {
    flex: 1,
    marginBottom: 0,
    borderTopRightRadius: 0,
    borderBottomRightRadius: 0,
    borderRightWidth: 0,
  },
  revealButton: {
    backgroundColor: JARVIS.elevated,
    borderWidth: 1,
    borderColor: JARVIS.border,
    borderTopRightRadius: 6,
    borderBottomRightRadius: 6,
    paddingHorizontal: 14,
    paddingVertical: 12,
    justifyContent: 'center',
    alignItems: 'center',
  },

  // Save button
  saveButton: {
    backgroundColor: JARVIS.cyan,
    borderRadius: 8,
    paddingVertical: 14,
    alignItems: 'center',
    marginBottom: 12,
  },
  saveButtonText: {
    color: JARVIS.bg,
    fontSize: 16,
    fontWeight: '700',
    letterSpacing: 0.5,
  },

  // Save confirmation
  saveConfirmation: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: JARVIS.greenDim,
    borderRadius: 6,
    paddingVertical: 8,
    paddingHorizontal: 12,
    marginBottom: 12,
  },
  saveConfirmationText: {
    color: JARVIS.green,
    fontSize: 14,
    fontWeight: '500',
    marginLeft: 6,
  },

  // Test connection button
  testButton: {
    backgroundColor: JARVIS.elevated,
    borderWidth: 1,
    borderColor: JARVIS.borderBright,
    borderRadius: 8,
    paddingVertical: 14,
    alignItems: 'center',
    marginBottom: 12,
  },
  testButtonText: {
    color: JARVIS.text,
    fontSize: 16,
    fontWeight: '600',
  },

  // Test result feedback
  testResultSuccess: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: JARVIS.greenDim,
    borderWidth: 1,
    borderColor: 'rgba(0, 255, 136, 0.2)',
    borderRadius: 8,
    paddingVertical: 10,
    paddingHorizontal: 14,
    marginBottom: 8,
  },
  testResultSuccessText: {
    color: JARVIS.green,
    fontSize: 14,
    fontWeight: '500',
    marginLeft: 8,
  },
  testResultFailure: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: JARVIS.redDim,
    borderWidth: 1,
    borderColor: 'rgba(255, 71, 87, 0.2)',
    borderRadius: 8,
    paddingVertical: 10,
    paddingHorizontal: 14,
    marginBottom: 8,
  },
  testResultFailureText: {
    color: JARVIS.red,
    fontSize: 14,
    fontWeight: '500',
    marginLeft: 8,
  },

  // Divider
  divider: {
    height: 1,
    backgroundColor: JARVIS.border,
    marginVertical: 24,
  },

  // Tailscale section header
  tailscaleHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
    marginBottom: 16,
  },
  tailscaleTitle: {
    fontSize: 17,
    fontWeight: '700',
    color: JARVIS.text,
    letterSpacing: 0.5,
  },

  // Tailscale info card
  tailscaleInfoCard: {
    ...jarvisStyles.holoPanel,
    marginBottom: 16,
    gap: 10,
  },
  tailscaleInfoRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  tailscaleInfoLabel: {
    fontSize: 13,
    color: JARVIS.textSecondary,
    fontWeight: '500',
  },
  tailscaleInfoValue: {
    fontSize: 13,
    color: JARVIS.text,
    fontWeight: '600',
    flexShrink: 1,
    marginLeft: 12,
    textAlign: 'right',
    fontFamily: 'SpaceMono',
  },

  // Connection type badge
  connectionTypeBadge: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 5,
  },
  connectionTypeDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  connectionTypeText: {
    fontSize: 13,
    fontWeight: '600',
  },

  // Tailscale IP input row
  tailscaleInputRow: {
    flexDirection: 'row',
    alignItems: 'center',
  },
  tailscaleInput: {
    flex: 1,
    marginBottom: 0,
    borderTopRightRadius: 0,
    borderBottomRightRadius: 0,
    borderRightWidth: 0,
  },
  useTailscaleButton: {
    backgroundColor: JARVIS.cyan,
    borderTopRightRadius: 6,
    borderBottomRightRadius: 6,
    paddingHorizontal: 14,
    paddingVertical: 13,
    justifyContent: 'center',
    alignItems: 'center',
  },
  useTailscaleButtonDisabled: {
    opacity: 0.3,
  },
  useTailscaleButtonText: {
    color: JARVIS.bg,
    fontSize: 14,
    fontWeight: '700',
  },

  // Validation hint
  validationHint: {
    fontSize: 12,
    color: JARVIS.red,
    marginBottom: 12,
    marginTop: -8,
    marginLeft: 16,
  },

  // Tailscale test button
  tailscaleTestButton: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: JARVIS.elevated,
    borderWidth: 1,
    borderColor: JARVIS.borderBright,
    borderRadius: 8,
    paddingVertical: 14,
    marginTop: 12,
    marginBottom: 12,
  },
  tailscaleTestIcon: {
    marginRight: 8,
  },
  tailscaleTestButtonText: {
    color: JARVIS.text,
    fontSize: 16,
    fontWeight: '600',
  },

  // Setup guide toggle
  guideToggle: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
    paddingVertical: 12,
    marginTop: 8,
  },
  guideToggleText: {
    fontSize: 14,
    color: JARVIS.cyan,
    fontWeight: '600',
    flex: 1,
  },

  // Setup guide content
  guideContainer: {
    ...jarvisStyles.holoPanel,
    gap: 12,
    marginBottom: 16,
  },
  guideStep: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    gap: 10,
  },
  guideStepNumber: {
    width: 22,
    height: 22,
    borderRadius: 11,
    backgroundColor: JARVIS.cyan,
    alignItems: 'center',
    justifyContent: 'center',
  },
  guideStepNumberText: {
    fontSize: 12,
    fontWeight: '700',
    color: JARVIS.bg,
  },
  guideStepText: {
    fontSize: 14,
    color: JARVIS.textSecondary,
    flex: 1,
    lineHeight: 20,
  },
});
