import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Animated,
  FlatList,
  Keyboard,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { Audio } from 'expo-av';
import * as FileSystem from 'expo-file-system/legacy';
import FontAwesome from '@expo/vector-icons/FontAwesome';

import { awmApi } from '../../lib/api';
import { storage } from '../../lib/storage';
import { JARVIS } from '../../constants/Colors';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface ChatMessage {
  id: string;
  role: 'user' | 'jarvis' | 'system';
  text: string;
  timestamp: Date;
}

type VoiceState = 'idle' | 'recording' | 'processing';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

let nextId = 0;
function generateId(): string {
  nextId += 1;
  return `msg-${Date.now()}-${nextId}`;
}

function formatTime(date: Date): string {
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

/** Build the WebSocket URL from the stored server URL and token. */
async function buildWsUrl(): Promise<string> {
  const serverUrl = await storage.getServerUrl();
  const token = await storage.getToken();
  // Convert http(s) to ws(s)
  const wsBase = serverUrl.replace(/^http/, 'ws');
  return `${wsBase}/ws/jarvis-mobile${token ? `?token=${token}` : ''}`;
}

// Minimum hold duration in ms to count as a real recording
const MIN_HOLD_MS = 500;

// ---------------------------------------------------------------------------
// Message bubble
// ---------------------------------------------------------------------------

const MessageBubble = React.memo(function MessageBubble({
  message,
}: {
  message: ChatMessage;
}) {
  const isUser = message.role === 'user';
  const isSystem = message.role === 'system';

  if (isSystem) {
    return (
      <View style={styles.systemRow}>
        <Text style={styles.systemText}>{message.text}</Text>
      </View>
    );
  }

  return (
    <View
      style={[
        styles.bubbleRow,
        isUser ? styles.bubbleRowUser : styles.bubbleRowJarvis,
      ]}
    >
      <View
        style={[
          styles.bubble,
          isUser ? styles.bubbleUser : styles.bubbleJarvis,
        ]}
      >
        {!isUser && <Text style={styles.jarvisLabel}>JARVIS</Text>}
        <Text style={styles.bubbleText}>{message.text}</Text>
        <Text style={styles.timestamp}>{formatTime(message.timestamp)}</Text>
      </View>
    </View>
  );
});

// ---------------------------------------------------------------------------
// Typing indicator
// ---------------------------------------------------------------------------

function TypingIndicator() {
  return (
    <View style={[styles.bubbleRow, styles.bubbleRowJarvis]}>
      <View style={[styles.bubble, styles.bubbleJarvis]}>
        <Text style={styles.jarvisLabel}>JARVIS</Text>
        <View style={styles.typingRow}>
          <ActivityIndicator size="small" color={JARVIS.cyan} />
          <Text style={styles.typingText}>Thinking...</Text>
        </View>
      </View>
    </View>
  );
}

// ---------------------------------------------------------------------------
// Pulsing recording ring (animated)
// ---------------------------------------------------------------------------

function PulsingRing({ active }: { active: boolean }) {
  const scaleAnim = useRef(new Animated.Value(1)).current;
  const opacityAnim = useRef(new Animated.Value(0.6)).current;

  useEffect(() => {
    if (active) {
      const pulse = Animated.loop(
        Animated.sequence([
          Animated.parallel([
            Animated.timing(scaleAnim, {
              toValue: 1.35,
              duration: 800,
              useNativeDriver: true,
            }),
            Animated.timing(opacityAnim, {
              toValue: 0,
              duration: 800,
              useNativeDriver: true,
            }),
          ]),
          Animated.parallel([
            Animated.timing(scaleAnim, {
              toValue: 1,
              duration: 0,
              useNativeDriver: true,
            }),
            Animated.timing(opacityAnim, {
              toValue: 0.6,
              duration: 0,
              useNativeDriver: true,
            }),
          ]),
        ]),
      );
      pulse.start();
      return () => pulse.stop();
    } else {
      scaleAnim.setValue(1);
      opacityAnim.setValue(0.6);
    }
  }, [active, scaleAnim, opacityAnim]);

  if (!active) return null;

  return (
    <Animated.View
      pointerEvents="none"
      style={[
        styles.pulsingRing,
        { transform: [{ scale: scaleAnim }], opacity: opacityAnim },
      ]}
    />
  );
}

// ---------------------------------------------------------------------------
// Voice button
// ---------------------------------------------------------------------------

function VoiceButton({
  voiceState,
  onPressIn,
  onPressOut,
  hint,
  disabled,
}: {
  voiceState: VoiceState;
  onPressIn: () => void;
  onPressOut: () => void;
  hint: string | null;
  disabled: boolean;
}) {
  const isRecording = voiceState === 'recording';
  const isProcessing = voiceState === 'processing';

  return (
    <View style={styles.voiceContainer}>
      <View style={styles.voiceButtonWrapper}>
        <PulsingRing active={isRecording} />
        <Pressable
          onPressIn={onPressIn}
          onPressOut={onPressOut}
          disabled={disabled || isProcessing}
          style={({ pressed }) => [
            styles.voiceButton,
            isRecording && styles.voiceButtonRecording,
            isProcessing && styles.voiceButtonProcessing,
            pressed && !isRecording && !isProcessing && styles.voiceButtonPressed,
          ]}
          accessibilityRole="button"
          accessibilityLabel={
            isRecording
              ? 'Release to stop recording'
              : isProcessing
                ? 'Jarvis is processing your voice'
                : 'Hold to talk to Jarvis'
          }
          accessibilityHint="Press and hold to record a voice message"
        >
          {isProcessing ? (
            <ActivityIndicator size="small" color={JARVIS.cyan} />
          ) : (
            <FontAwesome
              name="microphone"
              size={24}
              color={isRecording ? JARVIS.recording : JARVIS.cyan}
            />
          )}
        </Pressable>
      </View>

      <Text
        style={[
          styles.voiceLabel,
          isRecording && styles.voiceLabelRecording,
          isProcessing && styles.voiceLabelProcessing,
        ]}
      >
        {isRecording
          ? 'Recording...'
          : isProcessing
            ? 'Jarvis is thinking...'
            : 'Hold to talk'}
      </Text>

      {hint !== null && <Text style={styles.voiceHint}>{hint}</Text>}
    </View>
  );
}

// ---------------------------------------------------------------------------
// Main screen
// ---------------------------------------------------------------------------

export default function JarvisScreen() {
  const insets = useSafeAreaInsets();
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [inputText, setInputText] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [inputFocused, setInputFocused] = useState(false);

  // Voice state
  const [voiceState, setVoiceState] = useState<VoiceState>('idle');
  const [voiceHint, setVoiceHint] = useState<string | null>(null);
  const recordingRef = useRef<Audio.Recording | null>(null);
  const pressStartRef = useRef<number>(0);
  const wsRef = useRef<WebSocket | null>(null);
  const voiceHintTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const flatListRef = useRef<FlatList<ChatMessage>>(null);
  const scrollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const isNearBottomRef = useRef(true);

  // -------------------------------------------------------------------------
  // Auto-scroll to bottom (only when user is near the bottom)
  // -------------------------------------------------------------------------

  const scrollToEnd = useCallback(() => {
    if (!isNearBottomRef.current) return;
    if (scrollTimerRef.current) clearTimeout(scrollTimerRef.current);
    scrollTimerRef.current = setTimeout(() => {
      flatListRef.current?.scrollToEnd({ animated: true });
    }, 100);
  }, []);

  // -------------------------------------------------------------------------
  // Append a message to chat
  // -------------------------------------------------------------------------

  const appendMessage = useCallback(
    (role: ChatMessage['role'], text: string) => {
      const msg: ChatMessage = {
        id: generateId(),
        role,
        text,
        timestamp: new Date(),
      };
      setMessages((prev) => [...prev, msg]);
      scrollToEnd();
    },
    [scrollToEnd],
  );

  // -------------------------------------------------------------------------
  // WebSocket connection for voice
  // -------------------------------------------------------------------------

  const connectWs = useCallback(async (): Promise<WebSocket | null> => {
    // Reuse if already open
    if (
      wsRef.current &&
      wsRef.current.readyState === WebSocket.OPEN
    ) {
      return wsRef.current;
    }

    try {
      const url = await buildWsUrl();
      const ws = new WebSocket(url);

      return new Promise<WebSocket | null>((resolve) => {
        const timeout = setTimeout(() => {
          ws.close();
          resolve(null);
        }, 5000);

        ws.onopen = () => {
          clearTimeout(timeout);
          wsRef.current = ws;
          resolve(ws);
        };

        ws.onmessage = (event) => {
          try {
            const data = JSON.parse(event.data as string) as Record<string, unknown>;
            const msgType = data.type as string | undefined;

            if (msgType === 'response' && typeof data.text === 'string') {
              appendMessage('jarvis', data.text);
              setVoiceState('idle');
              setIsLoading(false);
            } else if (msgType === 'state') {
              // Could use for "speaking" indicator in future
            } else if (msgType === 'mobile_tts') {
              // TTS audio -- future: play base64 PCM
              // For MVP we rely on the text response
            } else if (msgType === 'error' && typeof data.message === 'string') {
              appendMessage('system', `Error: ${data.message}`);
              setVoiceState('idle');
              setIsLoading(false);
            }
          } catch {
            // Non-JSON message, ignore
          }
        };

        ws.onerror = () => {
          clearTimeout(timeout);
          wsRef.current = null;
          resolve(null);
        };

        ws.onclose = () => {
          wsRef.current = null;
        };
      });
    } catch {
      return null;
    }
  }, [appendMessage]);

  // Cleanup WebSocket on unmount
  useEffect(() => {
    return () => {
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, []);

  // -------------------------------------------------------------------------
  // Audio recording
  // -------------------------------------------------------------------------

  const startRecording = useCallback(async () => {
    // Clear any previous hint
    if (voiceHintTimerRef.current) {
      clearTimeout(voiceHintTimerRef.current);
      voiceHintTimerRef.current = null;
    }
    setVoiceHint(null);

    try {
      // Request permission
      const { granted } = await Audio.requestPermissionsAsync();
      if (!granted) {
        appendMessage('system', 'Microphone permission is required for voice input.');
        return;
      }

      // Configure audio mode for recording
      await Audio.setAudioModeAsync({
        allowsRecordingIOS: true,
        playsInSilentModeIOS: true,
      });

      // Create and start recording
      const recording = new Audio.Recording();
      await recording.prepareToRecordAsync({
        android: {
          extension: '.wav',
          outputFormat: Audio.AndroidOutputFormat.DEFAULT,
          audioEncoder: Audio.AndroidAudioEncoder.DEFAULT,
          sampleRate: 16000,
          numberOfChannels: 1,
          bitRate: 256000,
        },
        ios: {
          extension: '.wav',
          outputFormat: Audio.IOSOutputFormat.LINEARPCM,
          audioQuality: Audio.IOSAudioQuality.HIGH,
          sampleRate: 16000,
          numberOfChannels: 1,
          bitRate: 256000,
          linearPCMBitDepth: 16,
          linearPCMIsBigEndian: false,
          linearPCMIsFloat: false,
        },
        web: {},
      });
      await recording.startAsync();

      recordingRef.current = recording;
      pressStartRef.current = Date.now();
      setVoiceState('recording');
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to start recording';
      appendMessage('system', `Recording error: ${msg}`);
      setVoiceState('idle');
    }
  }, [appendMessage]);

  const stopRecordingAndSend = useCallback(async () => {
    const recording = recordingRef.current;
    if (!recording) {
      setVoiceState('idle');
      return;
    }
    recordingRef.current = null;

    const holdDuration = Date.now() - pressStartRef.current;

    try {
      await recording.stopAndUnloadAsync();

      // Reset audio mode so playback works normally
      await Audio.setAudioModeAsync({
        allowsRecordingIOS: false,
        playsInSilentModeIOS: true,
      });

      // Quick-release guard
      if (holdDuration < MIN_HOLD_MS) {
        setVoiceState('idle');
        setVoiceHint('Hold longer to speak');
        voiceHintTimerRef.current = setTimeout(() => {
          setVoiceHint(null);
          voiceHintTimerRef.current = null;
        }, 2000);
        return;
      }

      const uri = recording.getURI();
      if (!uri) {
        appendMessage('system', 'Recording failed -- no audio file produced.');
        setVoiceState('idle');
        return;
      }

      // Show processing state
      setVoiceState('processing');
      setIsLoading(true);
      appendMessage('user', '[Voice message]');

      // Read file as base64
      const base64Audio = await FileSystem.readAsStringAsync(uri, {
        encoding: FileSystem.EncodingType.Base64,
      });

      // Connect WebSocket and send
      const ws = await connectWs();
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        // Fallback: WS unavailable, notify user
        appendMessage(
          'system',
          'Could not connect to Jarvis voice service. Try sending a text message instead.',
        );
        setVoiceState('idle');
        setIsLoading(false);
        return;
      }

      // Send audio as JSON message with base64 payload
      ws.send(
        JSON.stringify({
          type: 'mobile_audio',
          data: base64Audio,
          sampleRate: 16000,
          channels: 1,
          bitDepth: 16,
        }),
      );

      // Response will arrive via ws.onmessage, which sets voiceState back to idle

      // Clean up the temp file
      FileSystem.deleteAsync(uri, { idempotent: true }).catch(() => {
        // Best effort cleanup
      });
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to process recording';
      appendMessage('system', `Voice error: ${msg}`);
      setVoiceState('idle');
      setIsLoading(false);
    }
  }, [appendMessage, connectWs]);

  // Cleanup recording and timers on unmount
  useEffect(() => {
    return () => {
      if (recordingRef.current) {
        recordingRef.current.stopAndUnloadAsync().catch(() => {});
        recordingRef.current = null;
      }
      if (voiceHintTimerRef.current) {
        clearTimeout(voiceHintTimerRef.current);
      }
      if (scrollTimerRef.current) {
        clearTimeout(scrollTimerRef.current);
      }
    };
  }, []);

  // -------------------------------------------------------------------------
  // Send text message
  // -------------------------------------------------------------------------

  const handleSend = useCallback(async () => {
    const trimmed = inputText.trim();
    if (trimmed.length === 0 || isLoading) return;

    Keyboard.dismiss();
    setInputText('');

    const userMessage: ChatMessage = {
      id: generateId(),
      role: 'user',
      text: trimmed,
      timestamp: new Date(),
    };

    setMessages((prev) => [...prev, userMessage]);
    setIsLoading(true);
    scrollToEnd();

    try {
      const result = await awmApi.jarvisChat(trimmed);

      const jarvisMessage: ChatMessage = {
        id: generateId(),
        role: 'jarvis',
        text: result.response,
        timestamp: new Date(),
      };

      setMessages((prev) => [...prev, jarvisMessage]);
    } catch (err: unknown) {
      const errorText =
        err instanceof Error ? err.message : 'Failed to reach Jarvis';

      const errorMessage: ChatMessage = {
        id: generateId(),
        role: 'system',
        text: `Error: ${errorText}`,
        timestamp: new Date(),
      };

      setMessages((prev) => [...prev, errorMessage]);
    } finally {
      setIsLoading(false);
      scrollToEnd();
    }
  }, [inputText, isLoading, scrollToEnd]);

  // -------------------------------------------------------------------------
  // Render helpers
  // -------------------------------------------------------------------------

  const renderItem = useCallback(
    ({ item }: { item: ChatMessage }) => <MessageBubble message={item} />,
    [],
  );

  const keyExtractor = useCallback((item: ChatMessage) => item.id, []);

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  const isVoiceBusy = voiceState !== 'idle';

  return (
    <KeyboardAvoidingView
      style={styles.container}
      behavior={Platform.OS === 'ios' ? 'padding' : 'height'}
      keyboardVerticalOffset={Platform.OS === 'ios' ? 90 : 0}
    >
      {/* Message list */}
      <FlatList
        ref={flatListRef}
        data={messages}
        renderItem={renderItem}
        keyExtractor={keyExtractor}
        contentContainerStyle={[
          styles.listContent,
          messages.length === 0 && styles.listEmpty,
        ]}
        onScroll={(e) => {
          const { contentOffset, contentSize, layoutMeasurement } = e.nativeEvent;
          isNearBottomRef.current =
            contentOffset.y >= contentSize.height - layoutMeasurement.height - 50;
        }}
        scrollEventThrottle={64}
        ListEmptyComponent={
          <View style={styles.emptyContainer}>
            <Text style={styles.emptyIcon}>J</Text>
            <Text style={styles.emptyTitle}>Jarvis</Text>
            <Text style={styles.emptySubtitle}>
              Your AI assistant is ready. Ask anything about your projects,
              sessions, or tasks.
            </Text>
          </View>
        }
        ListFooterComponent={
          isLoading && voiceState !== 'processing' ? <TypingIndicator /> : null
        }
        onContentSizeChange={scrollToEnd}
        keyboardDismissMode="interactive"
        keyboardShouldPersistTaps="handled"
      />

      {/* Voice button */}
      <VoiceButton
        voiceState={voiceState}
        onPressIn={startRecording}
        onPressOut={stopRecordingAndSend}
        hint={voiceHint}
        disabled={isLoading && !isVoiceBusy}
      />

      {/* Text input bar */}
      <View
        style={[
          styles.inputBar,
          { paddingBottom: Math.max(insets.bottom, 8) },
        ]}
      >
        <TextInput
          style={[
            styles.textInput,
            inputFocused && styles.textInputFocused,
          ]}
          value={inputText}
          onChangeText={setInputText}
          placeholder="Message Jarvis..."
          placeholderTextColor={JARVIS.chatTextMuted}
          multiline
          maxLength={2000}
          returnKeyType="default"
          onFocus={() => setInputFocused(true)}
          onBlur={() => setInputFocused(false)}
          editable={!isLoading}
          accessibilityLabel="Message input"
          accessibilityHint="Type a message to send to Jarvis"
        />
        <TouchableOpacity
          style={[
            styles.sendButton,
            (inputText.trim().length === 0 || isLoading) &&
              styles.sendButtonDisabled,
          ]}
          onPress={handleSend}
          disabled={inputText.trim().length === 0 || isLoading}
          activeOpacity={0.7}
          accessibilityRole="button"
          accessibilityLabel="Send message"
        >
          {isLoading && voiceState !== 'processing' ? (
            <ActivityIndicator size="small" color={JARVIS.bg} />
          ) : (
            <Text style={styles.sendButtonText}>Send</Text>
          )}
        </TouchableOpacity>
      </View>
    </KeyboardAvoidingView>
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

  // Message list
  listContent: {
    paddingHorizontal: 16,
    paddingTop: 16,
    paddingBottom: 8,
  },
  listEmpty: {
    flexGrow: 1,
    justifyContent: 'center',
    alignItems: 'center',
  },

  // Empty state
  emptyContainer: {
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 32,
  },
  emptyIcon: {
    fontSize: 48,
    fontWeight: '800',
    color: JARVIS.cyan,
    marginBottom: 12,
    letterSpacing: 2,
  },
  emptyTitle: {
    fontSize: 22,
    fontWeight: '700',
    color: JARVIS.text,
    marginBottom: 8,
  },
  emptySubtitle: {
    fontSize: 14,
    color: JARVIS.chatTextMuted,
    textAlign: 'center',
    lineHeight: 20,
  },

  // Bubble row
  bubbleRow: {
    marginBottom: 12,
    flexDirection: 'row',
  },
  bubbleRowUser: {
    justifyContent: 'flex-end',
  },
  bubbleRowJarvis: {
    justifyContent: 'flex-start',
  },

  // Bubble
  bubble: {
    maxWidth: '80%',
    borderRadius: 16,
    paddingHorizontal: 14,
    paddingVertical: 10,
  },
  bubbleUser: {
    backgroundColor: JARVIS.userBubble,
    borderBottomRightRadius: 4,
  },
  bubbleJarvis: {
    backgroundColor: JARVIS.surface,
    borderBottomLeftRadius: 4,
    borderWidth: 1,
    borderColor: JARVIS.inputBorder,
  },

  // Labels & text
  jarvisLabel: {
    fontSize: 10,
    fontWeight: '700',
    color: JARVIS.cyan,
    letterSpacing: 1.5,
    marginBottom: 4,
  },
  bubbleText: {
    fontSize: 15,
    color: JARVIS.text,
    lineHeight: 21,
  },
  timestamp: {
    fontSize: 10,
    color: JARVIS.chatTextMuted,
    marginTop: 4,
    alignSelf: 'flex-end',
  },

  // System message
  systemRow: {
    alignItems: 'center',
    marginBottom: 12,
    paddingHorizontal: 16,
  },
  systemText: {
    fontSize: 13,
    color: JARVIS.recording,
    fontStyle: 'italic',
    textAlign: 'center',
  },

  // Typing indicator
  typingRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
  },
  typingText: {
    fontSize: 13,
    color: JARVIS.chatTextMuted,
    fontStyle: 'italic',
  },

  // Voice button area
  voiceContainer: {
    alignItems: 'center',
    paddingVertical: 12,
    borderTopWidth: 1,
    borderTopColor: JARVIS.headerBorder,
  },
  voiceButtonWrapper: {
    width: 64,
    height: 64,
    alignItems: 'center',
    justifyContent: 'center',
  },
  voiceButton: {
    width: 56,
    height: 56,
    borderRadius: 28,
    backgroundColor: JARVIS.inputBg,
    borderWidth: 2,
    borderColor: JARVIS.cyan,
    alignItems: 'center',
    justifyContent: 'center',
  },
  voiceButtonPressed: {
    backgroundColor: JARVIS.cyanFaint,
    borderColor: JARVIS.cyan,
  },
  voiceButtonRecording: {
    backgroundColor: JARVIS.recordingBg,
    borderColor: JARVIS.recording,
    borderWidth: 2.5,
  },
  voiceButtonProcessing: {
    backgroundColor: JARVIS.inputBg,
    borderColor: JARVIS.chatTextMuted,
    opacity: 0.7,
  },
  pulsingRing: {
    position: 'absolute',
    width: 56,
    height: 56,
    borderRadius: 28,
    borderWidth: 2,
    borderColor: JARVIS.recording,
  },
  voiceLabel: {
    marginTop: 6,
    fontSize: 12,
    color: JARVIS.chatTextMuted,
    fontWeight: '500',
  },
  voiceLabelRecording: {
    color: JARVIS.recording,
    fontWeight: '600',
  },
  voiceLabelProcessing: {
    color: JARVIS.cyan,
    fontStyle: 'italic',
  },
  voiceHint: {
    marginTop: 4,
    fontSize: 11,
    color: JARVIS.recording,
    fontWeight: '500',
  },

  // Input bar
  inputBar: {
    flexDirection: 'row',
    alignItems: 'flex-end',
    paddingHorizontal: 12,
    paddingTop: 8,
    borderTopWidth: 1,
    borderTopColor: JARVIS.headerBorder,
    backgroundColor: JARVIS.bg,
    gap: 8,
  },
  textInput: {
    flex: 1,
    minHeight: 40,
    maxHeight: 120,
    backgroundColor: JARVIS.inputBg,
    borderRadius: 20,
    paddingHorizontal: 16,
    paddingTop: Platform.OS === 'ios' ? 10 : 8,
    paddingBottom: Platform.OS === 'ios' ? 10 : 8,
    fontSize: 15,
    color: JARVIS.text,
    borderWidth: 1,
    borderColor: JARVIS.inputBorder,
  },
  textInputFocused: {
    borderColor: JARVIS.cyan,
  },

  // Send button
  sendButton: {
    height: 40,
    paddingHorizontal: 16,
    borderRadius: 20,
    backgroundColor: JARVIS.cyan,
    justifyContent: 'center',
    alignItems: 'center',
    marginBottom: Platform.OS === 'ios' ? 0 : 0,
  },
  sendButtonDisabled: {
    opacity: 0.4,
  },
  sendButtonText: {
    fontSize: 15,
    fontWeight: '700',
    color: JARVIS.bg,
  },
});
