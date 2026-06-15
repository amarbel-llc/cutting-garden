package cutting_garden_plugin_file

import "testing"

func TestPathConfinedTo(t *testing.T) {
	cases := []struct {
		name         string
		materialized string
		dest         string
		want         bool
	}{
		{name: "equal absolute", materialized: "/foo/bar", dest: "/foo/bar", want: true},
		{name: "child absolute", materialized: "/foo/bar/baz", dest: "/foo/bar", want: true},
		{name: "sibling absolute", materialized: "/foo/baz", dest: "/foo/bar", want: false},
		{name: "parent absolute", materialized: "/foo", dest: "/foo/bar", want: false},
		{name: "escape outside", materialized: "/etc/passwd", dest: "/home/u/dest", want: false},
		{name: "prefix not child", materialized: "/foo/barbaz", dest: "/foo/bar", want: false},

		{name: "dot dest equal", materialized: ".", dest: ".", want: true},
		{name: "dot dest child", materialized: "file.pdf", dest: ".", want: true},
		{name: "dot dest nested", materialized: "sub/file.pdf", dest: ".", want: true},
		{name: "dot dest escape", materialized: "../foo", dest: ".", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pathConfinedTo(tc.materialized, tc.dest)
			if got != tc.want {
				t.Errorf("pathConfinedTo(%q, %q) = %v, want %v",
					tc.materialized, tc.dest, got, tc.want)
			}
		})
	}
}
