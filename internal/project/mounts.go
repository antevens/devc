package project

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// mount is a devcontainer.json mounts entry.
type mount struct {
	Type   string
	Source string
}

// UnmarshalJSON reads a docker --mount string or an object with type and
// source.
func (m *mount) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*m, err = parseMount(s)
		return err
	}
	var o struct {
		Type   string `json:"type"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(data, &o); err != nil {
		return fmt.Errorf("mount %s: %w", data, err)
	}
	*m = mount{Type: o.Type, Source: o.Source}
	return nil
}

// parseMount parses a docker --mount value: comma-separated key=value
// fields, CSV quoted, with type defaulting to volume.
func parseMount(s string) (mount, error) {
	fields, err := csv.NewReader(strings.NewReader(s)).Read()
	if err != nil {
		return mount{}, fmt.Errorf("mount %q: %w", s, err)
	}
	m := mount{Type: "volume"}
	for _, field := range fields {
		key, value, _ := strings.Cut(field, "=")
		switch strings.ToLower(key) {
		case "type":
			m.Type = value
		case "source", "src":
			m.Source = value
		}
	}
	return m, nil
}

// createMountSources creates missing bind mount sources inside home as
// directories, as docker run -v does, and reports each one to w.
func createMountSources(mounts []mount, home string, w io.Writer) error {
	for _, m := range mounts {
		if m.Type != "bind" || !within(home, m.Source) {
			continue
		}
		if _, err := os.Lstat(m.Source); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err := os.MkdirAll(m.Source, 0o700); err != nil {
			return fmt.Errorf("create mount source: %w", err)
		}
		fmt.Fprintf(w, "devc: created missing mount source %s\n", m.Source)
	}
	return nil
}

// within reports whether path is dir or inside it.
func within(dir, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(rel)
}
