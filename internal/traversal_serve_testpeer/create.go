package traversal_serve_testpeer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// The tracker's creation fixture (RFC 0013 §Creation, forge organize F10): a
// ticket is creatable under a box container and requires its title (the
// trailer). The host builds the node.create_child body from the facet_writes
// fields and the trailer_field — `{"title": …, "state": …, "milestone": …,
// "labels": […]}` — and the peer numbers the new ticket after the container's
// highest-numbered child, as a forge numbers issues.
var (
	_ cutting_garden_plugins.CreationDescriber = (*TreePlugin)(nil)
	_ cutting_garden_plugins.CreateApplier     = (*TreePlugin)(nil)
	_ cutting_garden_plugins.ContainerCreator  = (*TreePlugin)(nil)
)

// DescribeCreation is the CreationDescriber the served peer advertises on its
// node_types entries.
func (p *TreePlugin) DescribeCreation() []cutting_garden_plugins.NodeTypeCreation {
	return []cutting_garden_plugins.NodeTypeCreation{{
		Tag:           TicketType,
		ContainerType: ContainerType,
		Required:      []string{TrailerWriteField},
	}}
}

// BuildCreateBody is the linked CreateApplier: exactly the host-built body a
// wire host sends (the presentation synthesis), so linked and wire creation
// put the same bytes on create_child.
func (p *TreePlugin) BuildCreateBody(
	_ context.Context, typ string, fields map[string][]string, _ string,
) ([]byte, error) {
	return p.presentation().BuildCreateBody(typ, fields)
}

// createTicketLocked creates a ticket under parent from a host-built create
// body. Caller holds p.mu. The body is validated whole before anything
// changes: a missing or non-string title, or a recognized facet field with an
// unusable value, is a bad request (-32602) with the tree untouched.
func (p *TreePlugin) createTicketLocked(
	containerKey string, parent *memNode, data []byte,
) (*url.URL, error) {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, errors.BadRequestf("create_child %s: body is not a JSON object: %s", containerKey, err)
	}
	title, ok := fields[TrailerWriteField].(string)
	if !ok || title == "" {
		return nil, errors.BadRequestf(
			"create_child %s: a ticket needs a non-empty %q", containerKey, TrailerWriteField,
		)
	}
	updates, err := p.facetUpdatesFromPatch(containerKey, TicketType, fields)
	if err != nil {
		return nil, err
	}

	facets := map[string][]cutting_garden_plugins.FacetValue{
		"state": {{Key: "open"}},
	}
	for dimension, values := range updates {
		if len(values) == 0 {
			delete(facets, dimension)
			continue
		}
		facets[dimension] = values
	}
	state := facets["state"][0].Key

	key := fmt.Sprintf("%s/%d", containerKey, nextChildNumber(parent.children)+1)
	p.nodes[key] = &memNode{
		name:       title,
		typ:        TicketType,
		facets:     facets,
		structured: map[string]any{"title": title, "state": state},
	}
	parent.children = append(parent.children, key)
	p.generation++
	if err := p.persistLocked(); err != nil {
		return nil, err
	}

	created, err := url.Parse(key)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return created, nil
}

// nextChildNumber is the highest numeric last segment among children (0 when
// none is numeric).
func nextChildNumber(children []string) int {
	highest := 0
	for _, child := range children {
		n, err := strconv.Atoi(child[strings.LastIndex(child, "/")+1:])
		if err == nil && n > highest {
			highest = n
		}
	}
	return highest
}
