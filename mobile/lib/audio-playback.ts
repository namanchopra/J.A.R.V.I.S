import { Audio } from 'expo-av';
import * as FileSystem from 'expo-file-system/legacy';

/**
 * Streaming audio playback for TTS chunks coming from the WebSocket.
 *
 * The daemon streams raw 16-bit signed-PCM audio at 16kHz mono. expo-av's
 * `Sound` cannot consume raw PCM directly, so we wrap each accumulated batch
 * in a minimal RIFF/WAVE header, persist it to a temporary file, and load it
 * as a `Sound`. To minimise perceived latency we start playback once the
 * buffer crosses a low-water-mark (default 1KB ≈ 32ms of audio); subsequent
 * chunks that arrive after playback has begun are appended and played as the
 * current Sound finishes.
 *
 * Usage:
 *   const player = new AudioPlayer({ sampleRate: 16000 });
 *   await player.start();
 *   // For each binary frame received from the WS:
 *   await player.append(pcmChunk);
 *   // When the daemon signals end-of-stream:
 *   await player.flush();
 *   // To interrupt (e.g. user starts talking again):
 *   await player.stop();
 */

export interface AudioPlayerOptions {
  /** PCM sample rate in Hz. Defaults to 16000 to match the daemon's TTS output. */
  sampleRate?: number;
  /** Number of channels. Daemon streams mono. */
  channels?: number;
  /** Bits per sample. Daemon streams 16-bit. */
  bitsPerSample?: number;
  /**
   * Minimum buffered bytes before playback begins. Smaller values reduce
   * onset latency but risk choppy playback if chunks arrive slowly.
   * Default 1024 bytes (~32ms at 16kHz/16-bit mono).
   */
  lowWaterMarkBytes?: number;
  /**
   * Directory to write temporary WAV files into. Defaults to the OS cache
   * directory so the system can reclaim disk space.
   */
  tmpDir?: string;
}

type RequiredOptions = Required<Omit<AudioPlayerOptions, 'tmpDir'>> & {
  tmpDir: string;
};

/**
 * Lifecycle states tracked internally:
 *   idle      — start() hasn't run, or stop() was called.
 *   buffering — append() has been called but bufferedBytes < lwm.
 *   playing   — a Sound is currently loaded and playing.
 */
type PlaybackState = 'idle' | 'buffering' | 'playing';

/** Internal seam — exposed for tests and to allow swapping the persistence layer. */
export interface PlaybackDeps {
  /** Writes a base64-encoded blob to a file URI. Defaults to expo-file-system. */
  writeFile?: (uri: string, base64: string) => Promise<void>;
  /** Deletes a file URI. Defaults to expo-file-system (idempotent). */
  deleteFile?: (uri: string) => Promise<void>;
  /**
   * Creates and plays a Sound from a file URI. Returns the Sound instance and
   * a promise that resolves when natural playback completes (didJustFinish).
   * Defaults to expo-av's Audio.Sound.createAsync.
   */
  createSound?: (uri: string) => Promise<{ sound: PlayableSound; finished: Promise<void> }>;
}

/** Minimal Sound interface used internally — matches expo-av Audio.Sound. */
export interface PlayableSound {
  stopAsync(): Promise<unknown>;
  unloadAsync(): Promise<unknown>;
}

export class AudioPlayer {
  private readonly opts: RequiredOptions;
  private readonly deps: Required<PlaybackDeps>;
  private buffer: Uint8Array[] = [];
  private bufferedBytes = 0;
  private state: PlaybackState = 'idle';
  private currentSound: PlayableSound | null = null;
  private currentFileUri: string | null = null;
  private playbackChain: Promise<void> = Promise.resolve();
  private tmpCounter = 0;
  private stopped = true;
  private endOfStream = false;

  constructor(opts?: AudioPlayerOptions, deps?: PlaybackDeps) {
    const tmpDir = opts?.tmpDir ?? FileSystem.cacheDirectory ?? '';
    this.opts = {
      sampleRate: opts?.sampleRate ?? 16000,
      channels: opts?.channels ?? 1,
      bitsPerSample: opts?.bitsPerSample ?? 16,
      lowWaterMarkBytes: opts?.lowWaterMarkBytes ?? 1024,
      tmpDir,
    };
    this.deps = {
      writeFile: deps?.writeFile ?? defaultWriteFile,
      deleteFile: deps?.deleteFile ?? defaultDeleteFile,
      createSound: deps?.createSound ?? defaultCreateSound,
    };
  }

  /**
   * Configure the device's audio session for simultaneous record+play (so the
   * Friday push-to-talk mic doesn't get blocked while the orb is speaking).
   * Safe to call multiple times.
   */
  async start(): Promise<void> {
    this.stopped = false;
    this.endOfStream = false;
    await Audio.setAudioModeAsync({
      allowsRecordingIOS: true,
      playsInSilentModeIOS: true,
      staysActiveInBackground: false,
      shouldDuckAndroid: true,
      playThroughEarpieceAndroid: false,
    });
    if (this.state === 'idle') {
      this.state = 'buffering';
    }
  }

  /** Append a binary PCM chunk to the playback buffer. */
  async append(chunk: Uint8Array): Promise<void> {
    if (this.stopped) return;
    if (chunk.byteLength === 0) return;
    this.buffer.push(chunk);
    this.bufferedBytes += chunk.byteLength;
    if (this.state !== 'playing' && this.bufferedBytes >= this.opts.lowWaterMarkBytes) {
      await this.beginPlayback();
    }
  }

  /**
   * Signal end-of-stream: flush any remaining buffered audio (even below the
   * low-water-mark) and wait for the final chunk to finish playing.
   * Returns once the queued audio has been issued to expo-av — callers can
   * `await waitForCompletion()` to block until playback finishes.
   */
  async flush(): Promise<void> {
    if (this.stopped) return;
    this.endOfStream = true;
    if (this.buffer.length > 0) {
      await this.beginPlayback();
    }
  }

  /**
   * Hard-stop playback. Cuts current audio within ~50ms (the time expo-av
   * needs to deliver stopAsync + unloadAsync) and discards any buffered
   * chunks. Used when the user re-presses push-to-talk.
   */
  async stop(): Promise<void> {
    this.stopped = true;
    this.endOfStream = false;
    this.buffer = [];
    this.bufferedBytes = 0;
    const sound = this.currentSound;
    const fileUri = this.currentFileUri;
    this.currentSound = null;
    this.currentFileUri = null;
    this.state = 'idle';
    if (sound) {
      try {
        await sound.stopAsync();
      } catch {
        // already stopped
      }
      try {
        await sound.unloadAsync();
      } catch {
        // already unloaded
      }
    }
    if (fileUri) {
      await this.deps.deleteFile(fileUri).catch(() => {
        /* best-effort cleanup */
      });
    }
  }

  /**
   * Returns a promise that resolves when all currently queued audio has
   * finished playing through the speaker.
   */
  async waitForCompletion(): Promise<void> {
    await this.playbackChain;
  }

  /** Current internal state — exposed for tests and UI introspection. */
  getState(): PlaybackState {
    return this.state;
  }

  /** Current buffered byte count — exposed for tests. */
  getBufferedBytes(): number {
    return this.bufferedBytes;
  }

  // ---- private --------------------------------------------------------------

  private async beginPlayback(): Promise<void> {
    if (this.stopped) return;
    if (this.buffer.length === 0) return;

    // Drain the current buffer into one contiguous PCM blob and wrap in WAV.
    const pcm = concatChunks(this.buffer);
    this.buffer = [];
    this.bufferedBytes = 0;
    this.state = 'playing';

    const wav = wrapWav(
      pcm,
      this.opts.sampleRate,
      this.opts.channels,
      this.opts.bitsPerSample,
    );

    // Chain this playback after any in-flight playback so chunks play in
    // arrival order without overlap.
    this.playbackChain = this.playbackChain
      .catch(() => {
        /* swallow earlier errors so the chain continues */
      })
      .then(() => this.playWav(wav));
  }

  private async playWav(wav: Uint8Array): Promise<void> {
    if (this.stopped) return;
    const fileUri = this.nextTmpUri();
    const base64 = uint8ArrayToBase64(wav);
    try {
      await this.deps.writeFile(fileUri, base64);
    } catch (err) {
      // If we can't write the tmp file, there's no way to play — surface the
      // error to the caller via waitForCompletion's rejection.
      this.state = 'idle';
      throw err;
    }

    if (this.stopped) {
      await this.deps.deleteFile(fileUri).catch(() => {});
      return;
    }

    let finishedPromise: Promise<void>;
    try {
      const created = await this.deps.createSound(fileUri);
      this.currentSound = created.sound;
      this.currentFileUri = fileUri;
      finishedPromise = created.finished;
    } catch (err) {
      await this.deps.deleteFile(fileUri).catch(() => {});
      this.state = 'idle';
      throw err;
    }

    try {
      await finishedPromise;
    } finally {
      const soundToCleanup = this.currentSound;
      const fileToCleanup = this.currentFileUri;
      this.currentSound = null;
      this.currentFileUri = null;
      if (soundToCleanup) {
        await soundToCleanup.unloadAsync().catch(() => {});
      }
      if (fileToCleanup) {
        await this.deps.deleteFile(fileToCleanup).catch(() => {});
      }
    }

    if (this.buffer.length > 0 && !this.stopped) {
      // More chunks have arrived during the previous playback — issue them.
      await this.beginPlayback();
    } else {
      this.state = this.endOfStream ? 'idle' : 'buffering';
    }
  }

  private nextTmpUri(): string {
    this.tmpCounter += 1;
    const base = this.opts.tmpDir.endsWith('/')
      ? this.opts.tmpDir
      : `${this.opts.tmpDir}/`;
    return `${base}jarvis-tts-${Date.now()}-${this.tmpCounter}.wav`;
  }
}

// ---------------------------------------------------------------------------
// Pure helpers — exported for unit-testing
// ---------------------------------------------------------------------------

/**
 * Wrap raw signed 16-bit PCM in a minimal RIFF/WAVE header so expo-av can
 * play it. Returns a fresh Uint8Array of length `44 + pcm.byteLength`.
 *
 * Header layout (44 bytes, all multi-byte fields little-endian):
 *   0..3   "RIFF"
 *   4..7   chunk size (36 + data size)
 *   8..11  "WAVE"
 *   12..15 "fmt "
 *   16..19 sub-chunk size (16 for PCM)
 *   20..21 audio format (1 = PCM)
 *   22..23 channels
 *   24..27 sample rate
 *   28..31 byte rate (sampleRate * channels * bitsPerSample / 8)
 *   32..33 block align (channels * bitsPerSample / 8)
 *   34..35 bits per sample
 *   36..39 "data"
 *   40..43 data size
 */
export function wrapWav(
  pcm: Uint8Array,
  sampleRate: number,
  channels: number,
  bitsPerSample: number,
): Uint8Array {
  const dataSize = pcm.byteLength;
  const byteRate = (sampleRate * channels * bitsPerSample) / 8;
  const blockAlign = (channels * bitsPerSample) / 8;

  const out = new Uint8Array(44 + dataSize);
  const view = new DataView(out.buffer, out.byteOffset, 44);

  writeAscii(view, 0, 'RIFF');
  view.setUint32(4, 36 + dataSize, true);
  writeAscii(view, 8, 'WAVE');
  writeAscii(view, 12, 'fmt ');
  view.setUint32(16, 16, true); // PCM sub-chunk size
  view.setUint16(20, 1, true); // audio format = PCM
  view.setUint16(22, channels, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, byteRate, true);
  view.setUint16(32, blockAlign, true);
  view.setUint16(34, bitsPerSample, true);
  writeAscii(view, 36, 'data');
  view.setUint32(40, dataSize, true);

  out.set(pcm, 44);
  return out;
}

/** Concatenate an array of Uint8Arrays into a single Uint8Array. */
export function concatChunks(chunks: Uint8Array[]): Uint8Array {
  let total = 0;
  for (const c of chunks) total += c.byteLength;
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.byteLength;
  }
  return out;
}

/** Encode a Uint8Array as base64 — works in both RN and Node test envs. */
export function uint8ArrayToBase64(bytes: Uint8Array): string {
  // Prefer Node's Buffer when available (test env); fall back to the
  // browser/RN-friendly btoa path otherwise.
  const maybeBuffer = (globalThis as { Buffer?: { from(b: Uint8Array): { toString(enc: string): string } } }).Buffer;
  if (maybeBuffer && typeof maybeBuffer.from === 'function') {
    return maybeBuffer.from(bytes).toString('base64');
  }
  let binary = '';
  const chunkSize = 0x8000;
  for (let i = 0; i < bytes.length; i += chunkSize) {
    const slice = bytes.subarray(i, i + chunkSize);
    binary += String.fromCharCode.apply(null, Array.from(slice) as number[]);
  }
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const enc = (globalThis as any).btoa;
  if (typeof enc === 'function') return enc(binary);
  throw new Error('audio-playback: no base64 encoder available in this environment');
}

function writeAscii(view: DataView, offset: number, s: string): void {
  for (let i = 0; i < s.length; i++) {
    view.setUint8(offset + i, s.charCodeAt(i));
  }
}

// ---------------------------------------------------------------------------
// Default IO implementations — thin wrappers over expo-file-system & expo-av.
// ---------------------------------------------------------------------------

const defaultWriteFile = async (uri: string, base64: string): Promise<void> => {
  await FileSystem.writeAsStringAsync(uri, base64, {
    encoding: FileSystem.EncodingType.Base64,
  });
};

const defaultDeleteFile = async (uri: string): Promise<void> => {
  await FileSystem.deleteAsync(uri, { idempotent: true });
};

const defaultCreateSound = async (
  uri: string,
): Promise<{ sound: PlayableSound; finished: Promise<void> }> => {
  let resolveFinished: () => void = () => {};
  let rejectFinished: (err: unknown) => void = () => {};
  const finished = new Promise<void>((resolve, reject) => {
    resolveFinished = resolve;
    rejectFinished = reject;
  });

  try {
    const { sound } = await Audio.Sound.createAsync(
      { uri },
      { shouldPlay: true },
      (status) => {
        if (!status.isLoaded) {
          if (status.error) rejectFinished(new Error(status.error));
          return;
        }
        if (status.didJustFinish) resolveFinished();
      },
    );
    return { sound, finished };
  } catch (err) {
    rejectFinished(err);
    throw err;
  }
};
