---
status: exploring
date: 2026-06-07
promotion-criteria: |
  Promote to `proposed` once (a) the capture-posture fork
  (§Capture posture) is settled, (b) the v1 auth menu is blessed,
  and (c) the load-bearing items in §Verification checklist are
  resolved — export byte-stability foremost, since the export-bytes
  posture is dead on arrival if re-exports of unchanged documents
  are not byte-stable.
---

# google-drive plugin

> **Exploration only.** No code exists. This FDR captures a design
> exploration (2026-06-07); unlike FDR 0007 (keepassxc, the design-only
> precedent), the central capture-posture decision is *not yet made*,
> hence `exploring` rather than `proposed`. The git plugin (FDR 0006)
> is the mechanical template throughout. Claims marked ⚠ were not
> verified during the exploration and are collected in
> §Verification checklist; everything in §Auth marked "verified" was
> checked against the live Google docs on 2026-06-07.

## Problem Statement

Cutting-garden captures filesystem trees (FDR 0001), yt-dlp media
(FDR 0003), and git remotes (FDR 0006). Documents living in Google
Drive — Docs, Sheets, Slides, Forms — sit outside all three surfaces.
The situation is harsher than the keepassxc case: a native Google file
has **no canonical bytes at all**. You cannot download "the doc"; you
can only export it to a chosen format (`files.export` → docx/pdf/odt,
xlsx/csv, pptx, …) or fetch its logical structure as JSON via the
per-product APIs (`documents.get`, `spreadsheets.get`, `forms.get`).
Forms isn't exportable through Drive at all. Even the Drive-for-desktop
mount exposes native files as `.gdoc`/`.gsheet` stubs, so the file
plugin cannot capture their content from a synced tree.

## Interface

### Where it slots in (the mechanical part)

The extension surface is paved and this part is mostly settled:

- **Scheme claim** — peer-leaf package
  `internal/cutting_garden_plugin_gdrive/` claiming the single
  `gdrive` scheme via the FDR 0005 registries. No bare
  `https://docs.google.com/...` pass-through in v1: claiming `https`
  collides with ytdlp at init-time, and the FDR 0003 host-router layer
  does not exist yet. Whether the opaque form accepts full Drive URLs
  (normalized to file/folder IDs) is open.
- **Protocol capture** — implement `ProtocolCapturePlugin` and emit an
  RFC 0002 receipt merkle tree; the generic nodes come from
  `internal/capture_plugin` for free. The vestigial EntryV1 stubs are
  required for scheme resolution (the #48 wart, as documented by the
  git plugin).
- **Binding RFC** — a future `docs/rfcs/000X-gdrive-archive-binding.md`
  pins the node type-strings (`gdrive-capture-file-v1`,
  `gdrive-capture-folder-v1`, `jcs-gdrive-capture-environment-v1`, …),
  mirroring RFC 0004.
- **Deps** — `google.golang.org/api/drive/v3` (plus `forms/v1`,
  `docs/v1`, `sheets/v4` depending on posture) and an auth library
  (`golang.org/x/oauth2` or the newer `cloud.google.com/go/auth`).
  Non-bridged → `go get` + `gomod2nix generate`. Pure Go, no runtime
  binary; the exec fallback (FDR 0003 shape) would be rclone.

### Capture posture — the central fork (unsettled)

Because native files have no canonical bytes, the plugin must pick a
representation. This is the decision that gates promotion to
`proposed`:

1. **Export-bytes posture (ytdlp-like).** `files.export` each native
   file to one or more formats; RFC 0002's `captures[].format` field
   models the format set directly (one target, multiple capture
   formats). Simple and produces human-usable artifacts. Risks:
   export byte-stability (⚠ docx/xlsx/pptx are zips and re-exports of
   unchanged docs are suspected non-byte-identical — timestamps,
   generated IDs — which would kill dedup and diff without a
   `normalize` pass), the ⚠ believed ~10 MB `files.export` size limit,
   and Forms simply has no Drive export.
2. **Structural posture (kdbx-like).** Fetch the per-product JSON
   structure and mirror that logical tree into madder as
   content-addressed nodes. Per-element dedup, semantic diff
   ("paragraph 12 changed", "form question added"), and the only
   honest representation of a Form. Costs: one binding schema per
   product, restore becomes lossy `batchUpdate` reconstruction, and
   the JSON shapes are large and Google-versioned.
3. **Hybrid.** Structural tree for the merkle/diff value plus an
   export-bytes payload as the human-usable artifact.

Non-native files (uploaded PDFs, images, arbitrary blobs) have real
bytes (`files.get?alt=media`) and are posture-independent leaves.

### Tree shape and diff

- A folder capture mirrors the hierarchy as a merkle tree: folder
  nodes referencing children (the kdbx group-node analog), file
  leaves. Drive file IDs are stable, so diff can be **semantic and
  ID-keyed** like the kdbx plugin's UUID join: renames, moves between
  folders, and trashing are distinguishable from add+delete.
- **Cheap freshness probe** (the `git ls-remote` / ytdlp-info.json
  analog): `files.get fields=version,modifiedTime,headRevisionId` per
  file; for folder trees the **Changes API** (`changes.list` against a
  page token stored in the receipt's plugin-outcome node) gives true
  incremental re-capture — arguably a better primitive than either
  existing plugin has.
- An expired/revoked credential must surface as a distinct
  "auth failed" error kind so a dead token never masquerades as
  drift (or as its absence).

### Restore

Re-uploading an export creates a *converted copy* — never
byte-identical, with revision history, comments, and sharing lost.
Lean: follow the ytdlp restore-deferral. Restore-to-filesystem of the
export artifacts already works through the file plugin;
restore-to-Drive is a future FDR with loud caveats.

## Auth — the dominant design cost

Auth is not an implementation detail here; it dominated the
exploration. Summary of the friction map, ordered by severity.

### Structural blocker: scope classification economics

- There is no anonymous access: every OAuth client needs a Google
  Cloud project with the Drive API enabled.
- The scopes the plugin needs — `drive.readonly` or `drive` — are
  classified **restricted** (Google's highest tier). Production use
  requires OAuth verification including an ⚠ annual third-party
  security assessment (CASA) costing real money — a non-starter for a
  personal archival CLI, which kills the
  "embedded client ID in the binary, published app" path.
- The non-restricted `drive.file` scope only reaches files the app
  created or the user picked via Google's Picker UI — useless for
  "capture this existing folder" from a CLI.
- Staying unpublished doesn't escape cleanly: **Testing** status caps
  at 100 test users and ⚠ issues refresh tokens that expire after
  7 days (external-type consent screens with sensitive/restricted
  scopes) — weekly re-consent, fatal for unattended re-capture.
- The escape that works today is the **rclone model**: each user
  creates their own GCP project + OAuth client. rclone's
  "making your own client_id" doc is the canonical writeup of this
  pain.

### CLI flow mechanics

- The copy/paste **OOB flow is dead** (deprecated 2022, shut off
  2023). Remaining: **loopback redirect** (localhost callback server +
  browser) or the device flow — and ⚠ the device flow's scope
  allowlist is believed to exclude Drive scopes, which would leave
  loopback as the only path.
- Loopback is painful on **headless/remote machines** (the browser
  lives elsewhere). gcloud's `--no-browser` remote-bootstrap and
  `--no-launch-browser` copy/paste dances are the proven templates.

### ADC findings (verified against live docs, 2026-06-07)

Application Default Credentials is a credential *discovery
convention*, not a credential type: libraries search
`GOOGLE_APPLICATION_CREDENTIALS` → the well-known file
(`~/.config/gcloud/application_default_credentials.json`, written by
`gcloud auth application-default login`) → the metadata server.
`drive.NewService(ctx)` picks all of it up with near-zero auth code in
the plugin.

The headline finding: **Drive scopes do not ride gcloud's own client
ID.** The `--scopes` flag doc says verbatim: *"To add scopes for
applications outside of Google Cloud Platform, such as Google Drive,
create an OAuth Client ID and provide it by using the
--client-id-file flag."* The two sanctioned routes:

1. `--client-id-file=<your-client>.json --scopes=…drive.readonly` —
   i.e. BYO GCP project + OAuth client after all. ADC is then *not* a
   verification-economics escape, just flow + storage plumbing around
   your client (Testing-status token expiry still applies).
2. `--impersonate-service-account=<sa> --scopes=…drive.readonly` — no
   OAuth client and no consent screen at all. Costs move elsewhere:
   a GCP project for the SA, `iam.serviceAccountTokenCreator` for the
   user, and the SA only sees files explicitly shared with it.

⚠ Not verified: whether a Drive scope *without* `--client-id-file` is
hard-rejected today or merely unsupported-but-functional (it worked
historically; the docs' wording suggests it's now blocked). A
30-second empirical test.

What ADC still buys, honestly: headless flow plumbing
(`--no-browser` / `--no-launch-browser`), token cache + refresh owned
by gcloud and the client libraries (no `creds.go` token-lifecycle
code, no cutting-garden-owned credential file), and defined
quota-project semantics. ADC-specific friction the plugin must
absorb: **one global ADC file, last-writer-wins** ("Any credentials
previously generated … will be overwritten") — a plain ADC login for
GCP work clobbers the Drive-scoped credential, so the plugin needs a
preflight distinguishing no-ADC / ADC-without-Drive-scope /
ADC-expired, printing the exact re-login command; scopes are baked at
login time; ⚠ Go library support for the
`impersonated_service_account` ADC file type needs a version check.

### The corp-IT / Workspace axis

Asked directly: which approach has the most access to user-visible
resources *and* is least likely to be blocked by corp IT?

- **Access axis has a clear winner:** only 3-legged OAuth as the user
  with a broad Drive scope sees everything the user sees (My Drive,
  shared drives, shared-with-me). Every SA variant sees only what's
  shared to it; domain-wide delegation *is* a corp-IT grant by
  definition.
- **Blockability is about whose client ID,** not which flow: Workspace
  App Access Control keys on OAuth client ID + scope class, and
  restricted-class Drive scopes are where policies bite hardest.
  Ranking: Google first-party clients (never blocked, but unavailable
  — Google blocks its own gcloud client from Drive scopes, and
  borrowing another app's client ID is illegitimate) → **an
  "Internal" OAuth client created inside the user's own Workspace
  org** (skips Google verification entirely — no CASA, no
  Testing-mode expiry, no 100-user cap — and ⚠ is trusted by default
  by Workspace app-access settings; requires permission to create a
  GCP project in the org) → a verified published external client →
  an unverified external client (the exact profile admin policies are
  built to stop).
- Corp gotchas that bite even when allowed: ⚠ Workspace session-length
  policies may force periodic re-auth, silently killing unattended
  re-capture (whether they bind a given internal client's refresh
  tokens needs verification); the SA path is extra-blocked in corp
  (external-sharing policies block sharing to a non-domain SA email;
  `iam.disableServiceAccountKeyCreation` is near-standard).
- The sidesteps fail: Takeout is frequently admin-disabled; the
  Drive-for-desktop mount has no native-doc bytes.

### v1 credential menu (lean)

Support **ADC as the first credential source** — it makes two of the
three viable paths nearly code-free — and treat a plugin-native
interactive OAuth flow as a later convenience, not a v1 requirement:

| Path | Access | Unattended? | Setup cost |
|---|---|---|---|
| (a) SA + shared folder (key or impersonation) | only what's shared to the SA | yes — cleanest | GCP project, share ceremony |
| (b) BYO OAuth client via ADC `--client-id-file` | full personal Drive | ⚠ 7-day expiry while client is in Testing | GCP project + client, consent dance |
| (c) Internal org-owned client, 3LO | everything the user sees; best corp-IT survival | ⚠ modulo session policies | org must allow project creation |

Note the repo's static-env credential convention
(`CUTTING_GARDEN_GIT_TOKEN`-style) does not fit OAuth: the refresh
token is obtained interactively and rotates. Leaning on ADC keeps the
credential file under gcloud's ownership rather than introducing a
cutting-garden-owned token cache (which would drag FDR 0008's at-rest
questions onto the credential, not just the blob store).

### Identity interplay

The authenticated account determines *what you can see* — a folder
tree captured by two accounts can differ by ACL. Lean: the account is
a credential, not an invocation, so it stays **out** of the identity
subtree, with the ACL-visibility caveat documented. Open question.
`environment.plugin` would carry API surface versions and (under the
export posture) the export MIME map.

## Examples

Sketches, posture-independent:

    # one-time credential setup (path (b) shown)
    gcloud auth application-default login \
        --client-id-file=client.json \
        --scopes=openid,https://www.googleapis.com/auth/userinfo.email,https://www.googleapis.com/auth/drive.readonly

    cutting-garden capture gdrive:<folder-id>
    cutting-garden diff <gdrive-receipt> gdrive:<folder-id>

    # preflight failure shape (distinct from drift)
    $ cutting-garden capture gdrive:<folder-id>
    error: ADC found but missing Drive scope; re-run:
      gcloud auth application-default login --client-id-file=… --scopes=…

## Limitations

Anticipated v1 boundaries (to be firmed up at `proposed`):

- No `https://` pass-through; explicit `gdrive:` opt-in only.
- Restore-to-Drive out of scope (converted-copy semantics); restore
  is to filesystem via the export artifacts.
- Permissions/ACLs, comments, suggestions, and revision *history* are
  not captured (whatever the posture, exports and structural reads
  return current state).
- Shared-drive specifics, shortcuts, and multi-parent legacy files
  need explicit handling decisions.
- No CI against real Drive; testing needs an `httptest` fake-Drive
  server (the Google API Go clients accept a custom endpoint) — the
  `cmd/cutting-garden-test-git-sshd` analog, and a meaningful chunk
  of the work.

## Verification checklist

Empirical/doc checks that gate promotion; ⚠ items from above:

1. **Export byte-stability** — re-export an unchanged Doc/Sheet twice,
   compare bytes. Decides whether the export-bytes posture needs a
   `normalize` pass or is viable at all.
2. `files.export` size limit (believed ~10 MB) and the workaround
   surface for larger docs.
3. Whether `gcloud auth application-default login --scopes=<drive>`
   *without* `--client-id-file` is hard-rejected today.
4. Device-flow scope allowlist (believed to exclude Drive).
5. Current Workspace defaults for unconfigured third-party apps on
   restricted scopes, and whether "trust internal apps" is still the
   default.
6. Whether Workspace cloud-session re-auth policies bind an internal
   OAuth client's refresh tokens.
7. Go auth-library support for the `impersonated_service_account` ADC
   file type (`cloud.google.com/go/auth` vs `x/oauth2/google`).
8. Forms API access through SA sharing.
9. Testing-status 7-day refresh-token expiry conditions (which scope
   classes / consent-screen types exactly).

## Open Questions

- **The posture fork** (§Capture posture) — export-bytes, structural,
  or hybrid; per-product structural schemas if structural.
- Does the capturing account participate in capture identity?
  (Leaning no; ACL caveat documented.)
- Accept full `docs.google.com` / `drive.google.com` URLs inside the
  `gdrive:` opaque form, normalized to IDs?
- Default export format set (export posture): pdf? docx+xlsx+pptx?
  per-product defaults? — a tuning lever once real captures exist.
- Capture Forms *responses* (forms.responses) or structure only?

## References

- FDR 0005 — URI-scheme plugin system (registry contract).
- FDR 0006 — git plugin (the protocol-capture template).
- FDR 0007 — keepassxc plugin (the design-only precedent; the
  "where the analogy snaps" framing used here).
- FDR 0003 — yt-dlp plugin (exec-template fallback shape; restore
  deferral precedent; the `https` host-router placeholder).
- FDR 0008 — capture-store requirements (at-rest posture, relevant to
  any cutting-garden-owned token cache).
- RFC 0002 — Capture Plugin Protocol; RFC 0004 — Git-Archive Binding
  (the binding-RFC template).
- gcloud `auth application-default login` reference (fetched
  2026-06-07; page updated 2026-05-27) — source of the
  Drive-scopes-need-`--client-id-file` finding.
- "How Application Default Credentials works"
  (cloud.google.com/docs/authentication/application-default-credentials,
  updated 2026-06-05) — search order, scope-extension routes.
- rclone "making your own client_id" documentation — the BYO-client
  pain writeup.
