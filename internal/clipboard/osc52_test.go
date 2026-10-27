package clipboard

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// filter writes in to a Filter, whole or one byte at a time, and returns
// the output and the copies.
func filter(t *testing.T, in string, bytewise bool) (string, []string) {
	t.Helper()
	var out bytes.Buffer
	var copies []string
	f := NewFilter(&out, func(data []byte) { copies = append(copies, string(data)) })
	chunks := []string{in}
	if bytewise {
		chunks = strings.Split(in, "")
	}
	for _, c := range chunks {
		if n, err := f.Write([]byte(c)); err != nil || n != len(c) {
			t.Fatalf("Write(%q) = %d, %v", c, n, err)
		}
	}
	if err := f.Flush(); err != nil {
		t.Fatal(err)
	}
	return out.String(), copies
}

func TestFilter(t *testing.T) {
	tests := []struct {
		name, in, out string
		copies        []string
	}{
		{"plain", "hello\r\n", "hello\r\n", nil},
		{"bel", "a\x1b]52;c;aGk=\ab", "ab", []string{"hi"}},
		{"st", "a\x1b]52;c;aGk=\x1b\\b", "ab", []string{"hi"}},
		{"unpadded", "\x1b]52;c;aGk\a", "", []string{"hi"}},
		{"primary", "\x1b]52;p;aGk=\a", "", []string{"hi"}},
		{"no selection", "\x1b]52;;aGk=\a", "", []string{"hi"}},
		{"two", "\x1b]52;c;YQ==\a\x1b]52;c;Yg==\a", "", []string{"a", "b"}},
		{"read", "\x1b]52;c;?\a", "", nil},
		{"invalid", "\x1b]52;c;*\a", "", nil},
		{"empty", "\x1b]52;c;\a", "", nil},
		{"other osc", "\x1b]0;title\a\x1b]5;x\a", "\x1b]0;title\a\x1b]5;x\a", nil},
		{"csi", "\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m", nil},
		{"unterminated", "\x1b]52;c;aGk=\x1b[0m", "\x1b[0m", nil},
		{"partial introducer", "x\x1b]5", "x\x1b]5", nil},
		{"esc esc", "\x1b\x1b]52;c;aGk=\a", "\x1b", []string{"hi"}},
	}
	for _, tt := range tests {
		for _, bytewise := range []bool{false, true} {
			out, copies := filter(t, tt.in, bytewise)
			if out != tt.out || !reflect.DeepEqual(copies, tt.copies) {
				t.Errorf("%s (bytewise %v): got %q %q, want %q %q", tt.name, bytewise, out, copies, tt.out, tt.copies)
			}
		}
	}
}

func TestFilterTooLong(t *testing.T) {
	out, copies := filter(t, "\x1b]52;c;"+strings.Repeat("A", maxPayload)+"\aend", false)
	if out != "end" || copies != nil {
		t.Errorf("got %q, %d copies", out, len(copies))
	}
}

func TestFilterHolds(t *testing.T) {
	var out bytes.Buffer
	f := NewFilter(&out, func([]byte) {})
	if _, err := f.Write([]byte("x\x1b]5")); err != nil {
		t.Fatal(err)
	}
	if out.String() != "x" {
		t.Errorf("before the next write: %q", out.String())
	}
	if _, err := f.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	if out.String() != "x\x1b]5y" {
		t.Errorf("after the next write: %q", out.String())
	}
}
