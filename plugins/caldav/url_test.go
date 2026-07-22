package caldav

import (
	"net/url"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestBaseURLFromArg(t *testing.T) {
	cases := []struct {
		name    string
		arg     string
		want    string
		wantErr bool
	}{
		{name: "hierarchical maps to https", arg: "caldav://host.example/dav/", want: "https://host.example/dav/"},
		{name: "hierarchical with port", arg: "caldav://host.example:8443/dav/", want: "https://host.example:8443/dav/"},
		{name: "hierarchical with userinfo retained", arg: "caldav://u:p@host/dav/", want: "https://u:p@host/dav/"},
		{name: "opaque http passthrough", arg: "caldav:http://localhost:5232/dav/", want: "http://localhost:5232/dav/"},
		{name: "opaque https passthrough", arg: "caldav:https://host/dav/", want: "https://host/dav/"},
		{name: "opaque non-http rejected", arg: "caldav:ftp://host/x", wantErr: true},
		{name: "empty host rejected", arg: "caldav:", wantErr: true},
		{name: "wrong scheme rejected", arg: "file:///x", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := baseURLFromArg(mustParseURL(t, tc.arg))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("baseURLFromArg(%q) = %q, want error", tc.arg, got)
				}
				// Every rejection here is a malformed CALLER argument and
				// must classify as a bad request, so the wire reports
				// -32602 rather than "plugin failed" (cutting-garden#187).
				if !errors.Is400BadRequest(err) {
					t.Errorf("baseURLFromArg(%q) error must classify as a"+
						" caller fault: %v", tc.arg, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("baseURLFromArg(%q): %v", tc.arg, err)
			}
			if got != tc.want {
				t.Errorf("baseURLFromArg(%q) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}

func TestConnectionFromArg_UserinfoWins(t *testing.T) {
	t.Setenv(envUsername, "envuser")
	t.Setenv(envPassword, "envpass")

	base, user, pass, err := connectionFromArg(mustParseURL(t, "caldav://urluser:urlpass@host/dav/"))
	if err != nil {
		t.Fatalf("connectionFromArg: %v", err)
	}
	if base != "https://host/dav/" {
		t.Errorf("base = %q, want stripped of userinfo", base)
	}
	if user != "urluser" || pass != "urlpass" {
		t.Errorf("creds = %q/%q, want urluser/urlpass", user, pass)
	}
}

func TestConnectionFromArg_EnvFallback(t *testing.T) {
	t.Setenv(envUsername, "envuser")
	t.Setenv(envPassword, "envpass")

	_, user, pass, err := connectionFromArg(mustParseURL(t, "caldav://host/dav/"))
	if err != nil {
		t.Fatalf("connectionFromArg: %v", err)
	}
	if user != "envuser" || pass != "envpass" {
		t.Errorf("creds = %q/%q, want envuser/envpass", user, pass)
	}
}

func TestOriginOf(t *testing.T) {
	cases := map[string]string{
		"https://host/dav/":       "https://host",
		"https://host:8443/dav/x": "https://host:8443",
		"http://127.0.0.1:5232/":  "http://127.0.0.1:5232",
	}
	for in, want := range cases {
		got, ok := originOf(in)
		if !ok || got != want {
			t.Errorf("originOf(%q) = %q,%v want %q,true", in, got, ok, want)
		}
	}
}

func TestServerPath(t *testing.T) {
	cases := map[string]string{
		"https://host/dav/cal/a.ics":      "dav/cal/a.ics",
		"http://h:5232/u/cal/abc%40d.ics": "u/cal/abc%40d.ics",
		"https://host/":                   "",
	}
	for in, want := range cases {
		if got := serverPath(in); got != want {
			t.Errorf("serverPath(%q) = %q, want %q", in, got, want)
		}
	}
}
