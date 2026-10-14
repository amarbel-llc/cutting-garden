package org

import (
	"context"
	"net/url"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
)

const mimeOrg = "text/org"

// headlineContent is read_node's structured view of a headline (FDR 0026
// D5): its listing fields plus properties and own body. The raw subtree
// rides alongside as Raw.
type headlineContent struct {
	Fields     map[string]any    `json:"fields"`
	Properties map[string]string `json:"properties"`
	Body       string            `json:"body"`
	Warnings   []orgtext.Warning `json:"warnings,omitempty"`
}

// fileContent is read_node's structured view of a file.
type fileContent struct {
	File      string            `json:"file"`
	Keywords  orgtext.Keywords  `json:"todo_keywords"`
	FileTags  []string          `json:"filetags"`
	Headlines int               `json:"headlines"`
	Warnings  []orgtext.Warning `json:"warnings,omitempty"`
}

// ReadLeaf serves a file's or headline's own body — even though both are
// containers (cutting-garden-plugins(7), "A CONTAINER MAY HAVE ITS OWN
// BODY"). A folder has none.
func (Plugin) ReadLeaf(ctx context.Context, node *url.URL) (cutting_garden_plugins.LeafContent, bool, error) {
	t, err := resolve(node)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}
	switch t.ref.kind {
	case kindFolder:
		return cutting_garden_plugins.LeafContent{}, false, nil
	case kindFile:
		pf, err := t.file(ctx)
		if err != nil {
			return cutting_garden_plugins.LeafContent{}, false, err
		}
		count := 0
		eachHeadline(pf.top, func(*hnode) { count++ })
		return cutting_garden_plugins.LeafContent{
			Structured: fileContent{
				File:      pf.rel,
				Keywords:  pf.file.Keywords,
				FileTags:  nonNil(pf.file.FileTags),
				Headlines: count,
				Warnings:  pf.file.Warnings,
			},
			Raw:         pf.file.Bytes(),
			RawMimeType: mimeOrg,
		}, true, nil
	default:
		pf, n, err := t.headline(ctx)
		if err != nil {
			return cutting_garden_plugins.LeafContent{}, false, err
		}
		props := map[string]string{}
		for _, p := range n.h.Properties {
			props[p.Key] = p.Value
		}
		return cutting_garden_plugins.LeafContent{
			Structured: headlineContent{
				Fields:     headlineNode(pf, n, time.Now()).Fields,
				Properties: props,
				Body:       n.h.Body,
				Warnings:   pf.file.Warnings,
			},
			Raw:         pf.file.Source(n.h.SubtreeSpan),
			RawMimeType: mimeOrg,
		}, true, nil
	}
}
