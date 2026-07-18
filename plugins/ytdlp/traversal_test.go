package cutting_garden_plugin_ytdlp

import (
	"context"
	"sort"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestPlugin_Types_DeclaresChannelAndVideo(t *testing.T) {
	types := Plugin{}.Types()
	if len(types) != 2 {
		t.Fatalf("Types() = %+v, want 2 entries", types)
	}
	byTag := map[string]cutting_garden_plugins.NodeType{}
	for _, nt := range types {
		byTag[nt.Tag] = nt
	}
	ch, ok := byTag[typeChannel]
	if !ok || !ch.Container {
		t.Errorf("typeChannel = %+v, want Container=true", ch)
	}
	vid, ok := byTag[typeVideo]
	if !ok || vid.Container {
		t.Errorf("typeVideo = %+v, want Container=false", vid)
	}
	if vid.BodyMimeType() != cutting_garden_plugins.MimeTypeDefault {
		t.Errorf("typeVideo.BodyMimeType() = %q, want default", vid.BodyMimeType())
	}
}

func TestPlugin_ListRoots_ChannelYieldsVideoLeaves(t *testing.T) {
	installFlatPlaylistFake(t)

	nodes, err := Plugin{}.ListRoots(context.Background(), mustParseURL(t, "https://www.youtube.com/@channel"))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("ListRoots returned %d nodes, want 3: %+v", len(nodes), nodes)
	}

	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
		if n.Type != typeVideo {
			t.Errorf("node %q Type = %q, want %q", n.Name, n.Type, typeVideo)
		}
		if n.URI == nil || n.URI.Scheme != "https" {
			t.Errorf("node %q URI = %v, want an https URI", n.Name, n.URI)
		}
	}
	sort.Strings(names)
	want := []string{"Video One", "Video Three (live)", "Video Two"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestPlugin_ListRoots_VideoNodeURIIsCapturableThroughSingleVideoPath(t *testing.T) {
	installFlatPlaylistFake(t)

	nodes, err := Plugin{}.ListRoots(context.Background(), mustParseURL(t, "https://www.youtube.com/@channel"))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	for _, n := range nodes {
		if err := (Plugin{}).ValidateSource(n.URI, n.URIString()); err != nil {
			t.Errorf("node %q URI %s failed ValidateSource (must round-trip through the existing single-video capture path): %v",
				n.Name, n.URIString(), err)
		}
	}
}

func TestPlugin_ListRoots_SingleVideoIsLeafWithNoChildren(t *testing.T) {
	installFlatPlaylistFake(t)

	nodes, err := Plugin{}.ListRoots(context.Background(), mustParseURL(t, "https://youtu.be/dQw4w9WgXcQ"))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("ListRoots on a single video = %+v, want no children (a leaf)", nodes)
	}
}

func TestPlugin_ListRoots_NilNodeErrors(t *testing.T) {
	if _, err := (Plugin{}).ListRoots(context.Background(), nil); err == nil {
		t.Error("ListRoots(nil) returned nil error")
	}
}

func TestPlugin_ListRoots_RejectsOffAllowlistHost(t *testing.T) {
	installFlatPlaylistFake(t)

	if _, err := (Plugin{}).ListRoots(context.Background(), mustParseURL(t, "https://vimeo.com/123")); err == nil {
		t.Error("ListRoots on an off-allowlist host returned nil error")
	}
}
