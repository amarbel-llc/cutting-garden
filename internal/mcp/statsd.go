package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// mutate_nodes takes each op body as a STRING, so a JSON body must be
// JSON-encoded into it. Whether agents get that wrong, and how, is measured
// rather than guessed (cutting-garden#319): best-effort statsd counters, one
// per call and one per mis-encoded body, named by the anchor's scheme. They
// are a no-op unless STATSD_HOST is set (STATSD_PORT defaults to 8125), the
// same opt-in dodder's MCP server uses.

var (
	statsdOnce sync.Once
	statsdAddr string
)

func statsdTarget() string {
	statsdOnce.Do(func() {
		host := os.Getenv("STATSD_HOST")
		if host == "" {
			return
		}
		port := os.Getenv("STATSD_PORT")
		if port == "" {
			port = "8125"
		}
		statsdAddr = net.JoinHostPort(host, port)
	})
	return statsdAddr
}

// emitCounter sends a statsd counter increment (`name:1|c`) over UDP,
// swallowing every error so it can never block or fail a call. A variable so
// tests can record instead.
var emitCounter = func(name string) {
	addr := statsdTarget()
	if addr == "" {
		return
	}
	conn, err := net.DialTimeout("udp", addr, 100*time.Millisecond)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = fmt.Fprintf(conn, "%s:1|c", name)
}

// countMutateCall reads the raw mutate_nodes arguments leniently — so even a
// call that fails to decode is counted — and emits one calls counter per
// group plus one body counter per mis-encoded op body.
func countMutateCall(args json.RawMessage) {
	var in struct {
		Groups []struct {
			Anchor string                    `json:"anchor"`
			Ops    []mutateRawOp             `json:"ops"`
			Sweep  *struct{ Op mutateRawOp } `json:"sweep"`
		} `json:"groups"`
	}
	if json.Unmarshal(args, &in) != nil {
		return
	}

	for _, group := range in.Groups {
		prefix := "cutting_garden.mcp.mutate_nodes." + metricScheme(group.Anchor)
		emitCounter(prefix + ".calls")

		ops := group.Ops
		if group.Sweep != nil {
			ops = append(ops, group.Sweep.Op)
		}
		for _, op := range ops {
			if shape := classifyRawBody(op); shape != "" {
				emitCounter(prefix + ".body." + shape)
			}
		}
	}
}

type mutateRawOp struct {
	Kind string          `json:"kind"`
	Body json.RawMessage `json:"body"`
}

func classifyRawBody(op mutateRawOp) string {
	raw := bytes.TrimSpace(op.Body)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var body string
	if json.Unmarshal(raw, &body) != nil {
		return "object_not_string"
	}
	return classifyMutateBody(op.Kind, body)
}

// classifyMutateBody names how a string body is mis-encoded, or "" when it
// is well-formed (or not JSON at all, like a raw iCalendar create). A patch
// body is always a JSON object; another kind's body is judged as JSON only
// when it looks like JSON.
func classifyMutateBody(kind, body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	looksJSON := strings.ContainsRune(`{["`, rune(trimmed[0]))
	if kind != "patch" && !looksJSON {
		return ""
	}

	var value any
	if json.Unmarshal([]byte(trimmed), &value) != nil {
		return "invalid_json"
	}
	switch v := value.(type) {
	case map[string]any:
		return ""
	case string:
		var inner any
		if json.Unmarshal([]byte(v), &inner) == nil {
			return "json_in_json_string"
		}
	}
	return "not_object"
}

// metricScheme is a statsd-safe name for an anchor's scheme.
func metricScheme(anchor string) string {
	u, err := url.Parse(anchor)
	if err != nil || u.Scheme == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, strings.ToLower(u.Scheme))
}
