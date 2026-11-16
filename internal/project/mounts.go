package project

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
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

// checkMountSources returns an error naming the bind mount sources that do
// not exist on the host. Docker refuses to create a container with a missing
// bind source, and devc does not create them.
func checkMountSources(mounts []mount) error {
	var missing []string
	for _, m := range mounts {
		if m.Type != "bind" || !filepath.IsAbs(m.Source) {
			continue
		}
		if _, err := os.Stat(m.Source); errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, m.Source)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("bind mount sources missing on the host: %s; create them or remove the mounts from the devcontainer config",
		strings.Join(missing, ", "))
}
