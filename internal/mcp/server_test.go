package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/purse-first/libs/go-mcp/server"
	"code.linenisgreat.com/purse-first/libs/go-mcp/transport"
)

// rpcResponse is the slice of a JSON-RPC response this test inspects.
type rpcResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// runServerOnce feeds newline-delimited JSON-RPC requests through a real
// go-mcp server backed by the fake-resolver provider, waits for the
// server to drain on input EOF, and returns the responses indexed by id.
func runServerOnce(t *testing.T, requests ...string) map[int64]rpcResponse {
	t.Helper()

	root, err := url.Parse("faketest://h/")
	if err != nil {
		t.Fatalf("parse root: %v", err)
	}
	provider := &Resources{
		roots:    []*url.URL{root},
		resolve:  fakeResolve,
		facets:   newFacetCache(),
		listings: newListingCache(),
	}

	var out bytes.Buffer
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")

	srv, err := server.New(
		transport.NewStdio(in, &out),
		server.Options{ServerName: serverName, Resources: provider},
	)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	// Input is a fixed reader: the server reads every line, then hits EOF
	// and Run returns after draining in-flight handlers (gracefulShutdown
	// waits), so the buffer is safe to read here.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if runErr := srv.Run(ctx); runErr != nil {
		t.Fatalf("server.Run: %v", runErr)
	}

	byID := map[int64]rpcResponse{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("decode response %q: %v", line, err)
		}
		byID[resp.ID] = resp
	}
	return byID
}

func TestServer_ResourcesListRoundTrip(t *testing.T) {
	resps := runServerOnce(
		t,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
	)

	resp, ok := resps[1]
	if !ok {
		t.Fatalf("no response for id 1; got %+v", resps)
	}
	if resp.Error != nil {
		t.Fatalf("resources/list errored: %s", resp.Error.Message)
	}

	var result struct {
		Resources []struct {
			URI      string `json:"uri"`
			Name     string `json:"name"`
			MimeType string `json:"mimeType"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result %s: %v", resp.Result, err)
	}
	if len(result.Resources) != 2 {
		t.Fatalf("got %d resources, want 2: %+v", len(result.Resources), result.Resources)
	}
}

func TestServer_ResourcesReadRoundTrip(t *testing.T) {
	resps := runServerOnce(
		t,
		`{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"faketest://h/work"}}`,
	)

	resp, ok := resps[7]
	if !ok {
		t.Fatalf("no response for id 7; got %+v", resps)
	}
	if resp.Error != nil {
		t.Fatalf("resources/read errored: %s", resp.Error.Message)
	}

	var result struct {
		Contents []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result %s: %v", resp.Result, err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(result.Contents))
	}
	var views []nodeView
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &views); err != nil {
		t.Fatalf("decode listing %q: %v", result.Contents[0].Text, err)
	}
	if len(views) != 1 || views[0].URI != "faketest://h/work/task1.ics" {
		t.Fatalf("unexpected child listing: %+v", views)
	}
}
