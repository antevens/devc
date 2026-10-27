package main

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// debounce coalesces bursts of changes, such as a git pull, into one sync.
const debounce = 300 * time.Millisecond

// watcher copies shared trees into the container when they change on the
// host. Changes are copied one way only, the container's copies are not
// written back.
type watcher struct {
	fs   *fsnotify.Watcher
	c    *container
	home string
	log  *log.Logger
	// dirs maps each watched directory to the tree it belongs to.
	dirs map[string]tree
	// roots maps the real path of each tree to the tree.
	roots map[string]tree

	mu      sync.Mutex
	pending map[string]*time.Timer
	syncing sync.Mutex
	done    chan struct{}
}

// watch starts syncing changes to the shared trees into the container. The
// returned function stops it. Errors are logged to devc.log in the state
// directory, as the session owns the terminal.
func watch(c *container, home string) (func(), error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	logFile, err := openLog(home)
	if err != nil {
		fsw.Close()
		return nil, err
	}
	w := &watcher{
		fs: fsw, c: c, home: home,
		log:     log.New(logFile, "", log.LstdFlags),
		dirs:    map[string]tree{},
		roots:   map[string]tree{},
		pending: map[string]*time.Timer{},
		done:    make(chan struct{}),
	}
	for _, t := range sharedTrees {
		root, err := filepath.EvalSymlinks(filepath.Join(home, t.name))
		if err != nil {
			continue
		}
		w.roots[root] = t
		w.add(root, t)
	}

	go w.loop()
	return func() {
		fsw.Close()
		<-w.done
		w.mu.Lock()
		for _, timer := range w.pending {
			timer.Stop()
		}
		w.mu.Unlock()
		w.syncing.Lock()
		logFile.Close()
	}, nil
}

func openLog(home string) (*os.File, error) {
	dir := stateDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "devc.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// add watches dir and its subdirectories for changes to t. Symlinks are
// followed by also watching their targets, so dotfiles kept in another
// directory are picked up.
func (w *watcher) add(dir string, t tree) {
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
			w.log.Printf("watch %s: %v", path, err)
			return filepath.SkipDir
		}
		w.dirs[path] = t
		return nil
	})
}

// addTarget watches the target of the symlink at path: a directory tree, or
// the directory holding a file, as editors replace files rather than write
// them in place.
func (w *watcher) addTarget(path string, t tree) {
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
		w.log.Printf("watch %s: %v", dir, err)
		return
	}
	w.dirs[dir] = t
}

// skipped reports whether path is a top-level entry of a tree that is not
// shared.
func (w *watcher) skipped(path string) bool {
	t, ok := w.roots[filepath.Dir(path)]
	return ok && t.skip[filepath.Base(path)]
}

func (w *watcher) loop() {
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
			w.log.Printf("watch: %v", err)
		}
	}
}

// schedule syncs t once no change has been seen for the debounce interval.
func (w *watcher) schedule(t tree) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if timer, ok := w.pending[t.name]; ok {
		timer.Reset(debounce)
		return
	}
	w.pending[t.name] = time.AfterFunc(debounce, func() {
		w.mu.Lock()
		delete(w.pending, t.name)
		w.mu.Unlock()

		w.syncing.Lock()
		defer w.syncing.Unlock()
		if err := w.c.syncHome(w.home, []tree{t}, false); err != nil {
			w.log.Printf("sync %s: %v", t.name, err)
			return
		}
		if t.name == ".claude" {
			if err := w.c.installClaude(w.home); err != nil {
				w.log.Printf("install claude settings: %v", err)
			}
		}
	})
}
