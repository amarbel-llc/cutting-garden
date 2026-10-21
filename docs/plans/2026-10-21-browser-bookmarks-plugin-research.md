# Browser bookmarks plugin (Chrome + Firefox): research

Status: research only, no code. Paths use `~`. No real profile or bookmark data was read; facts come from docs, Chromium/Firefox source mirrors, and the chrest/moxy/cutting-garden/smith repos. `(guess)` marks inference not backed by a source I fetched.

## 1. Chrome: is there an API besides chrome.bookmarks and the Bookmarks file?

No documented first-party HTTP/server API for Chrome Sync bookmarks was found. This is a negative result from the pages checked, not a proof.

- Chrome sync help describes the feature from the user side only; with a passphrase the data is client-side encrypted. https://support.google.com/chrome/answer/165139
- Chrome Management API is enterprise fleet management (apps, profiles, reports, telemetry); no bookmark resource. https://developers.google.com/chrome/management/reference/rest (`profiles.commands` remote command types not checked.)
- Takeout lists no Chrome/bookmarks line on the help page (https://support.google.com/accounts/answer/3024190); manual export is HTML only (https://support.google.com/chrome/answer/96816).
- Legacy google.com/bookmarks, People API, Drive: not verified; (guess) none expose Chrome bookmarks.
- moxy's gws moxins wrap Drive/Docs/Sheets/Gmail/Calendar only; `rg chrome|bookmark` over `moxins/` found nothing.

Bookmarks file (Chromium `components/bookmarks/browser/bookmark_codec.cc`, `bookmark_storage.cc`): JSON with `version`, `roots` (`bookmark_bar`, `other`, `synced` = mobile, "left as 'synced' for historical reasons"), `checksum`, optional `sync_metadata`. Nodes: `id` (decimal string), `guid`, `name`, `type` url|folder, `date_added` as microseconds since 1601, `meta_info`. The in-memory model is authoritative and re-serialized by a delayed atomic write; `Bookmarks.bak` is copied once per session. (Inference) external edits while Chrome runs are overwritten and not picked up; treat as read-only unless Chrome is closed.

chrome.bookmarks (https://developer.chrome.com/docs/extensions/reference/api/bookmarks): `bookmarks` permission; string ids stable per profile across restarts; create/move/update(title,url only)/remove(empty folder only)/removeTree; `folderType` and `unmodifiable: "managed"` mark unwritable roots; `dateAdded` in ms; no tags, no keywords, no guid.

## 2. Firefox: same question

- WebExtension `browser.bookmarks` (MDN + `browser/components/extensions/{schemas/bookmarks.json,parent/ext-bookmarks.js}`): ids are Places guids; roots `root________`, `menu________`, `toolbar_____`, `unfiled_____`, `mobile______`; types bookmark|folder|separator (separator url `data:`); create/move/update(title,url)/remove (non-empty folder refused)/removeTree; root unmodifiable. **Tags and keywords are not exposed** (no fields; tagging events skipped).
- `places.sqlite` (`nsPlacesTables.h`): `moz_bookmarks` (type 1/2/3, fk, parent, position, guid, dateAdded), `moz_places`, `moz_keywords` (keyed to place, not bookmark), `moz_bookmarks_deleted` tombstones. Tags: root guid `tags________` exists; (guess) each tag is a folder of rows whose fk is the place. WAL: a reader needs the `-wal`/`-shm` files or `immutable=1` on a quiesced copy (https://www.sqlite.org/wal.html section 5). Writing while Firefox runs bypasses Places caches and sync change counters; do not.
- Automatic backups `bookmarkbackups/*.jsonlz4` (`PlacesBackups.sys.mjs`): atomic files; tree JSON includes `tags` (CSV), `keyword`, `annos`; dates in microseconds. Safest read source for tags.
- Firefox Sync: protocol is documented and client-side encrypted (AES-256-CBC + HMAC, keys from the Mozilla account) https://mozilla-services.readthedocs.io/en/latest/sync/overview.html, `.../storageformat5.html`, `.../objectformats.html` (bookmark records carry `tags`, `keyword`, `parentid`, `children`). Third-party OAuth scopes `apps/sync/bookmarks[.readonly]` appear in a design doc (https://mozilla.github.io/ecosystem-platform/explanation/scoped-keys); that they are issuable to arbitrary clients today is not verified (guess: no). Not a practical API.

## 3. Prior art

### chrest (`~/eng/repos/chrest`)

- Manifests request `bookmarks` in both browsers (`extension/manifest-{chrome,firefox}.json:6`).
- Read: `GET /items` (`extension/src/routes.js:20-31`) merges tabs, bookmarks (flat `bookmarks.search({})`, `items.js:175-179`) and history. Item shape is `{title, id{browser,id,type}, url, date}`: **no parentId, path, index, folder/separator type, tags** (`go/internal/charlie/browser_items/item.go:3-9`). `GET /bookmarks` (`routes.js:466-474`) returns raw `getTree()` with hierarchy, but **no Go code consumes it** (reachable via `chrest client`).
- Ids: `/<browser>-<name>/bookmark-<id>` (`item_id.go`), browser-native id only; not cross-browser stable.
- Write: create is broken (`makeUrlItemBookmarks`, `items.js:64-81`, uses an undeclared `result`; no parentId); delete = `bookmarks.remove` only; **no update/move/folder-create/removeTree/separators**. `isOurBid` is async so the browser-id filter on PUT is a no-op (`routes.js:52-62`). MCP `items-put` sends `ExternalId` where the extension reads `id.browser/id.id`, so delete likely does not work end to end (guess; not run). Man pages document none of this.
- Transport: native messaging to the Go host, which serves HTTP over an `AF_UNIX` socket per browser (`$XDG_STATE_HOME/chrest/<browser-id>.sock`, dir mode 0700); no TCP port, no token. Fan-out across sockets lets Chrome and Firefox be live at once.
- cutting-garden integration today: `capture-serve` (RFC 0008) and `capture-batch` for `web:`; chrest already bridges cutting-garden as a flake input (`go/go.nix:6-7`). **No traversal plugin** exists in chrest.

### moxy gws and siblings (`~/eng/repos/moxy/moxins`)

All are thin TS/zx wrappers around the external `gws` CLI; moxy holds no OAuth code. Auth is gws's: one-time `gws auth login` against a user-owned Desktop OAuth client, credentials in `~/.config/gws/credentials.enc` (key in OS keyring or file), overridable by `GOOGLE_WORKSPACE_CLI_TOKEN` / `..._CREDENTIALS_FILE` (`gws/README.md:111-178`). Not relevant to Chrome bookmarks. Transferable pattern: shell out to a vetted CLI that owns credentials; never put secrets in cutting-garden config or on the wire (RFC 0013 initialize). Irrelevant for chrest, whose "auth" is socket file permissions.

### cutting-garden plugin model

- Wire plugin minimum: `initialize`, `shutdown`, `nodes.list`; capabilities gate the rest (`roots`, `leaf-read`, `facet-counts`, `mutate`, `container-create`, `bulk-mutate`...). `node.patch` returns `applied` keys; bad value = `-32602`, nothing written (RFC 0013; plugins.7).
- organize writes only through declared facet dimensions: moving a line under another bucket heading sends `node.patch {"<field>": "<bucket>"}` (`facet_writes` mode `one`). Folder move therefore maps to a `folder` dimension (mode `one`, field `parent`). org's explicit `parent`/`before`/`after` patch keys (`plugins/org/move.go:18-65`) are the model for non-organize callers and reordering.
- Config: `[[traversal_plugins]]` with FULL argv, or `[[plugins]]` with BASE argv and `protocols`; one stanza cannot declare two protocols, so chrest needs a second stanza for traversal beside the existing `web` capture stanza.
- Smith (`~/eng/repos/smith/crates/cutting_garden/src/lib.rs`) is the out-of-tree precedent; Go peers use `pkgs/traversal_serve`.
- Wire limitations: no `NodeIDer` token (ids default to relative path; use `?id=` as ynab does), no `ReportPartialListing`.

## 4. Recommendation: B, a wire plugin served by chrest

`chrest traversal-serve`, scheme `bookmark:`, in the chrest repo. Reasons: chrest already owns the browser knowledge, both sockets, the extension, and the cutting-garden bridge; smith shows the shape; an in-tree plugin (A) would hard-code chrest's socket layout and ship in every cutting-garden release; a file reader (C) cannot write safely.

Prerequisite (chrest's lane, not ours): extension routes for folder-aware read, create (bookmark and folder), update, move, remove; fix the broken create and `isOurBid`.

Tree: `bookmark://<browser>-<name>/` (one root per live browser socket) -> root folders (Chrome: bookmarks bar / other / mobile; Firefox: toolbar / menu / unfiled / mobile) -> folders -> bookmarks. Node ids via `?id=` (browser-native id; Firefox guid). Separators: list as read-only nodes or hide (decide).

Facets: `folder` (mode `one`, writable, `known_empty_values` so empty folders are drop targets), `domain` (derived, read-only), `date_added` band (read-only), `tags` only if a Firefox-only read path is added (Option C as a read supplement). Chrome has no tags/keywords at all.

Write surface (v1): move between folders (organize drag-drop), reorder via `before`/`after`, rename (`title`), change `url`, create bookmark and folder (temp-id create, `creatable`). Delete: bookmark only; refuse non-empty folders (`remove` already does), refuse roots and `unmodifiable`/managed nodes, no `removeTree` in v1. Mutating tags/keywords: out of scope.

## 5. Risks and guards

- Data loss: folder delete (`removeTree`), moves into the wrong folder, URL edits. Guard, ynab pattern: validate before any request; fresh `bookmarks.get` by id before each write; verify `parentId`/`index`/title in the response; no-op detection; refuse ambiguous (same title+folder, unknown folder path) listing candidates; refuse roots/managed.
- Sync conflicts: extension writes go through the browser's own model, so Chrome Sync / Firefox Sync reconcile them, but a move can race another device (inherent; read-back verify detects the local result only).
- Profile files: never write Chrome's `Bookmarks` or Firefox's `places.sqlite` while the browser runs (overwritten / bypasses sync counters). File/backup reads only, as a supplement.
- Multi-browser: chrest's PUT fan-out bug could hit both browsers; the plugin must address one socket explicitly.
- Volume: no batching in chrest; `BulkMutator` would cost N requests.
- Live-check ritual before shipping: apply each write to a scratch folder in a throwaway profile, read back, restore, compare (ynab FDR 0027 ritual).

## 6. Open questions

For chrest's session (not answered here): should chrest grow a bookmarks API (folder-aware tree, move/update/create/removeTree) and a `traversal-serve` subcommand? Fix create / `isOurBid` / `items-put` delete? How does `search({})` present folders/separators in each browser (unverified)?

For the user: is read-only first acceptable (list + facets, writes in a second slice)? Are Firefox tags/keywords needed (forces a backup/places.sqlite read path; the extension cannot see them)? Separators: show or hide? One root per browser or per profile?

## 7. Decisions (2026-10-21)

Answered by the user after this research:

- **v1 is read-only**: list and group by folder, domain and date added. Writes (move, reorder, rename, change URL, create, delete bookmark) are separate follow-up issues, not part of v1.
- **No Firefox tags or keywords**: folders only, so there is one data path (chrest) and no profile-file reader.
- **One root per browser instance**, matching chrest's one-socket-per-browser addressing: `bookmark://<browser>-<name>/`. Per-profile roots would need chrest to expose profiles, which it does not today.
- **Separators**: not decided; hide them in v1 (they carry no data) unless asked otherwise.
- **chrest-owned questions** (a bookmarks API, the create / `isOurBid` / `items-put` delete bugs, what `search({})` returns for folders and separators) went to a chrest session, not decided here.
