package jira

import (
	"net/url"
	"testing"
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
		{name: "hierarchical maps to https", arg: "jira://acme.atlassian.net/PROJ", want: "https://acme.atlassian.net/PROJ"},
		{name: "hierarchical bare host", arg: "jira://acme.atlassian.net", want: "https://acme.atlassian.net"},
		{name: "hierarchical with userinfo retained", arg: "jira://u:t@host/PROJ", want: "https://u:t@host/PROJ"},
		{name: "opaque http passthrough", arg: "jira:http://localhost:8080/PROJ", want: "http://localhost:8080/PROJ"},
		{name: "opaque https passthrough", arg: "jira:https://host/PROJ/PROJ-1", want: "https://host/PROJ/PROJ-1"},
		{name: "opaque non-http rejected", arg: "jira:ftp://host/x", wantErr: true},
		{name: "empty host rejected", arg: "jira:", wantErr: true},
		{name: "wrong scheme rejected", arg: "file:///x", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := baseURLFromArg(mustParseURL(t, tc.arg))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("baseURLFromArg(%q) = %q, want error", tc.arg, got)
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

func TestNodeFromBase(t *testing.T) {
	cases := []struct {
		base                          string
		wantOrigin, wantProj, wantIss string
		wantErr                       bool
	}{
		{base: "https://acme.atlassian.net", wantOrigin: "https://acme.atlassian.net"},
		{base: "https://acme.atlassian.net/", wantOrigin: "https://acme.atlassian.net"},
		{base: "https://acme.atlassian.net/PROJ", wantOrigin: "https://acme.atlassian.net", wantProj: "PROJ"},
		{base: "http://h:8080/PROJ/PROJ-42", wantOrigin: "http://h:8080", wantProj: "PROJ", wantIss: "PROJ-42"},
		{base: "https://h/A/B/C", wantErr: true},
	}
	for _, tc := range cases {
		origin, proj, iss, err := nodeFromBase(tc.base)
		if tc.wantErr {
			if err == nil {
				t.Errorf("nodeFromBase(%q) = nil error, want error", tc.base)
			}
			continue
		}
		if err != nil {
			t.Fatalf("nodeFromBase(%q): %v", tc.base, err)
		}
		if origin != tc.wantOrigin || proj != tc.wantProj || iss != tc.wantIss {
			t.Errorf("nodeFromBase(%q) = (%q,%q,%q), want (%q,%q,%q)",
				tc.base, origin, proj, iss, tc.wantOrigin, tc.wantProj, tc.wantIss)
		}
	}
}

func TestConnectionFromArg_UserinfoWins(t *testing.T) {
	t.Setenv(envUsername, "envuser")
	t.Setenv(envToken, "envtoken")

	base, user, token, err := connectionFromArg(mustParseURL(t, "jira://urluser:urltoken@host/PROJ"))
	if err != nil {
		t.Fatalf("connectionFromArg: %v", err)
	}
	if base != "https://host/PROJ" {
		t.Errorf("base = %q, want stripped of userinfo", base)
	}
	if user != "urluser" || token != "urltoken" {
		t.Errorf("creds = %q/%q, want urluser/urltoken", user, token)
	}
}

func TestConnectionFromArg_EnvFallback(t *testing.T) {
	t.Setenv(envUsername, "envuser")
	t.Setenv(envToken, "envtoken")

	_, user, token, err := connectionFromArg(mustParseURL(t, "jira://host/PROJ"))
	if err != nil {
		t.Fatalf("connectionFromArg: %v", err)
	}
	if user != "envuser" || token != "envtoken" {
		t.Errorf("creds = %q/%q, want envuser/envtoken", user, token)
	}
}

func TestIssuePathAndProjectOfKey(t *testing.T) {
	cases := map[string]struct{ proj, path string }{
		"PROJ-42":    {proj: "PROJ", path: "PROJ/PROJ-42.json"},
		"A-1":        {proj: "A", path: "A/A-1.json"},
		"MULTI-WORD": {proj: "MULTI", path: "MULTI/MULTI-WORD.json"},
		"NOHYPHEN":   {proj: "NOHYPHEN", path: "NOHYPHEN/NOHYPHEN.json"},
	}
	for key, want := range cases {
		if got := projectOfKey(key); got != want.proj {
			t.Errorf("projectOfKey(%q) = %q, want %q", key, got, want.proj)
		}
		if got := issuePath(key); got != want.path {
			t.Errorf("issuePath(%q) = %q, want %q", key, got, want.path)
		}
	}
}

func TestJiraURIForNode(t *testing.T) {
	cases := []struct {
		origin, proj, iss, want string
	}{
		{origin: "https://acme.atlassian.net", proj: "PROJ", iss: "", want: "jira://acme.atlassian.net/PROJ"},
		{origin: "https://acme.atlassian.net", proj: "PROJ", iss: "PROJ-1", want: "jira://acme.atlassian.net/PROJ/PROJ-1"},
		{origin: "https://acme.atlassian.net", proj: "", iss: "", want: "jira://acme.atlassian.net"},
		{origin: "http://h:8080", proj: "PROJ", iss: "", want: "jira:http://h:8080/PROJ"},
	}
	for _, tc := range cases {
		if got := jiraURIForNode(tc.origin, tc.proj, tc.iss).String(); got != tc.want {
			t.Errorf("jiraURIForNode(%q,%q,%q) = %q, want %q",
				tc.origin, tc.proj, tc.iss, got, tc.want)
		}
	}
}
