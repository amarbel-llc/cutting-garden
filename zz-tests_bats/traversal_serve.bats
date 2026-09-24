setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output stderr
  peer_pid=
}

teardown() {
  # Reap a peer a failed assertion left behind so the suite never hangs
  # on an orphaned accept loop.
  if [[ -n ${peer_pid:-} ]]; then
    kill "$peer_pid" 2>/dev/null || true
  fi
}

# bats file_tags=traversal_serve

# RFC 0013 §Conformance Testing — the portable half of the
# traversal-plugin transport gate. The suite is split in three sections:
#
#   PORTABLE — LAUNCH (bats test_tags=portable, names
#   test_portable_*, cookie/announce/rendezvous): pure RFC 0013
#   launch-contract cases every conformant `traversal-serve`
#   implementation MUST pass. A non-Go peer (e.g. a Rust fj-cg)
#   substitutes its own binary via CG_TEST_TRAVERSAL_SERVE (bats-emo
#   require_bin) and runs these unmodified.
#
#   PORTABLE — CONFORMANCE (test_portable_conformance_*): the
#   method-semantics half, driven by the compiled conformance DRIVER
#   binary (CG_CONFORMANCE_TRAVERSAL, cutting-garden#186) rather than a
#   shell JSON-RPC client. This is the "env var the flake sets" escape
#   the TESTPEER note below anticipated: the driver owns the AF_UNIX
#   session + half-close choreography a shell can't do well, so bats
#   only runs it and checks the TAP verdict. It drives the same
#   CG_TEST_TRAVERSAL_SERVE peer.
#
#   TESTPEER (test_tags=testpeer, names test_testpeer_*): the Go test
#   peer's fixed cgtest tree driven through the HOST — `cutting-garden`
#   with the peer configured as a [[traversal_plugins]] wire plugin —
#   as whole-document organize vectors (the RFC 0013 facet_writes
#   amendment: organize --apply writes through a wire plugin). They pin
#   the cgtest tree's content, so a substituted peer does not run them;
#   its wire-observable facet_writes obligations (declaration usable,
#   write:one move, write:many full-set replacement) are the portable
#   conformance-driver points 15-17 below, parameterized by the peer's
#   own manifest [facet_write] table. There is still no raw shell
#   JSON-RPC client: the driver and the Go indistinguishability
#   end-to-end (internal/traversal_serve_testpeer) own the method rows.
#
# Protocol facts pinned here (RFC 0013 §Launch and rendezvous):
# cookie env TRAVERSAL_PLUGIN_COOKIE; one stdout announce line
# `cookie|traversal-plugin/v1|unix|<socket>|<metadata>|traversal-plugin`;
# rendezvous socket lives in a fresh mode-0700 directory removed on
# exit; stdin EOF unblocks a pending accept and the peer exits 0.

announce_regexp() {
  local cookie="$1"
  printf '^%s\\|traversal-plugin/v1\\|unix\\|[^|]+\\|[^|]*\\|traversal-plugin$' \
    "$cookie"
}

# start_peer COOKIE launches the peer with stdin held open on a fifo
# (so it stays alive, listening) and waits — bounded — for the announce
# line. Sets: peer_pid, peer_out, peer_err, peer_stdin_fd, announce.
start_peer() {
  local cookie="$1"
  peer_out="$BATS_TEST_TMPDIR/peer.out"
  peer_err="$BATS_TEST_TMPDIR/peer.err"
  local fifo="$BATS_TEST_TMPDIR/peer.in"
  mkfifo "$fifo"

  # 3>&- : don't inherit bats' fd 3 (a held-open copy stalls the run).
  env TRAVERSAL_PLUGIN_COOKIE="$cookie" "$CG_TEST_TRAVERSAL_SERVE" \
    <"$fifo" >"$peer_out" 2>"$peer_err" 3>&- &
  peer_pid=$!

  # Opening the fifo write-side unblocks the peer's read-side open and
  # keeps its stdin from EOF until stop_peer closes this fd.
  exec {peer_stdin_fd}>"$fifo"

  for _ in {1..100}; do
    if [[ -s $peer_out ]]; then break; fi
    kill -0 "$peer_pid" 2>/dev/null ||
      fail "peer exited before announcing (stderr: $(cat "$peer_err"))"
    sleep 0.1
  done
  [[ -s $peer_out ]] || fail "no announce line within 10s"
  announce="$(head -n 1 "$peer_out")"
}

# stop_peer closes the held stdin (the RFC 0013 lifecycle EOF), then
# asserts the peer exits 0 promptly with no stdout past the announce.
stop_peer() {
  exec {peer_stdin_fd}>&-

  for _ in {1..100}; do
    kill -0 "$peer_pid" 2>/dev/null || break
    sleep 0.1
  done
  if kill -0 "$peer_pid" 2>/dev/null; then
    kill "$peer_pid" 2>/dev/null || true
    fail "peer did not exit within 10s of stdin EOF"
  fi

  local status=0
  wait "$peer_pid" || status=$?
  peer_pid=
  [[ $status -eq 0 ]] ||
    fail "peer exit status $status (stderr: $(cat "$peer_err"))"

  # stdout is protocol-only: nothing may follow the announce.
  [[ "$(cat "$peer_out")" == "$announce" ]] ||
    fail "stdout grew past the announce: $(cat "$peer_out")"
}

# ---------------------------------------------------------------------
# PORTABLE — any conformant traversal-serve binary must pass these.
# ---------------------------------------------------------------------

# bats test_tags=portable
function test_portable_refuses_to_start_without_cookie { # @test
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"

  run --separate-stderr "$CG_TEST_TRAVERSAL_SERVE" </dev/null
  assert_failure
  # The refusal must keep stdout protocol-silent; stderr MAY say
  # anything (deliberately unasserted for cross-implementation runs).
  [[ -z $output ]] || fail "stdout not empty on cookie refusal: $output"
}

# bats test_tags=portable
function test_portable_announce_is_wellformed_and_socket_live { # @test
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"

  local cookie=bats-trav-cookie-$RANDOM
  start_peer "$cookie"

  printf '%s' "$announce" | grep -qE "$(announce_regexp "$cookie")" ||
    fail "malformed announce: $announce"

  local sock
  sock="$(awk -F'|' '{print $4}' <<<"$announce")"
  [[ -S $sock ]] || fail "announced address is not a live socket: $sock"

  stop_peer
}

# bats test_tags=portable
function test_portable_stdin_eof_unblocks_accept_and_exits_zero { # @test
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"

  local cookie=bats-trav-cookie-$RANDOM
  # </dev/null is an immediate stdin EOF: the peer must still announce
  # first, then exit 0 without ever accepting a connection. The timeout
  # bounds "promptly"; --preserve-status turns a hang into a failure.
  run --separate-stderr timeout --preserve-status 10s \
    env TRAVERSAL_PLUGIN_COOKIE="$cookie" "$CG_TEST_TRAVERSAL_SERVE" \
    </dev/null
  assert_success
  # Exactly the one announce line on stdout, nothing after it.
  assert_output --regexp "$(announce_regexp "$cookie")"
}

# bats test_tags=portable
function test_portable_rendezvous_dir_0700_and_removed_on_exit { # @test
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"

  local cookie=bats-trav-cookie-$RANDOM
  start_peer "$cookie"

  local sock dir
  sock="$(awk -F'|' '{print $4}' <<<"$announce")"
  dir="$(dirname "$sock")"
  [[ "$(file_mode "$dir")" == 700 ]] ||
    fail "rendezvous dir $dir mode $(file_mode "$dir"), want 700"

  stop_peer
  [[ ! -e $dir ]] || fail "rendezvous dir survived exit: $dir"
}

# ---------------------------------------------------------------------
# TESTPEER — organize --apply through the wire (the RFC 0013
# facet_writes amendment). The peer declares state (write:one, field
# `state`, write buckets open/closed) and tag (write:many, field `tags`)
# on cgtest-obj-v1; the HOST builds the node.patch bodies
# ({"state":"open"}, {"tags":[...]}) from that declaration. Each test
# persists the peer's tree in its own CG_TESTPEER_STATE_FILE: a wire
# plugin is spawned per host process, so without it the read-back
# invocation would see a fresh tree. Peer stderr is kept out of the
# asserted stdout (--separate-stderr): it may carry diagnostics.
# ---------------------------------------------------------------------

# configure_testpeer_wire_plugin writes the host config naming the test
# peer as the cgtest wire plugin, points its state file into the test's
# tmpdir, and creates the blob store organize pins its bases in.
configure_testpeer_wire_plugin() {
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"

  mkdir -p "$HOME/.config/cutting-garden"
  cat >"$HOME/.config/cutting-garden/config.toml" <<EOF
[[traversal_plugins]]
name = "cgtest"
command = ["$CG_TEST_TRAVERSAL_SERVE"]
schemes = ["cgtest"]
EOF
  export CG_TESTPEER_STATE_FILE="$BATS_TEST_TMPDIR/cgtest-state.json"
  init_store
}

run_cg_stdout() {
  run --separate-stderr timeout --preserve-status 10s \
    "${CG_BIN:-cutting-garden}" "$@"
}

# bats test_tags=testpeer
function test_testpeer_organize_write_one_move_round_trips { # @test
  configure_testpeer_wire_plugin

  run_cg_stdout organize -group-by state= -query '!cgtest-obj-v1' \
    cgtest://fixture/box
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by state= -query "!cgtest-obj-v1" cgtest://fixture/box`
	- _base = @blake2b256-hm97mtg5cv92gyd75hu9xc5f6nszdl9d6hrmc833p5shqyzwm9rs0nweyx
	- _anchor = cgtest://fixture/box/
	- _query = !cgtest-obj-v1
	- _type = !cgtest-obj-v1
	! organize-base-v1
	---

	# state=

	## =open

	- [alpha] alpha

	## =closed

	- [beta] beta
	EOM

  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by state= -query "!cgtest-obj-v1" cgtest://fixture/box`
	- _base = @blake2b256-hm97mtg5cv92gyd75hu9xc5f6nszdl9d6hrmc833p5shqyzwm9rs0nweyx
	- _anchor = cgtest://fixture/box/
	- _query = !cgtest-obj-v1
	- _type = !cgtest-obj-v1
	! organize-base-v1
	---

	# state=

	## =open

	- [alpha] alpha
	- [beta] beta

	## =closed
	EOM

  run_cg_stdout organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [beta state=[-closed-]{+open+}] beta

organize: wrote 1 change(s)
EOF

  # Read back through a second host process (a fresh peer over the
  # persisted tree): no cgtest-obj-v1 is closed any more, and the
  # re-rendered document files beta under =open.
  run_cg_stdout list -format json -query '!cgtest-obj-v1 state=closed' \
    cgtest://fixture/box
  assert_success
  assert_output ''

  run_cg_stdout organize -group-by state= -query '!cgtest-obj-v1' \
    cgtest://fixture/box
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by state= -query "!cgtest-obj-v1" cgtest://fixture/box`
	- _base = @blake2b256-6kt5f85907k0fyk7a75fdw6vpk2vu4quae66hve6j4as38ptnezs7w65h5
	- _anchor = cgtest://fixture/box/
	- _query = !cgtest-obj-v1
	- _type = !cgtest-obj-v1
	! organize-base-v1
	---

	# state=

	## =open

	- [alpha] alpha
	- [beta] beta

	## =closed
	EOM
}

# bats test_tags=testpeer
function test_testpeer_organize_write_many_replaces_membership { # @test
  configure_testpeer_wire_plugin

  run_cg_stdout organize -group-by tag= -query '!cgtest-obj-v1' \
    cgtest://fixture/box
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by tag= -query "!cgtest-obj-v1" cgtest://fixture/box`
	- _base = @blake2b256-76lu3m9s600nzdat77ydekykh8tsq99k0n2tep0a8fmx9zkcrljqejg34n
	- _anchor = cgtest://fixture/box/
	- _query = !cgtest-obj-v1
	- _type = !cgtest-obj-v1
	! organize-base-v1
	---

	# tag=

	## =a

	- [alpha] alpha

	## =b

	- [alpha] alpha
	- [beta] beta
	EOM

  # Renaming the =a bucket to =c re-files alpha's `a` membership as `c`:
  # the host sends alpha's COMPLETE new set, {"tags":["b","c"]}.
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by tag= -query "!cgtest-obj-v1" cgtest://fixture/box`
	- _base = @blake2b256-76lu3m9s600nzdat77ydekykh8tsq99k0n2tep0a8fmx9zkcrljqejg34n
	- _anchor = cgtest://fixture/box/
	- _query = !cgtest-obj-v1
	- _type = !cgtest-obj-v1
	! organize-base-v1
	---

	# tag=

	## =c

	- [alpha] alpha

	## =b

	- [alpha] alpha
	- [beta] beta
	EOM

  run_cg_stdout organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [alpha [-a-] {+c+}] alpha

organize: wrote 1 change(s)
EOF

  run_cg_stdout list -format json -query '!cgtest-obj-v1 tag=c' \
    cgtest://fixture/box
  assert_success
  assert_output '{"uri":"cgtest://fixture/box/alpha","name":"alpha","type":"cgtest-obj-v1"}'

  run_cg_stdout list -format json -query '!cgtest-obj-v1 tag=a' \
    cgtest://fixture/box
  assert_success
  assert_output ''

  run_cg_stdout organize -group-by tag= -query '!cgtest-obj-v1' \
    cgtest://fixture/box
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by tag= -query "!cgtest-obj-v1" cgtest://fixture/box`
	- _base = @blake2b256-zjmd63ug8x5r8c2ren3uraayw9c6mx790ujq3c8eh20ndupt078sffu7jj
	- _anchor = cgtest://fixture/box/
	- _query = !cgtest-obj-v1
	- _type = !cgtest-obj-v1
	! organize-base-v1
	---

	# tag=

	## =b

	- [alpha] alpha
	- [beta] beta

	## =c

	- [alpha] alpha
	EOM
}

# A facet_writes block naming a dimension the peer's facets block never
# declares is unusable — the host could not build a patch for it — so
# bring-up fails loudly, naming the plugin, type and dimension.
# bats test_tags=testpeer
function test_testpeer_undeclared_facet_write_fails_initialize { # @test
  configure_testpeer_wire_plugin

  export CG_TESTPEER_UNDECLARED_FACET_WRITE=1
  run_cg_stdout list cgtest://fixture/box
  assert_failure
  [[ $stderr == *'wire plugin "cgtest": initialize rejected: facet write: type "cgtest-obj-v1" dimension "no-such-dimension" is not a declared facet dimension'* ]] ||
    fail "stderr does not carry the rejection: $stderr"
}

# ---------------------------------------------------------------------
# TRACKER — the forge organize plan's stream-2 RFC 0013 additions, over
# the peer's forge-issue-shaped cgtest://fixture/tracker (not a root, so
# the box vectors above are untouched): cgtest-ticket-v1 declares state
# (write:one), milestone (write:one, CLEARABLE) and label (write:many).
# ---------------------------------------------------------------------

# A clearable write:one dimension (forge organize F12): moving a ticket
# into the no-value section above the `# milestone=` heading clears its
# milestone — the host sends {"milestone": null} — and the re-rendered
# document keeps it there.
# bats test_tags=testpeer
function test_testpeer_tracker_clearable_move_clears_milestone { # @test
  configure_testpeer_wire_plugin

  run_cg_stdout organize -group-by milestone= -query '!cgtest-ticket-v1' \
    cgtest://fixture/tracker
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by milestone= -query "!cgtest-ticket-v1" cgtest://fixture/tracker`
	- _base = @blake2b256-7dxw2wzpvndlv8gf78nr9h9mx0gyg9aulyuq2k6xn376mjszkvhqvpvj2u
	- _anchor = cgtest://fixture/tracker/
	- _query = !cgtest-ticket-v1
	- _type = !cgtest-ticket-v1
	! organize-base-v1
	---

	- [2 "good first issue"] Write the docs

	# milestone=

	## =v0.1

	- [1 area-organize bug] Fix the parser

	## =v0.2

	- [3 area-organize-apply] Ship it
	EOM

  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by milestone= -query "!cgtest-ticket-v1" cgtest://fixture/tracker`
	- _base = @blake2b256-7dxw2wzpvndlv8gf78nr9h9mx0gyg9aulyuq2k6xn376mjszkvhqvpvj2u
	- _anchor = cgtest://fixture/tracker/
	- _query = !cgtest-ticket-v1
	- _type = !cgtest-ticket-v1
	! organize-base-v1
	---

	- [1 area-organize bug] Fix the parser
	- [2 "good first issue"] Write the docs

	# milestone=

	## =v0.1

	## =v0.2

	- [3 area-organize-apply] Ship it
	EOM

  run_cg_stdout organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [1 area-organize bug milestone=[-v0.1-]] Fix the parser

organize: wrote 1 change(s)
EOF

  run_cg_stdout list -format json -query 'milestone=v0.1' cgtest://fixture/tracker
  assert_success
  assert_output ''

  run_cg_stdout organize -group-by milestone= -query '!cgtest-ticket-v1' \
    cgtest://fixture/tracker
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by milestone= -query "!cgtest-ticket-v1" cgtest://fixture/tracker`
	- _base = @blake2b256-ag7nxtghg6kt8mu2clg2dtecf2ucah9a77s0vqm9x807pjm7r72qzhvdnq
	- _anchor = cgtest://fixture/tracker/
	- _query = !cgtest-ticket-v1
	- _type = !cgtest-ticket-v1
	! organize-base-v1
	---

	- [1 area-organize bug] Fix the parser
	- [2 "good first issue"] Write the docs

	# milestone=

	## =v0.1

	## =v0.2

	- [3 area-organize-apply] Ship it
	EOM
}

# A write:one dimension NOT declared clearable refuses a move into the
# no-value section up front — before the diff — saying it cannot be
# cleared, and writes nothing.
# bats test_tags=testpeer
function test_testpeer_tracker_non_clearable_move_is_refused { # @test
  configure_testpeer_wire_plugin

  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by state= -query "!cgtest-ticket-v1" cgtest://fixture/tracker`
	- _base = @blake2b256-2h4m064n6078wven96m22a5n3vfywruxuypn09hugc348x6yevmqs4lmpv
	- _anchor = cgtest://fixture/tracker/
	- _query = !cgtest-ticket-v1
	- _type = !cgtest-ticket-v1
	! organize-base-v1
	---

	- [3 area-organize-apply] Ship it

	# state=

	## =open

	- [1 area-organize bug] Fix the parser
	- [2 "good first issue"] Write the docs

	## =closed
	EOM

  # Generate first so the pinned base blob exists in the store.
  run_cg_stdout organize -group-by state= -query '!cgtest-ticket-v1' \
    cgtest://fixture/tracker
  assert_success

  run_cg_stdout organize -apply "$edited" -commit
  assert_failure
  [[ $stderr == *'organize: dimension "state" cannot be cleared for type "cgtest-ticket-v1" — cgtest://fixture/tracker/3 was moved out of =closed into the no-value section; file it under a bucket instead'* ]] ||
    fail "stderr does not carry the refusal: $stderr"

  run_cg_stdout list -format json -query 'state=closed' cgtest://fixture/tracker
  assert_success
  assert_output '{"uri":"cgtest://fixture/tracker/3","name":"Ship it","type":"cgtest-ticket-v1","tags":["area-organize-apply"]}'
}

# A tag_set over the wire (forge organize F11): the ticket type's label
# dimension is its tag set under dodder-hyphen, so every box renders its
# labels as key-free tag atoms in SortKey order (a label with a space as a
# quoted atom), and `list -format json` carries the `tags` array. Editing
# the atoms — `bug` removed, `"needs review"` added — is a membership write
# through the declared write:many: the host sends ticket 1's complete new
# set, {"labels": ["area-organize", "needs review"]}.
# bats test_tags=testpeer
function test_testpeer_tracker_tag_atoms_render_and_edit { # @test
  configure_testpeer_wire_plugin

  run_cg_stdout list -format json cgtest://fixture/tracker
  assert_success
  assert_output - <<'EOF'
{"uri":"cgtest://fixture/tracker/1","name":"Fix the parser","type":"cgtest-ticket-v1","tags":["area-organize","bug"]}
{"uri":"cgtest://fixture/tracker/2","name":"Write the docs","type":"cgtest-ticket-v1","tags":["good first issue"]}
{"uri":"cgtest://fixture/tracker/3","name":"Ship it","type":"cgtest-ticket-v1","tags":["area-organize-apply"]}
EOF

  run_cg_stdout organize -group-by milestone= -query '!cgtest-ticket-v1' \
    cgtest://fixture/tracker
  assert_success

  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by milestone= -query "!cgtest-ticket-v1" cgtest://fixture/tracker`
	- _base = @blake2b256-7dxw2wzpvndlv8gf78nr9h9mx0gyg9aulyuq2k6xn376mjszkvhqvpvj2u
	- _anchor = cgtest://fixture/tracker/
	- _query = !cgtest-ticket-v1
	- _type = !cgtest-ticket-v1
	! organize-base-v1
	---

	- [2 "good first issue"] Write the docs

	# milestone=

	## =v0.1

	- [1 area-organize "needs review"] Fix the parser

	## =v0.2

	- [3 area-organize-apply] Ship it
	EOM

  run_cg_stdout organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [1 area-organize [-bug-] {+"needs review"+}] Fix the parser

organize: wrote 1 change(s)
EOF

  run_cg_stdout list -format json -query '!cgtest-ticket-v1 bug' cgtest://fixture/tracker
  assert_success
  assert_output ''

  run_cg_stdout organize -group-by milestone= -query '!cgtest-ticket-v1' \
    cgtest://fixture/tracker
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by milestone= -query "!cgtest-ticket-v1" cgtest://fixture/tracker`
	- _base = @blake2b256-dema2q3j3n3gl7dawvaycx8vqjgxt784d399eysx5sgrd5tqg7mqg0zdn2
	- _anchor = cgtest://fixture/tracker/
	- _query = !cgtest-ticket-v1
	- _type = !cgtest-ticket-v1
	! organize-base-v1
	---

	- [2 "good first issue"] Write the docs

	# milestone=

	## =v0.1

	- [1 area-organize "needs review"] Fix the parser

	## =v0.2

	- [3 area-organize-apply] Ship it
	EOM
}

# The tag set groups like a linked plugin's: `(tags)` hoists one bare
# heading per label (placement tags stripped from the boxes), and a
# dodder-hyphen namespace grouping rolls area-organize and
# area-organize-apply up under `# area` / `## -organize`. Moving ticket 2
# from `"good first issue"` under `# bug` is a membership write.
# bats test_tags=testpeer
function test_testpeer_tracker_tag_groupings { # @test
  configure_testpeer_wire_plugin

  run_cg_stdout organize -group-by area -query '!cgtest-ticket-v1' \
    cgtest://fixture/tracker
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by area -query "!cgtest-ticket-v1" cgtest://fixture/tracker`
	- _base = @blake2b256-dx6ylxadedgatw9dtufqvk30d39804ykdcfh2tpc2m5nlsdzr6hsd09p7r
	- _anchor = cgtest://fixture/tracker/
	- _query = !cgtest-ticket-v1
	- _type = !cgtest-ticket-v1
	- _group-by = area
	! organize-base-v1
	---

	- [2 "good first issue"] Write the docs

	# area

	## -organize

	- [1 bug] Fix the parser
	- [3] Ship it
	EOM

  run_cg_stdout organize -group-by '(tags)' -query '!cgtest-ticket-v1' \
    cgtest://fixture/tracker
  assert_success
  assert_output - <<-'EOM'
	---
	% generated: `cg organize -group-by (tags) -query "!cgtest-ticket-v1" cgtest://fixture/tracker`
	- _base = @blake2b256-a25kmqtu2wy808awgfrluyzmgh6t0x2y9p2cfwcszmffeu4ef3jqs7ee6a
	- _anchor = cgtest://fixture/tracker/
	- _query = !cgtest-ticket-v1
	- _type = !cgtest-ticket-v1
	- _group-by = (tags)
	! organize-base-v1
	---

	# area-organize

	- [1 bug] Fix the parser

	# area-organize-apply

	- [3] Ship it

	# bug

	- [1 area-organize] Fix the parser

	# "good first issue"

	- [2] Write the docs
	EOM

  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize -group-by (tags) -query "!cgtest-ticket-v1" cgtest://fixture/tracker`
	- _base = @blake2b256-a25kmqtu2wy808awgfrluyzmgh6t0x2y9p2cfwcszmffeu4ef3jqs7ee6a
	- _anchor = cgtest://fixture/tracker/
	- _query = !cgtest-ticket-v1
	- _type = !cgtest-ticket-v1
	- _group-by = (tags)
	! organize-base-v1
	---

	# area-organize

	- [1 bug] Fix the parser

	# area-organize-apply

	- [3] Ship it

	# bug

	- [1 area-organize] Fix the parser
	- [2] Write the docs

	# "good first issue"
	EOM

  run_cg_stdout organize -apply "$edited" -commit
  assert_success
  assert_output - <<'EOF'
organize: 1 change(s):

  - [2 {+bug+} [-"good first issue"-]] Write the docs

organize: wrote 1 change(s)
EOF

  run_cg_stdout list -format json -query '!cgtest-ticket-v1 bug' cgtest://fixture/tracker
  assert_success
  assert_output - <<'EOF'
{"uri":"cgtest://fixture/tracker/1","name":"Fix the parser","type":"cgtest-ticket-v1","tags":["area-organize","bug"]}
{"uri":"cgtest://fixture/tracker/2","name":"Write the docs","type":"cgtest-ticket-v1","tags":["bug"]}
EOF
}

# describe_node_types reports the wire type's tag_set exactly as a linked
# plugin's (the field is the tag-set dimension, the interpreter its
# declared one) and the milestone dimension's clearable one write; the mcp
# enriched listing carries each ticket's `tags`.
# bats test_tags=testpeer
function test_testpeer_tracker_mcp_describes_tag_set { # @test
  configure_testpeer_wire_plugin
  load "$(dirname "$BATS_TEST_FILE")/lib/mcp.bash"

  mcp_drive cgtest://fixture/tracker \
    "$(tools_call 3 describe_node_types '{}')" \
    "$(tools_call 4 list_nodes '{"uri":"cgtest://fixture/tracker"}')"
  local frames="$output"

  output="$(mcp_result_text "$frames" 3 | jq -c '
    .[].types[] | select(.tag == "cgtest-ticket-v1")
    | {tag_set, milestone: (.facets[] | select(.key == "milestone")
        | {writeMode, field, clearable})}')" ||
    fail "describe_node_types did not parse: $frames"
  assert_output '{"tag_set":{"field":"label","interpreter":"dodder-hyphen"},"milestone":{"writeMode":"one","field":"milestone","clearable":true}}'

  output="$(mcp_result_text "$frames" 4 | jq -c '.nodes[] | {uri, tags}')" ||
    fail "list_nodes did not parse: $frames"
  assert_output - <<'EOF'
{"uri":"cgtest://fixture/tracker/1","tags":["area-organize","bug"]}
{"uri":"cgtest://fixture/tracker/2","tags":["good first issue"]}
{"uri":"cgtest://fixture/tracker/3","tags":["area-organize-apply"]}
EOF
}

# ---------------------------------------------------------------------
# CONFORMANCE — session-level METHOD SEMANTICS, driven by the driver
# BINARY (cutting-garden#186). This is the "shell-honest tree case" the
# file banner anticipated: rather than a brittle shell JSON-RPC client,
# a compiled driver (CG_CONFORMANCE_TRAVERSAL) speaks the whole session
# and emits TAP, so bats only runs it and checks the verdict. It drives
# the SAME CG_TEST_TRAVERSAL_SERVE peer the launch cases use — the two
# together are the full lane a substituted non-Go peer runs (that peer
# supplies its own manifest; this case pins the in-tree testpeer).
# ---------------------------------------------------------------------

# bats test_tags=portable
function test_portable_conformance_driver_passes_testpeer { # @test
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"
  require_bin CG_CONFORMANCE_TRAVERSAL cutting-garden-conformance-traversal ||
    skip "cutting-garden-conformance-traversal not available in this lane"

  # The testpeer's manifest. command is the ONLY runtime-dependent field
  # (the peer's built path); the rest is the fixed cgtest tree's shape.
  # patch_unrecognized_only.body is empty on purpose: the testpeer merges
  # every patch key, so no unrecognized field is constructible and the
  # driver SKIPs that point rather than faking it.
  local manifest="$BATS_TEST_TMPDIR/testpeer.manifest.toml"
  cat >"$manifest" <<EOF
command = ["$CG_TEST_TRAVERSAL_SERVE"]
schemes = ["cgtest"]
writable_container = "cgtest://fixture/box"

[create]
type = "cgtest-obj-v1"
body = "conformance probe body"

[patch_recognized]
body = "{\"note\":\"patched\"}"
expect_applied = ["note"]

[patch_unrecognized_only]
body = ""

[patch_wrong_typed]
body = "not json"

[facet_container]
uri = "cgtest://fixture/box"
filter = "state=open"

[container_body]
uri = "cgtest://fixture/box/issue-1"

[bulk_mutate]
container = "cgtest://fixture/box"
create_type = "cgtest-obj-v1"
create_body = "bulk probe body"

[facet_write]
container = "cgtest://fixture/box"
node = "cgtest://fixture/box/beta"
one_dimension = "state"
one_bucket = "open"
many_dimension = "tag"
many_set = ["x", "y"]

[facet_clear]
container = "cgtest://fixture/tracker"
node = "cgtest://fixture/tracker/1"
dimension = "milestone"
EOF

  run --separate-stderr "$CG_CONFORMANCE_TRAVERSAL" --manifest "$manifest"
  assert_success
  # Substring, not --regexp with ^: bats matches a regex against the
  # whole multi-line output as ONE string (no per-line/multiline flag),
  # so `^not ok` would never match a mid-output failure — a false-safe
  # assertion. --partial 'not ok' catches a failing point anywhere.
  assert_output --partial '1..19'
  assert_output --partial 'ok 1 - initialize'
  assert_output --partial 'ok 11 - leaf.read: container returns its own body'
  assert_output --partial 'ok 13 - nodes.list: filter pushdown returns a sound subset'
  assert_output --partial 'ok 14 - node.bulk_mutate: best-effort applies'
  # The RFC 0013 facet_writes amendment: a peer declaring facet_writes
  # must accept the host-built node.patch bodies (a substituted peer
  # supplies its own [facet_write] node and dimensions; one declaring no
  # facet_writes SKIPs these).
  assert_output --partial 'ok 15 - initialize: facet_writes declaration is usable by the host'
  assert_output --partial 'ok 16 - node.patch: host-built write:one body moves the node into the bucket'
  assert_output --partial "ok 17 - node.patch: host-built write:many body replaces the node's set, [] clears"
  # A clearable write:one dimension (forge organize F12) accepts the
  # host-built {"<field>": null}; a substituted peer names its own
  # [facet_clear] node, or SKIPs by omitting the table.
  assert_output --partial 'ok 18 - node.patch: host-built clear body (null) empties a clearable write:one dimension'
  # The presentation additions (forge organize F11): a peer carrying a
  # tag_set on a node_types entry must pass the host's bring-up check.
  assert_output --partial 'ok 19 - initialize: node_types presentation members are usable by the host'
  refute_output --partial 'not ok'
}

# bats test_tags=portable
function test_portable_conformance_driver_fails_a_wrong_expectation { # @test
  # The driver's own acceptance property: it MUST be able to fail. A
  # manifest asserting an applied set the peer will not report has to
  # produce a non-ok point and a nonzero exit — a driver that passes
  # everything ratifies nothing (cutting-garden#186).
  require_bin CG_TEST_TRAVERSAL_SERVE cutting-garden-test-traversal-serve ||
    skip "cutting-garden-test-traversal-serve not available in this lane"
  require_bin CG_CONFORMANCE_TRAVERSAL cutting-garden-conformance-traversal ||
    skip "cutting-garden-conformance-traversal not available in this lane"

  local manifest="$BATS_TEST_TMPDIR/wrong.manifest.toml"
  cat >"$manifest" <<EOF
command = ["$CG_TEST_TRAVERSAL_SERVE"]
schemes = ["cgtest"]
writable_container = "cgtest://fixture/box"

[create]
type = "cgtest-obj-v1"
body = "conformance probe body"

[patch_recognized]
body = "{\"note\":\"patched\"}"
expect_applied = ["this-key-is-never-reported"]

[patch_wrong_typed]
body = "not json"
EOF

  run --separate-stderr "$CG_CONFORMANCE_TRAVERSAL" --manifest "$manifest"
  assert_failure
  assert_output --partial \
    'not ok 5 - node.patch: recognized fields reported applied'
}
