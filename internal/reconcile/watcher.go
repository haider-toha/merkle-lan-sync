package reconcile

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/haider-toha/merkle-sync/internal/pathnorm"
)

// watcherKernelBuffer is the per-watch ReadDirectoryChangesW buffer requested on
// Windows (GR-9, XP-5): larger than the 64 KiB default to reduce silent overflow
// drops under load. It is ignored on non-Windows backends. The exact value is a
// Phase-6 real-Windows tuning item.
const watcherKernelBuffer = 512 << 10

// fsWatcher is the advisory change-hint source the engine consumes (SR-11 — the
// watcher is a HINT, the periodic rescan is the truth). It is an interface so the
// engine never hard-depends on a live OS watcher and tests can drive synthetic
// events; cmd/msync wires the fsnotify-backed implementation.
type fsWatcher interface {
	// Changes emits canonical keys whose on-disk file may have changed.
	Changes() <-chan string
	// Overflow signals the OS dropped events ⇒ the engine must do a full rescan.
	Overflow() <-chan struct{}
	Close() error
}

// fsnotifyWatcher is the production fsWatcher: per-directory fsnotify watches over the
// sync root, converting OS event paths to canonical keys and surfacing overflow as a
// rescan trigger. Non-recursive watches are reconciled by re-walking on directory
// creates (GR-9). Closed via Close (idempotent) or by cancelling the context.
type fsnotifyWatcher struct {
	absRoot string
	w       *fsnotify.Watcher

	changes  chan string
	overflow chan struct{}

	closeOnce sync.Once
	wg        sync.WaitGroup
}

// newFSNotifyWatcher opens a buffered fsnotify watcher, adds a watch per directory
// under absRoot, and starts draining events until ctx is cancelled or Close is called.
func newFSNotifyWatcher(ctx context.Context, absRoot string) (*fsnotifyWatcher, error) {
	w, err := fsnotify.NewBufferedWatcher(4096)
	if err != nil {
		return nil, err
	}
	fw := &fsnotifyWatcher{
		absRoot:  absRoot,
		w:        w,
		changes:  make(chan string, chanDepth),
		overflow: make(chan struct{}, 1),
	}
	fw.addTree(absRoot)
	fw.wg.Add(1)
	go fw.run(ctx)
	return fw, nil
}

// addTree adds a watch for root and every directory beneath it (fsnotify is
// non-recursive — GR-9). Errors are best-effort: a watch we fail to add is covered by
// the periodic rescan (SR-11).
func (fw *fsnotifyWatcher) addTree(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = fw.w.AddWith(p, fsnotify.WithBufferSize(watcherKernelBuffer))
		}
		return nil
	})
}

func (fw *fsnotifyWatcher) run(ctx context.Context) {
	defer fw.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-fw.w.Events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Create) {
				if info, serr := os.Stat(ev.Name); serr == nil && info.IsDir() {
					fw.addTree(ev.Name) // a new subdir must be watched (non-recursive)
				}
			}
			key, kerr := pathnorm.FromOSPath(fw.absRoot, ev.Name, pathnorm.HostTarget())
			if kerr != nil || key == "" {
				continue
			}
			select {
			case fw.changes <- key:
			case <-ctx.Done():
				return
			}
		case err, ok := <-fw.w.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				select {
				case fw.overflow <- struct{}{}:
				default: // a rescan is already pending
				}
			}
		}
	}
}

func (fw *fsnotifyWatcher) Changes() <-chan string    { return fw.changes }
func (fw *fsnotifyWatcher) Overflow() <-chan struct{} { return fw.overflow }

// Close stops the watcher and waits for its goroutine. Idempotent. Closing the
// underlying watcher closes its Events/Errors channels, which ends run; if run is
// parked sending a change it also observes the caller's cancelled context.
func (fw *fsnotifyWatcher) Close() error {
	var err error
	fw.closeOnce.Do(func() { err = fw.w.Close() })
	fw.wg.Wait()
	return err
}
