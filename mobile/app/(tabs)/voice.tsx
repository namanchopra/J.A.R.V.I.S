import React, { useCallback, useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from 'react-native';
import {
  AudioSession,
  registerGlobals,
} from '@livekit/react-native';
import {
  Room,
  RoomEvent,
  Track,
  type RemoteAudioTrack,
} from 'livekit-client';

import { awmApi, ApiError } from '@/lib/api';
import { JARVIS } from '@/constants/Colors';

// Register WebRTC globals once per process. Safe to call multiple times.
registerGlobals();

type Status =
  | { kind: 'idle' }
  | { kind: 'fetching-token' }
  | { kind: 'connecting'; url: string; room: string }
  | { kind: 'connected'; url: string; room: string }
  | { kind: 'error'; message: string };

export default function VoiceScreen() {
  const [status, setStatus] = useState<Status>({ kind: 'idle' });
  const [participantCount, setParticipantCount] = useState(0);
  const [botSpeaking, setBotSpeaking] = useState(false);
  const roomRef = useRef<Room | null>(null);

  const cleanup = useCallback(async () => {
    const r = roomRef.current;
    roomRef.current = null;
    if (r) {
      try {
        await r.disconnect();
      } catch {
        // best-effort
      }
    }
    try {
      await AudioSession.stopAudioSession();
    } catch {
      // best-effort
    }
  }, []);

  const connect = useCallback(async () => {
    try {
      setStatus({ kind: 'fetching-token' });
      const tk = await awmApi.getLiveKitToken('phone');

      setStatus({ kind: 'connecting', url: tk.url, room: tk.room });
      await AudioSession.startAudioSession();

      const room = new Room({
        adaptiveStream: true,
        dynacast: true,
      });

      room.on(RoomEvent.ParticipantConnected, () => {
        setParticipantCount(room.numParticipants);
      });
      room.on(RoomEvent.ParticipantDisconnected, () => {
        setParticipantCount(room.numParticipants);
      });
      room.on(RoomEvent.TrackSubscribed, (track) => {
        if (track.kind === Track.Kind.Audio) {
          // Play the bot's audio. attach() returns the underlying audio element
          // managed by the WebRTC native module — no manual <audio> needed on RN.
          (track as RemoteAudioTrack).attach();
        }
      });
      room.on(RoomEvent.ActiveSpeakersChanged, (speakers) => {
        const botSpeakingNow = speakers.some(
          (s) => s.identity === 'jarvis' || s.identity?.toLowerCase().includes('jarvis'),
        );
        setBotSpeaking(botSpeakingNow);
      });
      room.on(RoomEvent.Disconnected, () => {
        setStatus({ kind: 'idle' });
        setParticipantCount(0);
        setBotSpeaking(false);
      });

      await room.connect(tk.url, tk.token);
      await room.localParticipant.setMicrophoneEnabled(true);

      roomRef.current = room;
      setParticipantCount(room.numParticipants);
      setStatus({ kind: 'connected', url: tk.url, room: tk.room });
    } catch (err: unknown) {
      const message =
        err instanceof ApiError
          ? `${err.status === 503 ? 'LiveKit not configured on the daemon. ' : ''}${err.message}`
          : err instanceof Error
            ? err.message
            : 'Unknown error';
      await cleanup();
      setStatus({ kind: 'error', message });
    }
  }, [cleanup]);

  const disconnect = useCallback(async () => {
    await cleanup();
    setStatus({ kind: 'idle' });
    setParticipantCount(0);
    setBotSpeaking(false);
  }, [cleanup]);

  // Disconnect on unmount.
  useEffect(() => {
    return () => {
      void cleanup();
    };
  }, [cleanup]);

  const isBusy = status.kind === 'fetching-token' || status.kind === 'connecting';
  const isConnected = status.kind === 'connected';

  return (
    <View style={styles.container}>
      <View style={styles.statusCard}>
        <Text style={styles.statusLabel}>Status</Text>
        <Text style={styles.statusValue}>{describeStatus(status)}</Text>

        {status.kind === 'connected' && (
          <>
            <Text style={styles.metaLabel}>Room</Text>
            <Text style={styles.metaValue}>{status.room}</Text>
            <Text style={styles.metaLabel}>Participants</Text>
            <Text style={styles.metaValue}>{participantCount}</Text>
            <Text style={styles.metaLabel}>Jarvis</Text>
            <Text style={[styles.metaValue, botSpeaking && styles.speakingHighlight]}>
              {botSpeaking ? 'speaking…' : 'listening'}
            </Text>
          </>
        )}

        {status.kind === 'error' && (
          <Text style={styles.errorText}>{status.message}</Text>
        )}
      </View>

      {!isConnected ? (
        <Pressable
          onPress={connect}
          disabled={isBusy}
          style={[styles.button, isBusy && styles.buttonDisabled]}
        >
          {isBusy ? (
            <ActivityIndicator color={JARVIS.cyan} />
          ) : (
            <Text style={styles.buttonText}>Talk to Jarvis</Text>
          )}
        </Pressable>
      ) : (
        <Pressable onPress={disconnect} style={[styles.button, styles.buttonDanger]}>
          <Text style={styles.buttonText}>Hang up</Text>
        </Pressable>
      )}

      <Text style={styles.hint}>
        Spike: requires the daemon to run with `useLiveKitTransport: true` and the same
        room name configured on both sides.
      </Text>
    </View>
  );
}

function describeStatus(s: Status): string {
  switch (s.kind) {
    case 'idle':
      return 'disconnected';
    case 'fetching-token':
      return 'requesting token…';
    case 'connecting':
      return `connecting to ${s.room}…`;
    case 'connected':
      return 'connected';
    case 'error':
      return 'error';
  }
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    padding: 20,
    backgroundColor: JARVIS.bg,
    gap: 16,
  },
  statusCard: {
    padding: 16,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: JARVIS.border,
    backgroundColor: JARVIS.surface,
    gap: 6,
  },
  statusLabel: {
    color: JARVIS.textMuted,
    fontSize: 12,
    textTransform: 'uppercase',
    letterSpacing: 1,
  },
  statusValue: {
    color: JARVIS.text,
    fontSize: 18,
    fontWeight: '600',
    marginBottom: 8,
  },
  metaLabel: {
    color: JARVIS.textMuted,
    fontSize: 11,
    textTransform: 'uppercase',
    letterSpacing: 1,
    marginTop: 4,
  },
  metaValue: {
    color: JARVIS.text,
    fontSize: 14,
    fontFamily: 'monospace',
  },
  speakingHighlight: {
    color: JARVIS.cyan,
  },
  errorText: {
    color: '#ff6b6b',
    fontSize: 13,
    marginTop: 6,
  },
  button: {
    paddingVertical: 16,
    borderRadius: 12,
    alignItems: 'center',
    backgroundColor: JARVIS.surface,
    borderWidth: 1,
    borderColor: JARVIS.cyan,
  },
  buttonDisabled: {
    opacity: 0.5,
  },
  buttonDanger: {
    borderColor: '#ff6b6b',
  },
  buttonText: {
    color: JARVIS.text,
    fontSize: 16,
    fontWeight: '600',
  },
  hint: {
    color: JARVIS.textMuted,
    fontSize: 12,
    marginTop: 'auto',
    lineHeight: 18,
  },
});
