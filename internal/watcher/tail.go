package watcher

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

const (
	// ringBufferSize is the maximum number of lines retained in the ring buffer.
	ringBufferSize = 1000

	// chanBufferSize is the capacity of the output channel so the tailer
	// goroutine does not block on slow consumers.
	chanBufferSize = 100

	// fileRemovedSentinel is emitted on the channel when the watched file is
	// deleted or renamed away.
	fileRemovedSentinel = "[FILE REMOVED]"
)

// Tailer watches a single file for appended content and sends each new line
// over a Go channel. It handles files that do not yet exist (watches the
// parent directory), truncation (seeks back to the beginning), and deletion.
type Tailer struct {
	mu   sync.Mutex
	ring []string // ring buffer of last ringBufferSize lines
	head int      // next write index in ring
	full bool     // true once we have wrapped around at least once
}

// NewTailer returns a ready-to-use Tailer.
func NewTailer() *Tailer {
	return &Tailer{
		ring: make([]string, ringBufferSize),
	}
}

// LastLines returns up to n of the most recently observed lines. If fewer than
// n lines have been seen, all available lines are returned. The returned slice
// is in chronological order (oldest first).
func (t *Tailer) LastLines(n int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	total := t.bufferedCount()
	if n > total {
		n = total
	}
	if n <= 0 {
		return nil
	}

	out := make([]string, n)
	// start is the index in the ring of the oldest line we want.
	start := (t.head - n + ringBufferSize) % ringBufferSize
	for i := 0; i < n; i++ {
		out[i] = t.ring[(start+i)%ringBufferSize]
	}
	return out
}

// bufferedCount returns how many lines are currently stored. Must be called
// with t.mu held.
func (t *Tailer) bufferedCount() int {
	if t.full {
		return ringBufferSize
	}
	return t.head
}

// pushLine adds a line to the ring buffer. Thread-safe.
func (t *Tailer) pushLine(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ring[t.head] = line
	t.head = (t.head + 1) % ringBufferSize
	if t.head == 0 {
		t.full = true
	}
}

// Start begins tailing the file at path. New lines are sent on the returned
// channel. The caller must cancel ctx to stop tailing; the channel is closed
// once the goroutine exits.
//
// If the file does not exist yet, Start watches the parent directory and waits
// for the file to be created before tailing.
func (t *Tailer) Start(ctx context.Context, path string) (<-chan string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving absolute path: %w", err)
	}

	parentDir := filepath.Dir(absPath)

	// Verify that the parent directory exists — we always need it for
	// watching, regardless of whether the file itself exists yet.
	if _, err := os.Stat(parentDir); err != nil {
		return nil, fmt.Errorf("parent directory: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating fsnotify watcher: %w", err)
	}

	ch := make(chan string, chanBufferSize)

	go t.run(ctx, watcher, absPath, parentDir, ch)

	return ch, nil
}

// run is the main goroutine that drives the tailer. It is responsible for
// closing both the watcher and the output channel on every exit path.
func (t *Tailer) run(ctx context.Context, watcher *fsnotify.Watcher, absPath, parentDir string, ch chan<- string) {
	defer close(ch)
	defer watcher.Close()

	// Determine whether the file already exists so we know if we need to
	// watch the parent directory first.
	fileExists := false
	if _, err := os.Stat(absPath); err == nil {
		fileExists = true
	}

	if !fileExists {
		// Watch the parent directory for file creation.
		if err := watcher.Add(parentDir); err != nil {
			return
		}
		if !t.waitForCreation(ctx, watcher, absPath) {
			return
		}
		// The file now exists. Remove the directory watch; we will watch
		// the directory again below for the tailing phase anyway.
		_ = watcher.Remove(parentDir)
	}

	// Watch the parent directory (not the file directly) as recommended by
	// fsnotify — editors often atomically replace files, which would remove
	// the watch on the file itself.
	if err := watcher.Add(parentDir); err != nil {
		return
	}

	t.tailFile(ctx, watcher, absPath, ch)
}

// waitForCreation blocks until the target file is created, the context is
// cancelled, or an unrecoverable error occurs. Returns true if the file was
// created successfully.
func (t *Tailer) waitForCreation(ctx context.Context, watcher *fsnotify.Watcher, absPath string) bool {
	for {
		select {
		case <-ctx.Done():
			return false

		case ev, ok := <-watcher.Events:
			if !ok {
				return false
			}
			if ev.Has(fsnotify.Create) && filepath.Clean(ev.Name) == absPath {
				return true
			}

		case _, ok := <-watcher.Errors:
			if !ok {
				return false
			}
			// Swallow errors and keep waiting.
		}
	}
}

// tailFile reads new lines from the file whenever fsnotify reports a write.
// It detects truncation (file shrinks) and deletion / rename, and handles
// context cancellation.
func (t *Tailer) tailFile(ctx context.Context, watcher *fsnotify.Watcher, absPath string, ch chan<- string) {
	var offset int64

	// Open the file and seek to the end so we only emit new content.
	f, err := os.Open(absPath)
	if err != nil {
		return
	}
	defer f.Close()

	offset, err = f.Seek(0, io.SeekEnd)
	if err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}

			// We watch the parent directory, so filter for our file.
			if filepath.Clean(ev.Name) != absPath {
				continue
			}

			if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				t.emit(ch, ctx, fileRemovedSentinel)
				return
			}

			if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Chmod) {
				offset = t.readNewLines(ctx, f, offset, ch)
			}

		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
			// Swallow and continue.
		}
	}
}

// readNewLines reads any bytes appended to f since offset, splits them into
// lines, and sends each line on ch. If the file has been truncated (size <
// offset) it seeks to the beginning. Returns the new offset.
func (t *Tailer) readNewLines(ctx context.Context, f *os.File, offset int64, ch chan<- string) int64 {
	info, err := f.Stat()
	if err != nil {
		return offset
	}

	currentSize := info.Size()

	// Detect truncation.
	if currentSize < offset {
		offset = 0
	}

	if currentSize == offset {
		return offset
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}

	scanner := bufio.NewScanner(io.LimitReader(f, currentSize-offset))
	for scanner.Scan() {
		line := scanner.Text()
		t.pushLine(line)
		t.emit(ch, ctx, line)
	}

	// Update offset to current position. Use Seek to get the real file
	// position rather than trusting arithmetic, in case the scanner consumed
	// a partial trailing line or the file changed underneath us.
	newOffset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return offset
	}
	return newOffset
}

// emit sends a line on ch unless the context has been cancelled.
func (t *Tailer) emit(ch chan<- string, ctx context.Context, line string) {
	select {
	case ch <- line:
	case <-ctx.Done():
	}
}
