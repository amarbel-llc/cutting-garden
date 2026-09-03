#! /usr/bin/env bats

# The CUD write-tools end-to-end lane (FDR 0020): drive `cutting-garden mcp`
# over its stdio JSON-RPC transport against the caldav testserver and exercise
# create_node -> put_node -> patch_node -> delete_node end to end, plus the #102 clown
# PreToolUse hook gating those tools as `ask`. This is the e2e exercise the
# per-layer Go unit tests (caldav mutate, mcp tools, claude_hooks) do not
# cover in one path.
#
# Dependent mutations (create -> update -> delete) are driven as SEPARATE mcp
# invocations against the one testserver (started in setup, persistent across
# the test): the go-mcp server dispatches the requests of a single connection
# concurrently, so two ordered mutations piped into one process would race.
# One mutation per `cutting-garden mcp` run serializes them; the testserver
# carries state across the runs.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/caldav.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/mcp.bash"
  export output
  start_caldav_server
}

teardown() {
  stop_caldav_server
}

# bats file_tags=mcp

# tools/list advertises the four read tools (describe/list/read/read_facets)
# and the write tools — the four CUD verbs (create/put/patch/delete) plus
# bulk_mutate (RFC 0017, advertised because caldav implements BulkMutator) —
# with the right destructive vs read-only annotations (the hint a client
# gates on, mirrored by the #102 hook).
function mcp_advertises_tools { # @test
  mcp_drive "$CALDAV_SOURCE" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'

  local names
  names="$(echo "$output" | jq -r 'select(.id==2) | (.result.tools | map(.name) | sort | join(","))')"
  assert_equal "$names" "bulk_mutate,create_node,delete_node,describe_node_types,list_nodes,patch_node,put_node,read_facets,read_node"

  # The write tools carry destructiveHint=true; the read tools don't.
  local destructive
  destructive="$(echo "$output" | jq -r 'select(.id==2) |
    ([.result.tools[] | select(.annotations.destructiveHint==true) | .name] | sort | join(","))')"
  assert_equal "$destructive" "bulk_mutate,create_node,delete_node,patch_node,put_node"

  local readonly_tools
  readonly_tools="$(echo "$output" | jq -r 'select(.id==2) |
    ([.result.tools[] | select(.annotations.readOnlyHint==true) | .name] | sort | join(","))')"
  assert_equal "$readonly_tools" "describe_node_types,list_nodes,read_facets,read_node"
}

# read_facets on the calendar-home root serves the memoized summary
# (cutting-garden#151): the same status histogram `list --facets` would
# compute, reachable from a tools-only client. A filter narrows it directly
# (RFC 0012 §6/§9).
function mcp_read_facets { # @test
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 read_facets "$(jq -nc --arg u "$CALDAV_SOURCE" '{uri:$u}')")"
  local text
  text="$(mcp_result_text "$output" 3)"

  echo "$text" | jq -e '.facets.component.VTODO >= 1' >/dev/null ||
    fail "read_facets summary missing component.VTODO: $text"
  echo "$text" | jq -e '.complete == true' >/dev/null ||
    fail "read_facets summary not complete: $text"

  # A filter narrows the summary directly (bypasses the memoized cache) and
  # is reported fresh.
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 read_facets "$(jq -nc --arg u "$CALDAV_SOURCE" '{uri:$u,filter:"component=VTODO"}')")"
  text="$(mcp_result_text "$output" 3)"
  echo "$text" | jq -e '.freshness == "fresh"' >/dev/null ||
    fail "filtered read_facets not reported fresh: $text"
}

# describe_node_types reports caldav's node types and which are writable, with
# the body payload for the writable object leaf.
function mcp_describe_node_types { # @test
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 describe_node_types '{}')"

  local text
  text="$(mcp_result_text "$output" 3)"

  # A per-component object leaf is writable, with both accepted body formats
  # described (the VTODO subtype is the representative check).
  echo "$text" | jq -e '
    any(.[].types[]; .tag=="caldav-object-vtodo-v1"
        and .container==false and .writable==true
        and (.body.accepts | length) >= 2)' >/dev/null ||
    fail "describe missing a writable caldav-object-vtodo-v1 with body: $text"

  # The calendar container is present and NOT writable.
  echo "$text" | jq -e '
    any(.[].types[]; .tag=="caldav-calendar-v1"
        and .container==true and .writable==false)' >/dev/null ||
    fail "describe missing a non-writable caldav-calendar-v1 container: $text"

  # The VTODO type's designated tag set (design G12, native tags slice 2):
  # the categories FieldTag dimension whose values ride each enriched
  # entry's `tags`, with its resolved interpreter (the declared naive
  # default — this lane sets no [tags] override). The calendar container
  # declares no tag set and carries no tag_set key.
  echo "$text" | jq -e '
    any(.[].types[]; .tag=="caldav-object-vtodo-v1"
        and .tag_set.field=="categories"
        and .tag_set.interpreter=="naive")' >/dev/null ||
    fail "describe missing the vtodo tag_set {categories, naive}: $text"
  echo "$text" | jq -e '
    any(.[].types[]; .tag=="caldav-calendar-v1" and (has("tag_set") | not))' >/dev/null ||
    fail "the calendar container grew a tag_set: $text"
}

# list_nodes (browse) surfaces the configured ROOTS as the entry points — a
# root is a container you descend into, NOT its flattened children (that is
# resources/list; a per-calendar root would otherwise dump every event into
# the entry-point listing, circus#29). Descending the root yields its calendars,
# then read_node returns a seeded object's parsed fields — the read half of
# the claude.ai-UI surface (circus#29), wrapping the RootLister traversal as
# tools.
function mcp_browse_and_read { # @test
  # No uri: the configured root itself, as a single container entry point
  # (mirrors `list` with no arg) — not the calendar/objects one level down.
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes '{}')"
  local roots rooturi
  roots="$(mcp_result_text "$output" 3)"
  echo "$roots" | jq -e \
    'length==1 and .[0].container==true and (.[0].uri | contains("/dav/"))' >/dev/null ||
    fail "list_nodes() root entry-point wrong: $roots"

  # Descend the root (using the uri it reported) → its calendars: the
  # testserver's two calendars, Personal and Work — discovered via PROPFIND
  # on the calendar-home root, each labeled by its DAV displayname
  # (cutting-garden#162; also the cutting-garden#120 friendly-label win for
  # accounts configured at the home level).
  rooturi="$(echo "$roots" | jq -r '.[0].uri')"
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes "$(jq -nc --arg u "$rooturi" '{uri:$u}')")"
  local cals
  cals="$(mcp_result_text "$output" 3)"
  echo "$cals" | jq -e 'any(.nodes[]; .name=="Personal")' >/dev/null ||
    fail "list_nodes(root) missing the Personal calendar: $cals"
  echo "$cals" | jq -e 'any(.nodes[]; .name=="Work")' >/dev/null ||
    fail "list_nodes(root) missing the discovered Work calendar: $cals"

  # read_node a seeded VTODO → its parsed task fields. grep (not jq) since a
  # leaf read may append a raw-bytes link line after the JSON.
  local obj read
  obj="$(caldav_object_uri task1.ics)"
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 read_node "$(jq -nc --arg u "$obj" '{uri:$u}')")"
  read="$(mcp_result_text "$output" 3)"
  # MarshalIndent emits "key": "value" (space after the colon); grep the
  # line so a trailing raw-bytes link (when a store is configured) is fine.
  echo "$read" | grep -q '"component": "VTODO"' ||
    fail "read_node(task1) component: $read"
  echo "$read" | grep -q '"summary": "Buy milk"' ||
    fail "read_node(task1) summary: $read"
}

# list_nodes' filter param retrieves the matching nodes directly — the
# #160 fix for the measured 45-tool-call gap (read_facets could only COUNT
# overdue/matching items, never retrieve them). Descend to the "Personal"
# calendar (task1.ics, task2.ics, event1.ics seeded there), then filter for
# component=VTODO: exactly the two tasks come back, wrapped with an honest
# filterApplied/filterMode signal, EACH CARRYING its facets and
# plugin-declared fields (summary) inline — proving the collapse from "list
# everything, read_node each candidate" to one filtered call.
function mcp_list_nodes_filter_retrieves_matching_enriched_nodes { # @test
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes '{}')"
  local rooturi
  rooturi="$(mcp_result_text "$output" 3 | jq -r '.[0].uri')"

  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes "$(jq -nc --arg u "$rooturi" '{uri:$u}')")"
  local personaluri
  personaluri="$(mcp_result_text "$output" 3 | jq -r 'first(.nodes[] | select(.name=="Personal")) | .uri')"
  [[ -n $personaluri ]] || fail "could not find the Personal calendar: $(mcp_result_text "$output" 3)"

  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "$personaluri" '{uri:$u,filter:"component=VTODO"}')")"
  local text
  text="$(mcp_result_text "$output" 3)"

  echo "$text" | jq -e '.filterApplied == true' >/dev/null ||
    fail "filter was not reported applied: $text"
  echo "$text" | jq -e '.filterMode == "plugin"' >/dev/null ||
    fail "filterMode wrong (caldav's EnrichedLister should apply it): $text"

  # Exactly the two VTODOs, never the VEVENT (event1.ics).
  echo "$text" | jq -e '(.nodes | length) == 2' >/dev/null ||
    fail "filtered nodes count wrong (want 2 VTODOs): $text"
  echo "$text" | jq -e 'any(.nodes[]; .name=="task1.ics")' >/dev/null ||
    fail "missing task1.ics: $text"
  echo "$text" | jq -e 'any(.nodes[]; .name=="task2.ics")' >/dev/null ||
    fail "missing task2.ics: $text"
  echo "$text" | jq -e 'all(.nodes[]; .name!="event1.ics")' >/dev/null ||
    fail "event1.ics leaked through a component=VTODO filter: $text"

  # Enough inline data to answer WITHOUT a follow-up read_node: facets and
  # the plugin-declared summary field.
  echo "$text" | jq -e 'all(.nodes[]; .facets.component[0]=="VTODO")' >/dev/null ||
    fail "filtered nodes missing inline component facet: $text"
  echo "$text" | jq -e 'any(.nodes[]; .fields.summary=="Buy milk")' >/dev/null ||
    fail "filtered nodes missing inline summary field: $text"
}

# An enriched list_nodes entry carries the node's presented tag set (design
# G12, native tags slice 2): seed a VTODO with CATEGORIES through create_node,
# then list its calendar — the entry gains a top-level `tags` array with the
# categories in the resolved interpreter's SortKey order (naive here → lexical:
# errand before work despite the stored work,errand), while the untagged
# task1.ics sibling omits the key entirely.
function mcp_list_nodes_enriched_entry_carries_tags { # @test
  local obj
  obj="$(caldav_object_uri tagged.ics)"
  mcp_call create_node "$(jq -nc --arg u "$obj" \
    --arg b "$(printf 'BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:tagged\nSUMMARY:Tagged task\nCATEGORIES:work,errand\nEND:VTODO\nEND:VCALENDAR\n')" \
    '{uri:$u,body:$b,type:"caldav-object-vtodo-v1"}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"

  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "${CALDAV_SOURCE%/dav/}/dav/cal/" '{uri:$u}')")"
  local text
  text="$(mcp_result_text "$output" 3)"
  echo "$text" | jq -e '
    first(.nodes[] | select(.name=="tagged.ics")) | .tags == ["errand","work"]' >/dev/null ||
    fail "tagged.ics entry missing SortKey-ordered tags: $text"
  echo "$text" | jq -e '
    first(.nodes[] | select(.name=="task1.ics")) | has("tags") | not' >/dev/null ||
    fail "untagged task1.ics grew a tags key: $text"
}

# list_nodes' query param (cutting-garden#211's MCP host): a trellis query
# anchored at the uri walks the tree through the real mcp binary — the MCP-suite
# mirror of caldav.bats's `list --query` CLI cases, over the same
# home->calendars->objects tree. Both surfaces call the same evaluator, so
# running the matrix on each proves the wirings agree.
function mcp_list_nodes_query_walks_and_reverses { # @test
  # The configured root (the caldav home) is the query anchor.
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes '{}')"
  local rooturi
  rooturi="$(mcp_result_text "$output" 3 | jq -r '.[0].uri')"
  [[ -n $rooturi ]] || fail "no root uri: $(mcp_result_text "$output" 3)"

  # Forward walk: home -> calendars -> their objects. task1.ics (Personal) and
  # task3.ics (Work) both come back, wrapped as {query, nodes}, so the walk
  # descended BOTH discovered calendars.
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "$rooturi" '{uri:$u,query:"!caldav-calendar-v1 -> :"}')")"
  local text
  text="$(mcp_result_text "$output" 3)"
  echo "$text" | jq -e '.query == "!caldav-calendar-v1 -> :"' >/dev/null ||
    fail "query not echoed back: $text"
  echo "$text" | jq -e 'any(.nodes[]; .name=="task1.ics")' >/dev/null ||
    fail "walk missing task1.ics (Personal): $text"
  echo "$text" | jq -e 'any(.nodes[]; .name=="task3.ics")' >/dev/null ||
    fail "walk missing task3.ics (Work): $text"

  # Reverse `<-`: from the matched objects back up to the calendars that hold
  # them. Both calendars have objects, so both come back — the anchor-bounded
  # child-relation inversion, end to end through the mcp binary.
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "$rooturi" '{uri:$u,query:"!caldav-calendar-v1 -> : <- !caldav-calendar-v1"}')")"
  text="$(mcp_result_text "$output" 3)"
  echo "$text" | jq -e 'any(.nodes[]; .name=="Personal")' >/dev/null ||
    fail "reverse missing the Personal calendar: $text"
  echo "$text" | jq -e 'any(.nodes[]; .name=="Work")' >/dev/null ||
    fail "reverse missing the Work calendar: $text"
}

# The query param's guards over the mcp binary: mutually exclusive with filter,
# and an unsupported grammar form is a tool error — never a silent empty result.
function mcp_list_nodes_query_guards { # @test
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes '{}')"
  local rooturi
  rooturi="$(mcp_result_text "$output" 3 | jq -r '.[0].uri')"

  # query + filter is rejected (the two narrowing surfaces are exclusive).
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "$rooturi" '{uri:$u,query:"!caldav-calendar-v1",filter:"component=VTODO"}')")"
  assert_equal "$(mcp_is_error "$output" 3)" "true"

  # A typed combinator (deferred, cutting-garden#211) is a tool error, not a
  # silent empty listing.
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "$rooturi" '{uri:$u,query:"!caldav-calendar-v1 -[!x]-> :"}')")"
  assert_equal "$(mcp_is_error "$output" 3)" "true"
}

# cutting-garden#212 over the mcp binary: a facet predicate in a trellis query
# matches caldav objects (whose Facets are populated only via ListEnriched),
# and the matched nodes carry the facet inline — proving BOTH halves of the fix
# (matching + enrichment) end to end, the MCP-suite mirror of the CLI facet
# case. The VTODOs come back with their component facet; the VEVENT does not.
function mcp_list_nodes_query_facet_predicate { # @test
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes '{}')"
  local rooturi
  rooturi="$(mcp_result_text "$output" 3 | jq -r '.[0].uri')"

  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "$rooturi" '{uri:$u,query:"!caldav-calendar-v1 -> component=VTODO"}')")"
  local text
  text="$(mcp_result_text "$output" 3)"
  echo "$text" | jq -e 'any(.nodes[]; .name=="task1.ics")' >/dev/null ||
    fail "facet query missing task1.ics (a VTODO): $text"
  echo "$text" | jq -e 'any(.nodes[]; .name=="task3.ics")' >/dev/null ||
    fail "facet query missing task3.ics (a VTODO): $text"
  echo "$text" | jq -e 'all(.nodes[]; .name!="event1.ics")' >/dev/null ||
    fail "facet query leaked event1.ics (a VEVENT): $text"

  # The under-enrichment half of #212: matched nodes carry the component facet
  # inline (the enriched listing drove the match), so an agent needs no
  # follow-up read_node.
  echo "$text" | jq -e 'all(.nodes[]; .facets.component[0]=="VTODO")' >/dev/null ||
    fail "facet-query results not enriched inline: $text"
}

# cutting-garden#211 OR-alternatives over the mcp binary: [a, b] matches either
# alternative. VEVENTs window out of caldav's object listing (#176/#177), so
# component=VTODO is the satisfiable alternative; the VTODOs from both calendars
# come back — the MCP-suite mirror of the CLI OR case (the evaluator unit tests
# carry the both-alternatives-match semantics).
function mcp_list_nodes_query_or_alternatives { # @test
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes '{}')"
  local rooturi
  rooturi="$(mcp_result_text "$output" 3 | jq -r '.[0].uri')"

  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes \
    "$(jq -nc --arg u "$rooturi" '{uri:$u,query:"!caldav-calendar-v1 -> [component=VTODO, component=VEVENT]"}')")"
  local text
  text="$(mcp_result_text "$output" 3)"
  echo "$text" | jq -e 'any(.nodes[]; .name=="task1.ics")' >/dev/null ||
    fail "OR-alternatives missing task1.ics (a VTODO): $text"
  echo "$text" | jq -e 'any(.nodes[]; .name=="task3.ics")' >/dev/null ||
    fail "OR-alternatives missing task3.ics (a VTODO): $text"
}

# A read-only cache root must not crash the server at startup (#121). The
# Phase-B blob writer eagerly inits the madder store, which mkdir's
# <cache>/tmp-<pid>; on an unwritable cache that mkdir fails and madder
# Cancel-panics. Acquisition is isolated to its own context, so the failure
# degrades to structured-only reads — the server still comes up and serves.
function mcp_starts_on_readonly_cache { # @test
  local rocache="$BATS_TEST_TMPDIR/ro-cache"
  mkdir -p "$rocache"
  chmod 0500 "$rocache"

  # Force standard XDG-from-env (disable the cwd walk-up override) so the
  # cache resolves under our read-only XDG_CACHE_HOME, reproducing the
  # unwritable-cache mkdir the krone systemd unit hit.
  export XDG_CACHE_HOME="$rocache"
  export MADDER_XDG_USER_LOCATION_ONLY=1

  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 list_nodes '{}')"

  # Restore write so bats can clean the tmpdir.
  chmod -R u+w "$rocache"

  # The server served list_nodes (the configured root entry-point) rather
  # than crashing at startup. Post-0bec098, no-uri list_nodes returns the
  # root container, not its flattened calendars — asserting it came back at
  # all is the "server started" signal we need here.
  local roots
  roots="$(mcp_result_text "$output" 3)"
  echo "$roots" | jq -e \
    'length==1 and .[0].container==true and (.[0].uri | contains("/dav/"))' >/dev/null ||
    fail "server did not serve list_nodes on a read-only cache: $output"
}

# The isolation in #121 must not break the writer on the normal (writable)
# deployment: with a store configured, a leaf read still emits the raw-bytes
# madder://blobs link beside the parsed fields (the #85 Phase-B enrichment).
function mcp_emits_blob_link_with_writable_store { # @test
  init_store

  local obj read
  obj="$(caldav_object_uri task1.ics)"
  mcp_drive "$CALDAV_SOURCE" "$(tools_call 3 read_node "$(jq -nc --arg u "$obj" '{uri:$u}')")"
  read="$(mcp_result_text "$output" 3)"

  echo "$read" | grep -q '"summary": "Buy milk"' ||
    fail "read_node(task1) summary: $output"
  # renderContents appends 'raw bytes: madder://blobs/<digest> (...)' for the
  # link-only content entry when a store is configured.
  echo "$read" | grep -q 'raw bytes: madder://blobs/' ||
    fail "expected a madder://blobs raw-bytes link with a writable store: $output"
}

# create -> put -> patch -> delete round-trips one VEVENT through the server,
# each mutation a separate invocation against the persistent testserver.
function mcp_cud_round_trips { # @test
  local obj v1 v2
  obj="$(caldav_object_uri e2e.ics)"
  v1="$(printf 'BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:e2e\nSUMMARY:E2E v1\nEND:VEVENT\nEND:VCALENDAR\n')"
  v2="$(printf 'BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:e2e\nSUMMARY:E2E v2\nEND:VEVENT\nEND:VCALENDAR\n')"

  mcp_call create_node "$(jq -nc --arg u "$obj" --arg b "$v1" '{uri:$u,body:$b,type:"caldav-object-vevent-v1"}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"
  [[ "$(mcp_result_text "$output" 3)" == created* ]] || fail "create: $(mcp_result_text "$output" 3)"

  mcp_call put_node "$(jq -nc --arg u "$obj" --arg b "$v2" '{uri:$u,body:$b}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"
  [[ "$(mcp_result_text "$output" 3)" == put* ]] || fail "put: $(mcp_result_text "$output" 3)"

  mcp_call patch_node "$(jq -nc --arg u "$obj" '{uri:$u,body:"{\"component\":\"VEVENT\",\"event\":{\"summary\":\"E2E patched\"}}"}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"
  [[ "$(mcp_result_text "$output" 3)" == patched* ]] || fail "patch: $(mcp_result_text "$output" 3)"

  mcp_call delete_node "$(jq -nc --arg u "$obj" '{uri:$u}')"
  assert_equal "$(mcp_is_error "$output" 3)" "false"
  [[ "$(mcp_result_text "$output" 3)" == deleted* ]] || fail "delete: $(mcp_result_text "$output" 3)"
}

# create is strict: a second create on the same object is an error result,
# not a silent overwrite.
function mcp_create_is_strict { # @test
  local obj args
  obj="$(caldav_object_uri dup.ics)"
  args="$(jq -nc --arg u "$obj" \
    --arg b "$(printf 'BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:dup\nSUMMARY:dup\nEND:VEVENT\nEND:VCALENDAR\n')" \
    '{uri:$u,body:$b,type:"caldav-object-vevent-v1"}')"

  mcp_call create_node "$args"
  assert_equal "$(mcp_is_error "$output" 3)" "false"

  mcp_call create_node "$args"
  assert_equal "$(mcp_is_error "$output" 3)" "true"
}

# put on a missing object is strict too: an error result, not a create.
function mcp_put_missing_errors { # @test
  local obj args
  obj="$(caldav_object_uri ghost.ics)"
  args="$(jq -nc --arg u "$obj" \
    --arg b "$(printf 'BEGIN:VCALENDAR\nBEGIN:VTODO\nUID:ghost\nSUMMARY:ghost\nEND:VTODO\nEND:VCALENDAR\n')" \
    '{uri:$u,body:$b}')"

  mcp_call put_node "$args"
  assert_equal "$(mcp_is_error "$output" 3)" "true"
}

# The #102 PreToolUse hook (`cutting-garden hook`) classifies a CUD tool as
# `ask`, exercising the binary's hook subcommand end to end (not just the Go
# Run unit). Uses the prefix the hook assumes for cutting-garden's MCP tools.
function hook_gates_cud_tool_ask { # @test
  local eventfile
  eventfile="$BATS_TEST_TMPDIR/hook_event.json"
  echo '{"hook_event_name":"PreToolUse","tool_name":"mcp__plugin_cutting-garden_cutting-garden__create_node"}' >"$eventfile"

  run --separate-stderr timeout --preserve-status 5s "${CG_BIN}" hook <"$eventfile"
  assert_success

  assert_equal "$(echo "$output" | jq -r '.hookSpecificOutput.permissionDecision')" "ask"
}
