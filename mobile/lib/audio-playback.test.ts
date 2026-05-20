/**
 * Unit tests for `mobile/lib/audio-playback.ts`.
 *
 * These tests exercise the pure helpers (wrapWav, concatChunks) directly and
 * exercise the `AudioPlayer` class via injected dependency seams so they do
 * not require `expo-av` or `expo-file-system` to be wired up to a real
 * device. They are written for Jest + jest-expo preset.
 */

jest.mock('expo-av', () => ({
  Audio: {
    setAudioModeAsync: jest.fn(async () => undefined),
    Sound: {
      createAsync: jest.fn(async () => ({
        sound: { stopAsync: async () => {}, unloadAsync: async () => {} },
        status: { isLoaded: true, didJustFinish: false },
      })),
    },
  },
}));

jest.mock('expo-file-system/legacy', () => ({
  cacheDirectory: 'file:///cache/',
  EncodingType: { Base64: 'base64', UTF8: 'utf8' },
  writeAsStringAsync: jest.fn(async () => undefined),
  deleteAsync: jest.fn(async () => undefined),
}));

import {
  AudioPlayer,
  type PlayableSound,
  type PlaybackDeps,
  concatChunks,
  uint8ArrayToBase64,
  wrapWav,
} from './audio-playback';

// ---------------------------------------------------------------------------
// wrapWav — pure header construction
// ---------------------------------------------------------------------------

describe('wrapWav', () => {
  it('produces a 44-byte header + len(pcm) bytes', () => {
    const pcm = new Uint8Array(200);
    const out = wrapWav(pcm, 16000, 1, 16);
    expect(out.byteLength).toBe(44 + pcm.byteLength);
  });

  it('writes the RIFF / WAVE / fmt  / data tokens at the correct offsets', () => {
    const pcm = new Uint8Array(32);
    const out = wrapWav(pcm, 16000, 1, 16);
    const ascii = (i: number, n: number): string =>
      Array.from(out.subarray(i, i + n))
        .map((b) => String.fromCharCode(b))
        .join('');
    expect(ascii(0, 4)).toBe('RIFF');
    expect(ascii(8, 4)).toBe('WAVE');
    expect(ascii(12, 4)).toBe('fmt ');
    expect(ascii(36, 4)).toBe('data');
  });

  it('writes sample rate + byte rate fields little-endian', () => {
    const pcm = new Uint8Array(0);
    const out = wrapWav(pcm, 16000, 1, 16);
    const view = new DataView(out.buffer, out.byteOffset, out.byteLength);
    // sample rate at offset 24 (4 bytes, LE)
    expect(view.getUint32(24, true)).toBe(16000);
    // byte rate = 16000 * 1 * 16 / 8 = 32000
    expect(view.getUint32(28, true)).toBe(32000);
    // block align = 1 * 16 / 8 = 2
    expect(view.getUint16(32, true)).toBe(2);
    // bits per sample
    expect(view.getUint16(34, true)).toBe(16);
    // audio format = 1 (PCM)
    expect(view.getUint16(20, true)).toBe(1);
    // channels
    expect(view.getUint16(22, true)).toBe(1);
  });

  it('writes RIFF chunk size = 36 + data size', () => {
    const pcm = new Uint8Array(64);
    const out = wrapWav(pcm, 16000, 1, 16);
    const view = new DataView(out.buffer, out.byteOffset, out.byteLength);
    expect(view.getUint32(4, true)).toBe(36 + 64);
    expect(view.getUint32(40, true)).toBe(64); // data sub-chunk size
  });

  it('copies the PCM payload verbatim starting at offset 44', () => {
    const pcm = new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8]);
    const out = wrapWav(pcm, 16000, 1, 16);
    expect(Array.from(out.subarray(44))).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
  });

  it('handles stereo + 24kHz parameters correctly', () => {
    const pcm = new Uint8Array(16);
    const out = wrapWav(pcm, 24000, 2, 16);
    const view = new DataView(out.buffer, out.byteOffset, out.byteLength);
    expect(view.getUint32(24, true)).toBe(24000);
    expect(view.getUint16(22, true)).toBe(2);
    // byte rate = 24000 * 2 * 16 / 8 = 96000
    expect(view.getUint32(28, true)).toBe(96000);
    // block align = 2 * 16 / 8 = 4
    expect(view.getUint16(32, true)).toBe(4);
  });
});

// ---------------------------------------------------------------------------
// concatChunks
// ---------------------------------------------------------------------------

describe('concatChunks', () => {
  it('returns an empty Uint8Array for an empty input', () => {
    const out = concatChunks([]);
    expect(out.byteLength).toBe(0);
  });

  it('concatenates chunks preserving byte order', () => {
    const a = new Uint8Array([1, 2, 3]);
    const b = new Uint8Array([4, 5]);
    const c = new Uint8Array([6, 7, 8, 9]);
    const out = concatChunks([a, b, c]);
    expect(Array.from(out)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9]);
  });
});

// ---------------------------------------------------------------------------
// uint8ArrayToBase64
// ---------------------------------------------------------------------------

describe('uint8ArrayToBase64', () => {
  it('encodes bytes to base64 round-trippable through Buffer', () => {
    const bytes = new Uint8Array([1, 2, 3, 4, 5]);
    const b64 = uint8ArrayToBase64(bytes);
    // Buffer is available under Node — verify the round-trip.
    const decoded = Buffer.from(b64, 'base64');
    expect(Array.from(decoded)).toEqual([1, 2, 3, 4, 5]);
  });
});

// ---------------------------------------------------------------------------
// AudioPlayer behavioural tests using injected deps
// ---------------------------------------------------------------------------

interface DepsHarness {
  deps: PlaybackDeps;
  writeCalls: { uri: string; base64Len: number }[];
  deleteCalls: string[];
  createCalls: string[];
  resolveFinished: () => void;
  rejectFinished: (err: unknown) => void;
  sound: PlayableSound;
}

function buildDeps(): DepsHarness {
  const writeCalls: { uri: string; base64Len: number }[] = [];
  const deleteCalls: string[] = [];
  const createCalls: string[] = [];
  let resolveFinished: () => void = () => {};
  let rejectFinished: (err: unknown) => void = () => {};

  const sound: PlayableSound = {
    stopAsync: jest.fn(async () => {}),
    unloadAsync: jest.fn(async () => {}),
  };

  const deps: PlaybackDeps = {
    writeFile: async (uri: string, base64: string) => {
      writeCalls.push({ uri, base64Len: base64.length });
    },
    deleteFile: async (uri: string) => {
      deleteCalls.push(uri);
    },
    createSound: async (uri: string) => {
      createCalls.push(uri);
      const finished = new Promise<void>((resolve, reject) => {
        resolveFinished = resolve;
        rejectFinished = reject;
      });
      return { sound, finished };
    },
  };

  return {
    deps,
    writeCalls,
    deleteCalls,
    createCalls,
    get resolveFinished() {
      return resolveFinished;
    },
    get rejectFinished() {
      return rejectFinished;
    },
    sound,
  };
}

describe('AudioPlayer', () => {
  it('append + flush with an empty buffer is a no-op (no crash, no writes)', async () => {
    const { deps, writeCalls, createCalls } = buildDeps();
    const player = new AudioPlayer({ tmpDir: 'file:///tmp/' }, deps);
    await player.start();
    await player.flush();
    expect(writeCalls).toHaveLength(0);
    expect(createCalls).toHaveLength(0);
  });

  it('appending below the low-water-mark does NOT trigger playback', async () => {
    const { deps, createCalls } = buildDeps();
    const player = new AudioPlayer(
      { tmpDir: 'file:///tmp/', lowWaterMarkBytes: 1024 },
      deps,
    );
    await player.start();
    await player.append(new Uint8Array(500));
    expect(createCalls).toHaveLength(0);
    expect(player.getBufferedBytes()).toBe(500);
    expect(player.getState()).toBe('buffering');
  });

  it('appending a 2KB blob with a 1KB low-water-mark triggers beginPlayback once', async () => {
    const harness = buildDeps();
    const player = new AudioPlayer(
      { tmpDir: 'file:///tmp/', lowWaterMarkBytes: 1024 },
      harness.deps,
    );
    await player.start();
    await player.append(new Uint8Array(2048));
    // playback chain dispatched; wait for createSound to be called
    await flushMicrotasks();
    expect(harness.createCalls).toHaveLength(1);
    expect(harness.writeCalls).toHaveLength(1);
    // Buffer should be drained — bytes were consumed by beginPlayback
    expect(player.getBufferedBytes()).toBe(0);
    expect(player.getState()).toBe('playing');

    // Finish playback to allow cleanup to run
    harness.resolveFinished();
    await flushMicrotasks();
  });

  it('flush() with a partial chunk below lwm still triggers playback', async () => {
    const harness = buildDeps();
    const player = new AudioPlayer(
      { tmpDir: 'file:///tmp/', lowWaterMarkBytes: 4096 },
      harness.deps,
    );
    await player.start();
    await player.append(new Uint8Array(200));
    expect(harness.createCalls).toHaveLength(0);
    await player.flush();
    await flushMicrotasks();
    expect(harness.createCalls).toHaveLength(1);

    harness.resolveFinished();
    await flushMicrotasks();
  });

  it('stop() clears the buffer, stops the sound, and cleans up the tmp file', async () => {
    const harness = buildDeps();
    const player = new AudioPlayer(
      { tmpDir: 'file:///tmp/', lowWaterMarkBytes: 100 },
      harness.deps,
    );
    await player.start();
    await player.append(new Uint8Array(512));
    await flushMicrotasks();
    expect(harness.createCalls).toHaveLength(1);

    await player.stop();
    expect(player.getBufferedBytes()).toBe(0);
    expect(player.getState()).toBe('idle');
    expect(harness.sound.stopAsync).toHaveBeenCalled();
    expect(harness.sound.unloadAsync).toHaveBeenCalled();
    expect(harness.deleteCalls.length).toBeGreaterThanOrEqual(1);

    // Subsequent append after stop should be a no-op
    await player.append(new Uint8Array(1024));
    await flushMicrotasks();
    expect(harness.createCalls).toHaveLength(1);
  });

  it('appends arriving during playback are issued after the current chunk finishes', async () => {
    const harness = buildDeps();
    const player = new AudioPlayer(
      { tmpDir: 'file:///tmp/', lowWaterMarkBytes: 100 },
      harness.deps,
    );
    await player.start();
    await player.append(new Uint8Array(200));
    await flushMicrotasks();
    expect(harness.createCalls).toHaveLength(1);

    // Append while first chunk is playing — should buffer, not start a 2nd.
    await player.append(new Uint8Array(200));
    expect(harness.createCalls).toHaveLength(1);

    // Now finish playback — the buffered chunk should kick off.
    harness.resolveFinished();
    await flushMicrotasks();
    await flushMicrotasks();
    expect(harness.createCalls.length).toBeGreaterThanOrEqual(2);

    harness.resolveFinished();
    await flushMicrotasks();
  });

  it('wraps each chunk with a fresh WAV header (different tmp URI per chunk)', async () => {
    const harness = buildDeps();
    const player = new AudioPlayer(
      { tmpDir: 'file:///tmp/', lowWaterMarkBytes: 100 },
      harness.deps,
    );
    await player.start();
    await player.append(new Uint8Array(200));
    await flushMicrotasks();
    harness.resolveFinished();
    await flushMicrotasks();
    await flushMicrotasks();
    await player.append(new Uint8Array(200));
    await flushMicrotasks();
    expect(harness.writeCalls.length).toBeGreaterThanOrEqual(2);
    expect(harness.writeCalls[0].uri).not.toBe(harness.writeCalls[1].uri);

    harness.resolveFinished();
    await flushMicrotasks();
  });
});

// Drain pending microtasks so awaited dep calls settle before assertions.
function flushMicrotasks(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}
