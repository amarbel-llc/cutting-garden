package cutting_garden_plugin_file

import (
	"net/url"
	"testing"
)

func TestPathFromURL(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "schemeless relative", input: "./foo", want: "./foo"},
		{name: "schemeless bare", input: "foo", want: "foo"},
		{name: "schemeless absolute", input: "/abs/foo", want: "/abs/foo"},
		{name: "file scheme absolute", input: "file:///abs/foo", want: "/abs/foo"},
		{name: "file scheme opaque relative", input: "file:./foo", want: "./foo"},
		{name: "file scheme opaque bare", input: "file:foo", want: "foo"},
		{name: "file scheme localhost", input: "file://localhost/abs/foo", want: "/abs/foo"},
		{name: "unsupported scheme", input: "s3://bucket/key", wantErr: true},
		{name: "file with non-localhost host", input: "file://example.com/x", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.input)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", tc.input, err)
			}
			got, err := pathFromURL(u)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("pathFromURL(%q) = %q, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("pathFromURL(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("pathFromURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
