package assets

import (
	"bytes"
	"testing"
)

// The scripts refer to the container paths literally, so they can be linted
// as they are. Check they agree with the constants.
func TestScriptPaths(t *testing.T) {
	tests := []struct {
		name   string
		script []byte
		paths  []string
	}{
		{"profile", Profile, []string{EnvPath, ClaudeSettings, ClaudePromptPath}},
		{"launcher", Launcher, []string{EnvPath}},
		{"credential helper", CredentialHelper, []string{TokenDir + "/"}},
	}
	for _, tt := range tests {
		for _, path := range tt.paths {
			if !bytes.Contains(tt.script, []byte(path)) {
				t.Errorf("%s does not refer to %s", tt.name, path)
			}
		}
	}
}

func TestEmbedded(t *testing.T) {
	for name, data := range map[string][]byte{
		"prompt": ClaudePrompt, "log hook": LogHook, "launcher": Launcher,
		"credential helper": CredentialHelper, "profile": Profile,
	} {
		if len(data) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}
