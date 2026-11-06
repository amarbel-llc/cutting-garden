package tailwriter

import (
	"bytes"
	"testing"
)

func TestWriter_KeepsOnlyTheLastCapBytes(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		want   string
	}{
		{"under the cap keeps everything", []string{"ab", "cd"}, "abcd"},
		{"a write over the cap keeps its tail", []string{"abcdefgh"}, "efgh"},
		{"a write at the cap replaces the buffer", []string{"xy", "abcd"}, "abcd"},
		{"an overflowing write drops the oldest bytes", []string{"ab", "cde"}, "bcde"},
		{"small writes slide the window", []string{"ab", "cd", "ef"}, "cdef"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := New(&buf, 4)
			for _, p := range c.writes {
				n, err := w.Write([]byte(p))
				if err != nil || n != len(p) {
					t.Fatalf("Write(%q) = %d, %v; want %d, nil", p, n, err, len(p))
				}
			}
			if got := buf.String(); got != c.want {
				t.Errorf("tail = %q, want %q", got, c.want)
			}
		})
	}
}
