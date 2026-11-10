package org

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// The org plugin opts into concurrent reads (cutting-garden#313) at a small
// bound, once its ETag confirmation is shared (TestConcurrentFirstReadsShare
// OneConfirmation). This pins the opt-in and that overlapping reads of several
// files and headlines, forced to overlap by server delays, return exactly what
// the same reads return one at a time.
func TestConcurrentReader_OptedInAtFour(t *testing.T) {
	cr, ok := any(Plugin{}).(cutting_garden_plugins.ConcurrentReader)
	if !ok {
		t.Fatal("the org plugin does not implement ConcurrentReader")
	}
	if got := cr.MaxConcurrentReads(); got != 4 {
		t.Errorf("MaxConcurrentReads() = %d, want 4", got)
	}
}

func TestConcurrentReads_MatchSequential(t *testing.T) {
	reads := []string{
		"list org://test/",
		"list org://test/_inbox.org",
		"list org://test/projects/garden.org",
		"leaf org://test/_inbox.org?h=Call+the+plumber",
		"leaf org://test/_inbox.org?id=abc-123",
		"leaf org://test/_inbox.org?h=Dup&n=2",
		"leaf org://test/projects/garden.org?h=Order+seeds",
		"list org://test/_inbox.org?id=abc-123",
	}
	do := func(ctx context.Context, read string) string {
		var kind, uri string
		if _, err := fmt.Sscan(read, &kind, &uri); err != nil {
			return "bad read " + read
		}
		if kind == "list" {
			nodes, err := Plugin{}.ListRoots(ctx, mustURL(t, uri))
			return fmt.Sprintf("%v %v", uris(nodes), err)
		}
		content, ok, err := Plugin{}.ReadLeaf(ctx, mustURL(t, uri))
		return fmt.Sprintf("%q %v %v", content.Raw, ok, err)
	}

	setup(t)
	seqCall := cutting_garden_plugins.WithCallScope(context.Background())
	want := make([]string, len(reads))
	for i, r := range reads {
		want[i] = do(seqCall, r)
	}

	srv := setup(t)
	srv.Delay("PROPFIND", 50*time.Millisecond)
	srv.Delay("GET", 50*time.Millisecond)
	call := cutting_garden_plugins.WithCallScope(context.Background())
	got := make([]string, len(reads))
	var wg sync.WaitGroup
	for round := 0; round < 3; round++ { // three overlapping passes over the same reads
		for i, r := range reads {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if s := do(call, r); round == 0 {
					got[i] = s
				} else if s != want[i] {
					t.Errorf("pass %d, %s:\n got  %s\n want %s", round, r, s, want[i])
				}
			}()
		}
	}
	wg.Wait()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("concurrent reads differ from sequential (-want +got):\n%s", diff)
	}
}
