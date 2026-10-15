package org

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func headlineByName(t *testing.T, fileURI, name string) cutting_garden_plugins.Node {
	t.Helper()
	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, fileURI))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("no headline %q in %s", name, fileURI)
	return cutting_garden_plugins.Node{}
}

func TestOrganize_DescribeFacetWritesIsConsistent(t *testing.T) {
	setup(t)
	p := Plugin{}
	if err := cutting_garden_plugins.ValidateFacetWrites(p.DescribeFacets(), p.DescribeFacetWrites()); err != nil {
		t.Fatal(err)
	}
	modes := map[string]string{}
	for _, w := range p.DescribeFacetWrites()[0].Writes {
		mode := string(w.Mode)
		if w.Clearable {
			mode += "+clear"
		}
		modes[w.DimensionKey] = mode
	}
	want := map[string]string{
		fieldState: "one", fieldTags: "many", fieldPriority: "one",
		fieldScheduled: "one+clear", fieldDeadline: "one+clear", fieldClosed: "none",
		fieldScheduledBand: "none", fieldDeadlineBand: "none", fieldFile: "none",
	}
	if diff := cmp.Diff(want, modes); diff != "" {
		t.Errorf("write modes (-want +got):\n%s", diff)
	}
}

func TestOrganize_BuildPatches(t *testing.T) {
	setup(t)
	p := Plugin{}
	ctx := context.Background()
	plumber := headlineByName(t, "org://test/_inbox.org", "Call the plumber")
	plumber.Fields[fieldScheduled] = "2026-01-31"

	cases := []struct {
		name string
		got  func() ([]byte, error)
		want string
	}{
		{"state move", func() ([]byte, error) {
			return p.BuildFacetWritePatch(ctx, plumber, cutting_garden_plugins.FacetWrite{DimensionKey: fieldState}, "DONE")
		}, `{"state":"DONE"}`},
		{"state to none clears", func() ([]byte, error) {
			return p.BuildFacetWritePatch(ctx, plumber, cutting_garden_plugins.FacetWrite{DimensionKey: fieldState}, "none")
		}, `{"state":null}`},
		{"month bucket keeps the day, clamped", func() ([]byte, error) {
			return p.BuildFacetWritePatch(ctx, plumber, cutting_garden_plugins.FacetWrite{DimensionKey: fieldScheduled}, "2026-02")
		}, `{"scheduled":"2026-02-28"}`},
		{"year bucket keeps month and day", func() ([]byte, error) {
			return p.BuildFacetWritePatch(ctx, plumber, cutting_garden_plugins.FacetWrite{DimensionKey: fieldScheduled}, "2027")
		}, `{"scheduled":"2027-01-31"}`},
		{"no-value section clears a date", func() ([]byte, error) {
			return p.BuildFacetWritePatch(ctx, plumber, cutting_garden_plugins.FacetWrite{DimensionKey: fieldDeadline}, "")
		}, `{"deadline":null}`},
		{"tags become own tags minus inherited", func() ([]byte, error) {
			return p.BuildMembershipWritePatch(ctx, plumber, cutting_garden_plugins.FacetWrite{DimensionKey: fieldTags},
				[]string{"zz-inbox", "project-house", "urgent"})
		}, `{"own_tags":["project-house","urgent"]}`},
		{"atoms and trailer", func() ([]byte, error) {
			return p.BuildFieldWritePatch(ctx, plumber, []cutting_garden_plugins.FieldEdit{
				{Name: fieldPriority, Value: "B"}, {Name: fieldTitle, Value: "Call the plumber today"},
			})
		}, `{"priority":"B","title":"Call the plumber today"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.got()
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}

	_, err := p.BuildMembershipWritePatch(ctx, plumber, cutting_garden_plugins.FacetWrite{DimensionKey: fieldTags},
		[]string{"project-house"})
	if err == nil || !strings.Contains(err.Error(), `"zz-inbox" is inherited`) {
		t.Errorf("dropping an inherited tag: err = %v", err)
	}
	_, err = p.BuildFacetWritePatch(ctx, plumber, cutting_garden_plugins.FacetWrite{DimensionKey: fieldFile}, "x.org")
	if err == nil {
		t.Error("the file dimension is read-only")
	}
}

func TestOrganize_PresentBoxAtoms(t *testing.T) {
	setup(t)
	plumber := headlineByName(t, "org://test/_inbox.org", "Call the plumber")
	got := Plugin{}.PresentBoxAtoms(plumber)
	want := []cutting_garden_plugins.BoxAtom{{Name: fieldDeadline, Value: "2000-01-01"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("atoms (-want +got):\n%s", diff)
	}
}

// A built patch applies through PatchNode, and a date-only reschedule
// keeps the time of day.
func TestOrganize_PatchRoundTrip(t *testing.T) {
	srv := setup(t)
	srv.Put("r.org", []byte("* TODO Standup :work:\nSCHEDULED: <2026-10-19 Mon 09:30 +1w>\n:PROPERTIES:\n:ID: s1\n:END:\n"))
	node := headlineByName(t, "org://test/r.org", "Standup")
	body, err := Plugin{}.BuildFacetWritePatch(context.Background(), node,
		cutting_garden_plugins.FacetWrite{DimensionKey: fieldScheduled}, "2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Plugin{}).PatchNode(context.Background(), node.URI, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	want := "* TODO Standup :work:\nSCHEDULED: <2026-11-19 Thu 09:30 +1w>\n:PROPERTIES:\n:ID: s1\n:END:\n"
	if got := string(srv.Content("r.org")); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
