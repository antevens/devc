package project

import (
	"reflect"
	"testing"
)

func TestParentsWithin(t *testing.T) {
	tests := []struct {
		dir, target string
		want        []string
	}{
		{"/home/vscode", "/home/vscode/.m2/repository", []string{"/home/vscode/.m2"}},
		{"/home/vscode/", "/home/vscode/.a/b/c", []string{"/home/vscode/.a", "/home/vscode/.a/b"}},
		{"/home/vscode", "/home/vscode/.claude", nil},
		{"/home/vscode", "/commandhistory", nil},
		{"/home/vscode", "/opt/x/y", nil},
		{"/home/vscode", "/home/vscodex/.a/b", nil},
	}
	for _, tt := range tests {
		if got := parentsWithin(tt.dir, tt.target); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parentsWithin(%s, %s) = %v, want %v", tt.dir, tt.target, got, tt.want)
		}
	}
}
