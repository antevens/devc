package sysbox

import "testing"

func TestHasRuntime(t *testing.T) {
	tests := []struct {
		json string
		want bool
	}{
		{`{"io.containerd.runc.v2":{"path":"runc"},"runc":{"path":"runc"},"sysbox-runc":{"path":"/usr/bin/sysbox-runc"}}`, true},
		{`{"io.containerd.runc.v2":{"path":"runc"},"runc":{"path":"runc"}}`, false},
		{`{}`, false},
	}
	for _, tt := range tests {
		got, err := hasRuntime([]byte(tt.json))
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.json, got, tt.want)
		}
	}
	if _, err := hasRuntime([]byte("not json")); err == nil {
		t.Error("invalid JSON: want an error")
	}
}
