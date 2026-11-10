package trellis_eval

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
)

// Concurrent per-node work (cutting-garden#313). A plugin opts in through
// cgp.ConcurrentReader; without it the walk stays sequential. These tests
// drive a counting fake: a flat tree of leaves for the per-node read rounds,
// and a nested tree whose listings, filters and closures must come out
// exactly as the sequential walk's.

// gatedTree is a RootLister + LeafReader whose ReadLeaf calls can be held at
// a barrier: a round releases when `batch` calls wait, or when every
// remaining call waits, so the rounds counted are the rounds the evaluator
// actually ran. A round that never fills releases after a timeout (a
// sequential walk under a batch > 1), so a red run terminates. Without -race:
// this repo has no race lane yet (godyn-test passes runtime flags only).
type gatedTree struct {
	children map[string][]cgp.Node
	bodies   map[string]string
	failing  map[string]bool

	gate      bool
	batch     int
	remaining int

	mu       sync.Mutex
	cond     *sync.Cond
	waiting  int
	gen      int
	rounds   int
	inFlight int
	maxIn    int
	listed   []string
	read     []string
}

type concurrentGatedTree struct {
	*gatedTree
	limit int
}

func (c concurrentGatedTree) MaxConcurrentReads() int { return c.limit }

var (
	_ cgp.LeafReader       = (*gatedTree)(nil)
	_ cgp.ConcurrentReader = concurrentGatedTree{}
)

func (*gatedTree) Schemes() []string { return []string{"fake"} }
func (*gatedTree) TypeTag() string   { return "fake-v1" }
func (*gatedTree) Types() []cgp.NodeType {
	return []cgp.NodeType{{Tag: "dir-v1", Container: true}, {Tag: "doc-v1"}}
}

func (g *gatedTree) ListRoots(_ context.Context, u *url.URL) ([]cgp.Node, error) {
	g.mu.Lock()
	g.listed = append(g.listed, u.String())
	g.mu.Unlock()
	return g.children[u.String()], nil
}

func (g *gatedTree) ReadLeaf(ctx context.Context, u *url.URL) (cgp.LeafContent, bool, error) {
	key := u.String()
	g.mu.Lock()
	g.read = append(g.read, key)
	g.inFlight++
	g.maxIn = max(g.maxIn, g.inFlight)
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.inFlight--
		g.mu.Unlock()
	}()
	if g.gate {
		g.await()
	}
	if g.failing[key] {
		return cgp.LeafContent{}, false, fmt.Errorf("read %s failed", key)
	}
	if !g.gate && g.failing != nil {
		// The error test: a slow sibling that honours cancellation.
		select {
		case <-ctx.Done():
			return cgp.LeafContent{}, false, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	body, ok := g.bodies[key]
	return cgp.LeafContent{Raw: []byte(body), RawMimeType: "text/plain"}, ok, nil
}

// await holds the caller until its round fills.
func (g *gatedTree) await() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cond == nil {
		g.cond = sync.NewCond(&g.mu)
	}
	gen := g.gen
	g.waiting++
	if g.waiting >= g.batch || g.waiting >= g.remaining {
		g.release()
		return
	}
	// Long enough that a loaded host does not split a filling round (a split
	// would add a round and flake (a)); it only fires in a red run.
	deadline := time.AfterFunc(2*time.Second, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.gen == gen {
			g.release()
		}
	})
	defer deadline.Stop()
	for g.gen == gen {
		g.cond.Wait()
	}
}

func (g *gatedTree) release() {
	g.remaining -= g.waiting
	g.waiting = 0
	g.rounds++
	g.gen++
	g.cond.Broadcast()
}

// flatDocs is an anchor with n doc leaves, l00…, each body "keep" or "drop".
func flatDocs(n int) *gatedTree {
	g := &gatedTree{children: map[string][]cgp.Node{}, bodies: map[string]string{}}
	for i := range n {
		uri := fmt.Sprintf("fake://f/l%02d", i)
		g.children["fake://f/"] = append(g.children["fake://f/"], cgp.Node{URI: urlp(uri), Type: "doc-v1"})
		g.bodies[uri] = map[bool]string{true: "keep", false: "drop"}[i%3 == 0]
	}
	return g
}

func evalNodes(t *testing.T, lister cgp.RootLister, anchor, query string) ([]string, error) {
	t.Helper()
	q, err := trellis.Parse(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	nodes, err := Evaluate(context.Background(), q, urlp(anchor), lister)
	got := make([]string, len(nodes))
	for i, n := range nodes {
		got[i] = n.URIString()
	}
	return got, err
}

// TestConcurrency_ReadRounds is (a): 20 per-node leaf reads under a plugin
// opted in at 4 run in ceil(20/4) = 5 rounds of 4; a plugin that does not opt
// in runs them one at a time.
func TestConcurrency_ReadRounds(t *testing.T) {
	const n, k = 20, 4

	opted := flatDocs(n)
	opted.gate, opted.batch, opted.remaining = true, k, n
	if _, err := evalNodes(t, concurrentGatedTree{opted, k}, "fake://f/", `_body*=keep`); err != nil {
		t.Fatal(err)
	}
	if opted.rounds != (n+k-1)/k {
		t.Errorf("opted in at %d: %d reads took %d rounds, want %d", k, n, opted.rounds, (n+k-1)/k)
	}
	if opted.maxIn != k {
		t.Errorf("opted in at %d: max reads in flight %d, want %d", k, opted.maxIn, k)
	}

	plain := flatDocs(n)
	plain.gate, plain.batch, plain.remaining = true, 1, n
	if _, err := evalNodes(t, plain, "fake://f/", `_body*=keep`); err != nil {
		t.Fatal(err)
	}
	if plain.maxIn != 1 || plain.rounds != n {
		t.Errorf("not opted in: max in flight %d over %d rounds, want 1 over %d", plain.maxIn, plain.rounds, n)
	}

	// A plugin's limit above the evaluator's bound is clamped to the bound:
	// rounds fill at the bound, and a ninth read in flight would show in maxIn.
	wide := flatDocs(n)
	wide.gate, wide.batch, wide.remaining = true, maxEvalWorkers, n
	if _, err := evalNodes(t, concurrentGatedTree{wide, 1000}, "fake://f/", `_body*=keep`); err != nil {
		t.Fatal(err)
	}
	if wide.maxIn != maxEvalWorkers {
		t.Errorf("opted in at 1000: max in flight %d, want the evaluator's bound %d", wide.maxIn, maxEvalWorkers)
	}
}

// nestedDocs is a three-level tree: the anchor holds dirs d0…d5, each dir
// holds sub-dirs and docs; a dir's inline label is "keep" for even dirs.
//
//	fake://n/ -> dN (label keep|drop) -> dN/sM (dir) -> dN/sM/xK (doc)
//	                                  -> dN/yK (doc)
func nestedDocs() *gatedTree {
	g := &gatedTree{children: map[string][]cgp.Node{}, bodies: map[string]string{}}
	add := func(parent, uri, typ string, fields map[string]any) {
		g.children[parent] = append(g.children[parent], cgp.Node{URI: urlp(uri), Type: typ, Fields: fields})
	}
	for d := range 6 {
		dir := fmt.Sprintf("fake://n/d%d/", d)
		add("fake://n/", dir, "dir-v1", map[string]any{"label": map[bool]string{true: "keep", false: "drop"}[d%2 == 0]})
		for s := range 3 {
			sub := fmt.Sprintf("%ss%d/", dir, s)
			add(dir, sub, "dir-v1", nil)
			for x := range 3 {
				doc := fmt.Sprintf("%sx%d", sub, x)
				add(sub, doc, "doc-v1", nil)
				g.bodies[doc] = map[bool]string{true: "keep", false: "drop"}[(d+s+x)%2 == 0]
			}
		}
		for y := range 2 {
			doc := fmt.Sprintf("%sy%d", dir, y)
			add(dir, doc, "doc-v1", nil)
			g.bodies[doc] = map[bool]string{true: "keep", false: "drop"}[(d+y)%3 != 0]
		}
	}
	return g
}

// TestConcurrency_SameAsSequential is (b): for a descend, a forward closure,
// a filter that prunes siblings before the next hop, an existential subpath
// and a reverse scan, the opted-in walk returns the same nodes in the same
// order as the sequential one, and lists and reads the same set of nodes.
func TestConcurrency_SameAsSequential(t *testing.T) {
	for _, query := range []string{
		`!dir-v1 -> !doc-v1`,
		`->> !doc-v1 _body*=keep`,
		`!dir-v1 label=keep -> !dir-v1 -> _body*=keep`,
		`!dir-v1 [-> !doc-v1 _body*=drop]`,
		`->> !doc-v1 _body*=drop <- !dir-v1`,
		`->> !doc-v1 _body*=keep <<- !dir-v1`,
	} {
		t.Run(query, func(t *testing.T) {
			seq := nestedDocs()
			want, err := evalNodes(t, seq, "fake://n/", query)
			if err != nil {
				t.Fatal(err)
			}
			par := nestedDocs()
			got, err := evalNodes(t, concurrentGatedTree{par, 4}, "fake://n/", query)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, want) {
				t.Errorf("result order differs:\n concurrent %v\n sequential %v", got, want)
			}
			for what, pair := range map[string][2][]string{
				"listed": {par.listed, seq.listed},
				"read":   {par.read, seq.read},
			} {
				a, b := slices.Clone(pair[0]), slices.Clone(pair[1])
				slices.Sort(a)
				slices.Sort(b)
				if !slices.Equal(a, b) {
					t.Errorf("the set of nodes %s differs:\n concurrent %v\n sequential %v", what, a, b)
				}
			}
			if len(want) == 0 {
				t.Errorf("the query matched nothing; the fixture does not exercise it")
			}
		})
	}
}

// TestConcurrency_LowestIndexErrorCancels is (c): reads l03 and l07 fail; the
// error returned is l03's (the one the sequential walk meets first) and the
// reads not yet started are cancelled rather than run.
func TestConcurrency_LowestIndexErrorCancels(t *testing.T) {
	for name, wrap := range map[string]func(*gatedTree) cgp.RootLister{
		"sequential": func(g *gatedTree) cgp.RootLister { return g },
		"opted in":   func(g *gatedTree) cgp.RootLister { return concurrentGatedTree{g, 4} },
	} {
		t.Run(name, func(t *testing.T) {
			g := flatDocs(20)
			g.failing = map[string]bool{"fake://f/l03": true, "fake://f/l07": true}
			_, err := evalNodes(t, wrap(g), "fake://f/", `_body*=keep`)
			if err == nil || !strings.Contains(err.Error(), "fake://f/l03") {
				t.Fatalf("error = %v, want l03's", err)
			}
			if len(g.read) >= 20 {
				t.Errorf("%d reads started; the reads after the error were not cancelled", len(g.read))
			}
		})
	}
}
