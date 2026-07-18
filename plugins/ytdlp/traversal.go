package cutting_garden_plugin_ytdlp

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

const (
	// typeChannel is a yt-dlp channel or playlist — a container whose
	// children are its videos. Tag convention matches caldav
	// ("caldav-calendar-v1") and git ("git-branch-v1"): bare
	// `<scheme>-<noun>-v1`, no "cutting_garden-" prefix (FDR 0014 §79).
	typeChannel = "ytdlp-channel-v1"
	// typeVideo is a single video — a leaf. A video's captured body is
	// several sidecar files (media, info.json, thumbnail, subs), not one
	// blob of a single mimetype, so NodeType.MimeType is left empty
	// (Types() below) and resolves to the leaf default
	// (application/octet-stream) per the BodyMimeType contract.
	typeVideo = "ytdlp-video-v1"
)

var _ cutting_garden_plugins.RootLister = (*Plugin)(nil)

// Types declares the two node types the ytdlp tree is built from: a
// channel/playlist container and a video leaf.
func (Plugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: typeChannel, Container: true},
		{Tag: typeVideo, Container: false},
	}
}

// ListRoots enumerates node's immediate children via the SAME
// flat-playlist primitive CaptureRoot uses to classify a source
// (FDR 0014 §"Where bulk orchestration lives"): a single flat-playlist
// entry means node is itself a video (a leaf — no children); more than
// one means node is a channel/playlist and each entry becomes a video
// Node, carrying whatever facet values the SAME enumeration already
// produced (RFC 0012 §1's no-per-node-refetch rule — see entryFacets).
//
// Unlike CaptureRoot, ListRoots does not apply the FDR 0004
// channelLimitParam/defaultChannelCaptureThreshold guardrail: it is a
// read-only, single-round-trip operation (the flat-playlist probe
// already has every entry in hand) rather than the disk/bandwidth
// operation the guardrail exists to bound, and capping a plain listing
// would work against exactly the progressive-disclosure exploration
// facets/traversal are for (FDR 0021). FDR 0014's own "Huge-tree
// guardrails" note marks a ListRoots-side cap as still open — left open
// here too, deliberately.
func (Plugin) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf(
			"ytdlp plugin: ListRoots requires a node URI",
		)
	}

	source, err := sourceURLFromArg(node)
	if err != nil {
		return nil, err
	}

	entries, err := probeFlatPlaylist(ctx, source)
	if err != nil {
		return nil, err
	}
	if len(entries) <= 1 {
		// A leaf (a plain video URL): no children.
		return nil, nil
	}

	nodes := make([]cutting_garden_plugins.Node, 0, len(entries))
	for _, e := range entries {
		videoURL, ok := entryTargetURL(e)
		if !ok {
			continue
		}
		parsed, err := url.Parse(videoURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		name := e.Title
		if name == "" {
			if id, ok := entryVideoID(e); ok {
				name = id
			}
		}
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:    parsed,
			Name:   name,
			Type:   typeVideo,
			Facets: entryFacets(e),
		})
	}
	return nodes, nil
}
