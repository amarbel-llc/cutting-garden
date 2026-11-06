package cutting_garden_plugins

import (
	"context"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// cancellingMutator cancels the call after its first create, the shape of a
// server whose process is told to stop partway through a batch
// (cutting-garden#392).
type cancellingMutator struct {
	fakeMutatorLister
	cancel context.CancelFunc
}

func (c *cancellingMutator) CreateNode(
	ctx context.Context, uri *url.URL, body io.Reader, typ string,
) error {
	err := c.fakeMutatorLister.CreateNode(ctx, uri, body, typ)
	c.cancel()
	return err
}

// Each op's outcome reaches the observer as it lands, in request order, so
// a caller that loses the reply can still learn what applied.
func TestBulkBestEffortOps_ObserverSeesEachOutcome(t *testing.T) {
	f := &fakeMutatorLister{failOn: map[string]bool{"fake://h/b": true}}
	var seen []string
	ctx := WithBulkOutcomeObserver(context.Background(), func(i int, o BulkOutcome) {
		seen = append(seen, string(o.Status)+" "+o.URI.String())
		if i != len(seen)-1 {
			t.Errorf("outcome %d reported with index %d", len(seen)-1, i)
		}
	})

	BulkBestEffortOps(ctx, f, []BulkOp{
		{Kind: BulkCreate, URI: mustURL(t, "fake://h/a")},
		{Kind: BulkCreate, URI: mustURL(t, "fake://h/b")},
		{Kind: BulkDelete, URI: mustURL(t, "fake://h/c")},
	})

	want := []string{"applied fake://h/a", "failed fake://h/b", "deleted fake://h/c"}
	if !slices.Equal(seen, want) {
		t.Errorf("observed %v, want %v", seen, want)
	}
}

// Once the call is cancelled, no further op starts: each is reported failed
// as not attempted, so the caller can resend exactly those.
func TestBulkBestEffortOps_StopsStartingOpsOnceCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &cancellingMutator{cancel: cancel}

	result := BulkBestEffortOps(ctx, m, []BulkOp{
		{Kind: BulkCreate, URI: mustURL(t, "fake://h/a")},
		{Kind: BulkCreate, URI: mustURL(t, "fake://h/b")},
		{Kind: BulkPatch, URI: mustURL(t, "fake://h/c")},
	})

	if !slices.Equal(m.created, []string{"fake://h/a"}) || len(m.patched) != 0 {
		t.Fatalf("ops ran after the cancel: created %v patched %v", m.created, m.patched)
	}
	if len(result.Outcomes) != 3 {
		t.Fatalf("outcomes = %+v, want one per op", result.Outcomes)
	}
	for _, o := range result.Outcomes[1:] {
		if o.Status != OutcomeFailed || !strings.Contains(o.Err, "not attempted") {
			t.Errorf("outcome after the cancel = %+v, want failed, not attempted", o)
		}
	}
	if len(result.Failed) != 2 {
		t.Errorf("failed = %+v, want the two unattempted ops", result.Failed)
	}
}
