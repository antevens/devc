package share

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// debounce is the time without changes before a tree is reported.
const debounce = 300 * time.Millisecond

// Watcher calls a function for each shared tree that changes on the host.
type Watcher struct {
	fs       *fsnotify.Watcher
	log      *slog.Logger
	onChange func(Tree)
	// dirs maps each watched directory to the tree it belongs to.
	dirs map[string]Tree
	// roots maps the real path of each tree to the tree.
	roots map[string]Tree

	mu      sync.Mutex
	pending map[string]*time.Timer
	running sync.Mutex
	done    chan struct{}
}

// Watch starts calling onChange for each of Trees that changes, once no
// further change has been seen for debounce. Calls are serialised. Errors
// are logged.
func Watch(home string, log *slog.Logger, onChange func(Tree)) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		fs:       fsw,
		log:      log,
		onChange: onChange,
		dirs:     map[string]Tree{},
		roots:    map[string]Tree{},
		pending:  map[string]*time.Timer{},
		done:     make(chan struct{}),
	}
	for _, t := range Trees {
		root, err := filepath.EvalSymlinks(filepath.Join(home, t.Name))
		if err != nil {
			continue
		}
		w.roots[root] = t
		w.add(root, t)
	}
	go w.loop()
	return w, nil
}

// Close stops watching and waits for a running call of onChange to return.
func (w *Watcher) Close() error {
	err := w.fs.Close()
	<-w.done
	w.mu.Lock()
	for _, timer := range w.pending {
		timer.Stop()
	}
	w.mu.Unlock()
	w.running.Lock()
	return err
}

// add watches dir, its subdirectories and the targets of symlinks in them
// for changes to t.
func (w *Watcher) add(dir string, t Tree) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if w.skipped(path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			w.addTarget(path, t)
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if _, ok := w.dirs[path]; ok {
			return filepath.SkipDir
		}
		if err := w.fs.Add(path); err != nil {
			w.log.Warn("watch", "path", path, "err", err)
			return filepath.SkipDir
		}
		w.dirs[path] = t
		return nil
	})
}

// addTarget watches the target of the symlink at path: a directory tree, or
// the directory holding a file.
func (w *Watcher) addTarget(path string, t Tree) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return
	}
	info, err := os.Stat(target)
	if err != nil {
		return
	}
	if info.IsDir() {
		w.add(target, t)
		return
	}
	dir := filepath.Dir(target)
	if _, ok := w.dirs[dir]; ok {
		return
	}
	if err := w.fs.Add(dir); err != nil {
		w.log.Warn("watch", "path", dir, "err", err)
		return
	}
	w.dirs[dir] = t
}

// skipped reports whether path is a top-level entry of a tree that is not
// shared.
func (w *Watcher) skipped(path string) bool {
	t, ok := w.roots[filepath.Dir(path)]
	return ok && t.Skip[filepath.Base(path)]
}

func (w *Watcher) loop() {
	defer close(w.done)
	for {
		select {
		case event, ok := <-w.fs.Events:
			if !ok {
				return
			}
			t, ok := w.dirs[filepath.Dir(event.Name)]
			if !ok || w.skipped(event.Name) {
				continue
			}
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					w.add(event.Name, t)
				}
			}
			w.schedule(t)
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			w.log.Warn("watch", "err", err)
		}
	}
}

// schedule calls onChange for t once no change has been seen for the
// debounce interval.
func (w *Watcher) schedule(t Tree) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if timer, ok := w.pending[t.Name]; ok {
		timer.Reset(debounce)
		return
	}
	w.pending[t.Name] = time.AfterFunc(debounce, func() {
		w.mu.Lock()
		delete(w.pending, t.Name)
		w.mu.Unlock()

		w.running.Lock()
		defer w.running.Unlock()
		w.onChange(t)
	})
}
