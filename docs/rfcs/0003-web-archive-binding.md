---
status: proposed
date: 2026-05-25
revised: 2026-07-12 (environment/outcome node v2 — `command_line` moved
  from identity to outcome; see § Compatibility)
---

# Web-Archive Binding

## Abstract

This document is a **binding** of the [Capture Plugin Protocol
(RFC 0002)](./0002-capture-plugin-protocol.md) for the `web`
capture kind. It pins the plugin-defined node-type schemas, the
payload-type catalog, per-format normalization rules, capabilities
artifact contents, and the extension-loading semantics that any
web-archive plugin (chrest is the reference implementation;
hypothetical second implementations such as wkhtmltopdf-based or
monolith-only fit the same slots) MUST emit.

This RFC does not redefine the protocol substrate. The merkle
tree, hyphence framing, typed blob refs, type-signature resolution,
IANA-media-type interface, batch input/output, writer contract,
and stability table all live in RFC 0002 and apply unchanged.

## Introduction

The Capture Plugin Protocol intentionally leaves three slots in the
capture merkle tree to be filled by a per-kind binding:

- `identity → environment → plugin` (identity-affecting plugin
  state)
- `outcome → plugin` (per-run plugin observations)
- `receipt → payload[]` (the captured bytes per format)

For the `web` capture kind, this RFC pins the schemas of those
slots. It also defines:

- The catalog of payload formats a web-archive plugin MUST or MAY
  support.
- Per-format payload normalization rules (what gets stripped from
  payload bytes when `normalize=true`, and where the stripped
  fields land in the outcome's `stripped` object).
- The DNS configuration sub-schema.
- The browser-extension loading semantics and the schema of the
  capabilities artifact that a web-archive plugin SHOULD emit at
  `environment.binary.capabilities_id`.

### Relationship to RFC 0002

This document is normative for the `web` capture kind. A web-archive
plugin is conformant with [RFC 0002](./0002-capture-plugin-protocol.md)
*and* this RFC iff its emitted blobs satisfy both. The plugin's
receipt-type-tag MUST be `!cutting_garden-capture-receipt-web-v1`.

### Scope

This document specifies, for the `web` capture kind:

- The schemas of the plugin-defined identity, outcome, and payload
  node types.
- The catalog of payload formats and their normalization rules.
- The capabilities artifact schema.
- The DNS configuration and extension-loading semantics that
  identity-affecting fields imply.

### Out of Scope

This document does not specify:

- Browser-launch internals, fetch pipeline, or rendering details.
- Cross-archive search, retrieval, or presentation.
- The protocol substrate itself (RFC 0002).
- Plugin binding for any other capture kind (fs, streaming, mail,
  calendar). Those are defined by their own RFCs or by RFC 0001
  (for fs).

### Background

This RFC supersedes [nebulous RFC 0001 §Artifact
Formats][nebulous-rfc-0001]'s flat `spec`/`envelope`/`payload`
shape. The chrest capturer's pre-migration emitter is the source of
truth for the web-specific *contents* (browser fields, DNS schema,
HTTP envelope shape, normalization rules per format); this RFC
relocates those contents to the merkle tree shape of RFC 0002.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in
this document are to be interpreted as described in [RFC
2119][rfc-2119].

## Specification

### Capture Kind

The capture kind is `web`. A web-archive plugin's receipt blob
MUST carry the type line:

```
! cutting_garden-capture-receipt-web-v1
```

The plugin discriminator inside the identity tree
(`environment.binary.name`) tells consumers which implementation
produced these bytes. Currently:

- `chrest` — the reference web-archive plugin.

Future implementations slot in by populating `binary.name` with
their own identifier and emitting plugin nodes under their own
prefix (`!jcs-<their-name>-capture-environment-v2`, etc.).

### Identity Tree — `plugin` Slot

The identity tree's plugin slot
(`identity → environment → plugin`) MUST be a hyphence node of
type `!jcs-<plugin>-capture-environment-v2`.

> `-v1` carried a `browser.command_line` field here; it moved to the
> outcome tree in v2 (see [§ Compatibility](#compatibility)). Readers
> for `-v1` are retained per RFC 0010; new captures MUST write `-v2`.

Body: JCS-canonical JSON.

```json
{
  "browser": {
    "name":         "<string>",
    "version":      "<string>",
    "user_agent":   "<string>",
    "js_engine":    "<string>",
    "platform":     "<string>",
    "prefs":        { "<key>": "<value>" }
  },
  "dns":        { /* see § DNS Configuration */ },
  "extensions": [
    { /* see § Browser Extensions */ }
  ],
  "isolation":  "fresh" | "session" | "shared"
}
```

| Field                  | Required | Identity-affecting | Description                                                                                                                                  |
|------------------------|----------|--------------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `browser.name`         | yes      | yes                | Browser backend. MUST be one of: `chrome`, `firefox`.                                                                                        |
| `browser.version`      | yes      | yes                | Browser version string as reported by the browser.                                                                                            |
| `browser.user_agent`   | yes      | yes                | User-agent string.                                                                                                                            |
| `browser.js_engine`    | no       | yes                | JS engine name (`V8`, `SpiderMonkey`).                                                                                                       |
| `browser.platform`     | yes      | yes                | Browser-reported platform string (e.g. `Linux x86_64`).                                                                                       |
| `browser.prefs`        | no       | yes                | Object of rendering-affecting browser preferences. Omitted if not gathered; `{}` means "gathered, none rendering-relevant".                  |
| `dns`                  | no       | yes                | Resolved DNS configuration (see [§ DNS Configuration](#dns-configuration)).                                                                   |
| `extensions`           | yes      | yes                | Array of extension descriptors (see [§ Browser Extensions](#browser-extensions)). MUST be `[]` if none.                                       |
| `isolation`            | yes      | yes                | Browser isolation level. MUST be one of: `fresh`, `session`, `shared`. Plugin's defaulting behavior is plugin-defined; once resolved, MUST appear here. |

All listed fields are identity-affecting; changing any changes the
plugin-node markl-id, which changes the environment markl-id, which
changes the identity markl-id.

Every identity field MUST be **stable under re-run**: if re-running
the identical capture request with identical configuration could
yield a different value, the field belongs in the outcome tree, not
here. (This is why v2 removed `browser.command_line`: real argv
embeds per-launch values — temp profile paths, ports — so no two
launches shared an identity markl-id, defeating the identity/dedup
model.) Corollary: a launch parameter that IS config-derived and
rendering-affecting MUST be surfaced as a structured, stable field
in this schema (as `prefs`, `isolation`, `dns`, and `extensions`
are) — never smuggled in via raw argv. Raw argv is an observation
of what ran, and lives in the outcome tree.

The plugin MUST ensure each `browser.prefs` entry actually
participates in rendering. The chrest reference implementation
filters to rendering-affecting preferences; non-rendering settings
(theme, telemetry opt-outs) are excluded by construction so
identity is not polluted with user-preference noise.

### Outcome Tree — `plugin` Slot

The outcome tree's plugin slot (`outcome → plugin`) MUST be a
hyphence node of type `!jcs-<plugin>-capture-outcome-v2`.

> `-v1` had no `process` object (`command_line` lived in the identity
> tree; see [§ Compatibility](#compatibility)). Readers for `-v1` are
> retained per RFC 0010; new captures MUST write `-v2`.

Body: JCS-canonical JSON.

```json
{
  "http": {
    "status":      <integer>,
    "final_url":   "<string>",
    "headers":     [
      { "name": "<lowercased-name>", "value": "<value>" }
    ],
    "timing_ms":   { "dns": <int>, "tcp": <int>, "tls": <int>, "ttfb": <int>, "load": <int> },
    "resolved_ip": "<string>"
  },
  "process": {
    "command_line": ["<string>", "..."]
  }
}
```

| Field                  | Required    | Description                                                                                                                                                  |
|------------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `http.status`          | yes         | HTTP status code of the top-level document fetch.                                                                                                            |
| `http.final_url`       | no          | The resolved URL after any redirect chain, if different from `identity → invocation.target`. Plugins SHOULD include when redirects were followed.            |
| `http.headers`         | yes         | HTTP response headers as an **array** of `{name, value}` objects. Names MUST be lowercased. Order MUST be preserved as observed on the wire. Duplicate names (e.g. multiple `set-cookie`) are preserved as separate array entries — using a map shape would silently lose multiplicity and order. |
| `http.timing_ms`       | no          | Network timing observations in milliseconds. All sub-keys (`dns`, `tcp`, `tls`, `ttfb`, `load`) are OPTIONAL; plugins emit whichever subset their transport exposes. A plugin restricted to overall load time only (e.g. W3C WebDriver BiDi `network.responseCompleted`) emits `{"load": <int>}`. The object form is canonical; bare-integer is NOT permitted. |
| `http.resolved_ip`     | no          | IP (IPv4 or IPv6) the browser used for the top-level fetch. Plugins that cannot obtain this (e.g. W3C WebDriver BiDi `network.ResponseData` lacks the field) MUST omit. |
| `process.command_line` | no          | The browser process argv **as observed** — verbatim, in the order passed, including per-launch values (temp profile paths, ports). An observation of what ran, NOT an identity claim; two captures of the same request legitimately differ here. Plugins that launch a browser process SHOULD emit it. |

The `http.*` fields are REQUIRED as a group when the transport
observed the top-level fetch (see the preview rule below); `process`
is independent of `http` and SHOULD be present on essentially every
capture that spawns a browser.

The body MAY include additional plugin-specific keys (e.g. chrest's
internal trace counters) under sibling top-level objects. Consumers
MUST ignore unknown top-level keys.

### Preview Schema for Backends Without `http.*`

Plugins whose transport stack cannot populate `http.{status,headers}`
(historical example: Chrome/CDP path in chrest before the BiDi
event-loop refactor) MUST emit the outcome-plugin node with type:

```
!jcs-<plugin>-capture-outcome-v2-preview
```

carrying whatever it DID observe — in practice at least
`process.command_line`, which is available on essentially every
capture that spawns a browser. The `-preview` marker signals only
that the REQUIRED `http.*` group is missing; the presence of
`process.*` neither requires nor lifts it.

Strict consumers MUST reject `-preview` typed outcomes. Tolerant
consumers MAY opt in. A plugin SHOULD drop `-preview` once it can
populate the required `http.*` fields.

### Capabilities Artifact

The web-archive plugin SHOULD emit a capabilities artifact at
`environment.binary.capabilities_id` describing what it can
produce. Type:

```
!jcs-<plugin>-capture-capabilities-v1
```

Body: JCS-canonical JSON.

```json
{
  "formats":        ["<string>", "..."],
  "browsers":       ["chrome", "firefox"],
  "normalizes":     ["<format>", "..."],
  "honors_dns":     <bool>,
  "honors_extensions": <bool>,
  "transport":      "cdp" | "bidi" | "<other>"
}
```

| Field               | Required | Description                                                                                          |
|---------------------|----------|------------------------------------------------------------------------------------------------------|
| `formats`           | yes      | Payload formats this plugin can emit. Subset of [§ Payload Format Catalog](#payload-format-catalog). |
| `browsers`          | yes      | Browser backends this plugin can drive.                                                              |
| `normalizes`        | yes      | Subset of `formats` for which the plugin implements payload normalization.                          |
| `honors_dns`        | yes      | Whether the plugin can apply the requested `dns` configuration.                                      |
| `honors_extensions` | yes      | Whether the plugin can load `browser.extensions`.                                                    |
| `transport`         | no       | The browser-control transport (e.g. `cdp`, `bidi`). Identity-relevant for the preview schema rule.   |

Capabilities are identity-affecting (its markl-id sits at
`environment.binary.capabilities_id` and participates in identity).

### Payload Format Catalog

The **format string** column is the value of `captures[].format`
in the batch input — user-facing, hyphen-separated where
multi-word. The **type-segment** column is the corresponding
segment in the payload type-string `!<plugin>-capture-payload-<segment>-v1`
— underscore-separated where multi-word, per RFC 0002's
type-string convention (`-` separates type-string segments, `_`
separates words within a segment).

Plugin emitters MUST map `format → type-segment` by replacing `-`
with `_` (the only transformation; otherwise the segments are
identical). Plugin emitters MUST NOT use the hyphenated form in
the type tag, and MUST NOT use the underscore form in batch
input. The two surfaces have intentionally different conventions.

| Format string         | Type-segment        | Type string                                              | IANA media type                              | Normalization defined? |
|-----------------------|---------------------|----------------------------------------------------------|----------------------------------------------|------------------------|
| `text`                | `text`              | `!<plugin>-capture-payload-text-v1`                      | `text/plain; charset=utf-8`                  | yes                    |
| `pdf`                 | `pdf`               | `!<plugin>-capture-payload-pdf-v1`                       | `application/pdf`                            | yes                    |
| `screenshot`          | `screenshot`        | `!<plugin>-capture-payload-screenshot-v1`                | `image/png`                                  | yes                    |
| `mhtml`               | `mhtml`             | `!<plugin>-capture-payload-mhtml-v1`                     | `multipart/related; type="text/html"`        | TBD (chrest follow-up) |
| `a11y`                | `a11y`              | `!<plugin>-capture-payload-a11y-v1`                      | `application/vnd.cutting-garden.a11y+json`   | TBD                    |
| `html-monolith`       | `html_monolith`     | `!<plugin>-capture-payload-html_monolith-v1`             | `text/html; charset=utf-8`                   | TBD                    |
| `markdown-full`       | `markdown_full`     | `!<plugin>-capture-payload-markdown_full-v1`             | `text/markdown`                              | TBD                    |
| `markdown-reader`     | `markdown_reader`   | `!<plugin>-capture-payload-markdown_reader-v1`           | `text/markdown`                              | TBD                    |
| `markdown-selector`   | `markdown_selector` | `!<plugin>-capture-payload-markdown_selector-v1`         | `text/markdown`                              | TBD                    |

Payload type-blobs MUST set both `iana_media_type` and
`payload_cardinality` per [RFC 0002 §IANA Media Type
Interface][rfc-0002-iana]. For all web payload types here,
`payload_cardinality = "single"`.

#### Screenshot Encoding

The `screenshot` format is PNG-only in this revision. JPEG and
other lossy encodings are out of scope. A future revision MAY
introduce JPEG as a quality-option under the same `screenshot`
format (e.g. `options.encoding = "jpeg"` with stripped fields
under `outcome.stripped.screenshot.jpeg.*`); it MUST NOT be a
separate top-level format string. Until then, plugins MUST emit
PNG bytes for `format = screenshot` and MUST NOT accept a JPEG
option.

#### Format Options

Format-specific options live in `captures[].options` in the batch
input and are echoed verbatim into `identity → invocation.options`.
The web kind defines options for two formats; others have no
defined options and any provided options MUST be ignored (and not
participate in identity).

##### `markdown-reader` options

```json
{ "reader_engine": "readability" | "browser" }
```

| Option          | Required | Default        | Description                                                                                                                                |
|-----------------|----------|----------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `reader_engine` | no       | `"readability"`| Selects the article-extraction backend. `"readability"` uses a Readability-style heuristic extractor. `"browser"` is reserved for a future native engine. |

A plugin MUST reject `reader_engine = "browser"` with a per-capture
error of kind `"not-implemented"` until that engine is shipped.
Unknown values MUST be rejected with a per-capture error of kind
`"invalid-options"`.

##### `markdown-selector` options

```json
{ "selector": "<css-selector>" }
```

| Option     | Required | Description                                                                                                              |
|------------|----------|--------------------------------------------------------------------------------------------------------------------------|
| `selector` | yes      | CSS selector scoping the markdown conversion to a single element. First match wins. Non-empty string.                    |

A plugin MUST reject a `markdown-selector` capture that omits
`selector` or provides an empty string with a per-capture error of
kind `"invalid-options"` and `message` naming the missing/invalid
field.

### Per-Format Normalization Rules

When the resolved `normalize` value for a capture is `true`, the
plugin MUST apply the format-specific normalization rules in this
section before writing payload bytes. Stripped fields MUST be
recorded in the outcome's `stripped.<format>` sub-object.

A plugin's [capabilities artifact](#capabilities-artifact) MUST
list a format in `normalizes` iff the plugin implements its
normalization rule below.

#### `text`

Stripped fields: none — text capture is naturally byte-stable when
extracted from a deterministic DOM snapshot.

`outcome.stripped.text` MAY be `{}` or omitted.

#### `pdf`

Stripped fields, recorded under `outcome.stripped.pdf`:

| PDF field      | Action                  |
|----------------|-------------------------|
| `/CreationDate`| stripped from `/Info` dict |
| `/ModDate`     | stripped from `/Info` dict |
| `/ID`          | stripped from trailer   |
| `/Producer`    | normalized to a fixed canonical string declared by the plugin |

The chrest reference implementation uses
[pdfcpu](https://github.com/pdfcpu/pdfcpu) for the rewrite.

#### `screenshot`

Stripped fields, recorded under `outcome.stripped.screenshot`:

| PNG chunk      | Action                  |
|----------------|-------------------------|
| `tIME`         | stripped                |
| `tEXt:date:*`  | stripped                |
| `tEXt:Creation Time` | stripped          |

Compression-level normalization (re-encoding at a fixed deflate
level) is RECOMMENDED but optional; when applied, the plugin MUST
record the chosen level in `outcome.stripped.screenshot.recompressed_at`.

#### `mhtml`, `a11y`, `html-monolith`, `markdown-*`

> **Deferred.** Per-format normalization rules for these formats
> are pending chrest implementation. Until rules are defined here,
> a conformant web-archive plugin MUST reject `normalize=true` for
> these formats with a per-capture error of kind `not-implemented`.
>
> Implementations MAY support `normalize=false` for these formats,
> in which case bytes are written as-captured and
> `outcome.stripped.<format>` is omitted.

### DNS Configuration

A `dns` object in the plugin identity node configures hostname
resolution for the top-level fetch and subresources.

```json
{
  "mode":    "system" | "doh" | "custom",
  "doh_url": "<string>",
  "servers": ["<string>", "..."]
}
```

| Field      | Required        | Description                                                                                        |
|------------|-----------------|----------------------------------------------------------------------------------------------------|
| `mode`     | yes             | One of `system` (OS resolver), `doh` (DNS-over-HTTPS), `custom` (resolvers in `servers`).         |
| `doh_url`  | conditional     | REQUIRED when `mode` is `doh`. MUST be a fully-qualified `https://` URL.                          |
| `servers`  | conditional     | REQUIRED when `mode` is `custom`. Non-empty list of resolver IPs or `host:port` strings.          |

The DNS configuration is identity-affecting (echoed verbatim into
the plugin identity node). Two captures of the same URL with
different DNS configurations are NOT interchangeable because
resolver behavior (blocklists, geo-routing, NXDOMAIN responses) can
change captured bytes.

A plugin that cannot honor a requested DNS configuration MUST fail
the affected capture rather than emit an identity claiming
`dns.<value>` was applied when it wasn't. The plugin SHOULD surface
the limitation in its capabilities artifact (`honors_dns = false`).

### Browser Extensions

Each entry in `identity.environment.plugin.extensions[]` is an
object whose shape depends on a `source` discriminator. Two modes
are conformant:

#### Preinstalled mode (`source = "preinstalled"`)

The plugin reads extensions already installed in the browser
profile rather than fetching from a URL. This is chrest's current
behavior.

```json
{
  "source":          "preinstalled",
  "id":              "<extension-id>",
  "version":         "<string>",
  "manifest_digest": "<markl-id>"
}
```

| Field             | Required    | Description                                                                                                    |
|-------------------|-------------|----------------------------------------------------------------------------------------------------------------|
| `source`          | yes         | MUST be `"preinstalled"`.                                                                                       |
| `id`              | yes         | Browser-assigned extension ID (e.g. Chrome's 32-char extension ID, Firefox's `<…>@<vendor>` ID).               |
| `version`         | yes         | Extension version as declared in its manifest.                                                                  |
| `manifest_digest` | RECOMMENDED | Markl ID of the extension's `manifest.json` bytes. Pins identity to the manifest seen at capture time.          |

#### Fetched mode (`source = "fetched"`)

The plugin fetches the extension archive from a URL and installs it
into the browser before capture.

```json
{
  "source":  "fetched",
  "name":    "<string>",
  "version": "<string>",
  "url":     "<https-url>",
  "digest":  "<markl-id>"
}
```

| Field     | Required | Description                                                                                            |
|-----------|----------|--------------------------------------------------------------------------------------------------------|
| `source`  | yes      | MUST be `"fetched"`.                                                                                    |
| `name`    | yes      | Extension name as declared in its manifest.                                                            |
| `version` | yes      | Extension version as declared in its manifest.                                                         |
| `url`     | yes      | HTTPS URL the plugin fetched the extension archive from.                                               |
| `digest`  | yes      | Markl ID of the extension archive bytes (returned by the writer after the plugin wrote them).         |

#### Identity

Both modes are identity-affecting in their entirety, **including the
`source` discriminator itself**. Two captures of the same extension
(same `id`/`name` + same `version`) under different `source` modes
MUST NOT produce identical extension entries: a preinstalled
extension's bytes can drift across browser updates without the
plugin observing the drift, while a fetched extension's bytes are
pinned by the writer-returned digest. The two modes therefore
represent meaningfully different identity claims about the
extension that participated in the capture.

A plugin that cannot load a requested extension (or cannot read an
existing profile extension) MUST fail the affected capture (per RFC
0002's identity-forgery rules) rather than emit an identity claiming
the extension was loaded.

A plugin's [capabilities artifact](#capabilities-artifact) MAY
split `honors_extensions` into `honors_preinstalled_extensions`
and `honors_fetched_extensions` to distinguish the two modes.

Install timing is plugin-defined and gated by `isolation`:

| Isolation | Install timing                                                                       |
|-----------|--------------------------------------------------------------------------------------|
| `fresh`   | Once per capture, into the per-capture browser process.                              |
| `session` | Once at session start, into the session's shared browser process.                    |
| `shared`  | Once at capturer startup, into the long-lived browser process.                       |

## Security Considerations

### Untrusted Page Content

A web capture renders bytes from URLs that may be adversarial. The
plugin MUST treat captured content (DOM, scripts, headers, images)
as untrusted input and MUST NOT propagate execution to the host
beyond the browser process the plugin controls. The browser's own
sandbox is the trust boundary.

### Header Leakage

`outcome.plugin.http.headers` carries HTTP response headers
verbatim (lowercased). These may include `Set-Cookie`, server
tokens, internal hostnames, or other fingerprinting material.
Operators should treat outcome blobs with the same access controls
they apply to raw request logs.

### Extension Trust

Browser extensions loaded via `identity.environment.plugin.extensions`
run inside the captured browser process with extension privileges.
A malicious extension URL is a vector for capturing or modifying
the page's bytes mid-render. Plugins SHOULD validate extension URLs
against an operator-controlled allow-list.

### DNS Configuration as Identity Lever

DNS configuration changes capture identity. An operator that
silently swaps DNS modes between captures of the same URL will
produce diverging identity markl-ids even though the user-visible
target is unchanged. This is intentional (DNS affects what bytes
are captured) but operators should document their DNS posture so
identity comparisons remain meaningful.

## Compatibility

### Migration from `web-capture-archive/v0+v1`

Old chrest captures emitted under [nebulous RFC
0001][nebulous-rfc-0001] mapped fields as follows; the new
locations in the merkle tree are listed for nebulous-side
consumer rewrites.

| Old location                                               | New location                                                                              |
|------------------------------------------------------------|-------------------------------------------------------------------------------------------|
| Batch output `capturer.{name,version}`                     | Batch output `plugin.{name,version}` AND `identity → environment → binary.{name,version}` |
| Batch output `capabilities.{id,size,media_type}`           | `identity → environment → binary.capabilities_id` (markl-id only)                         |
| Per-capture `spec` ref                                     | `receipt → identity` ref (different shape; merkle-decomposed)                             |
| Per-capture `payload` ref                                  | `receipt → payload[]` ref                                                                  |
| Per-capture `envelope` ref                                 | `receipt → outcome` ref (different shape; merkle-decomposed)                              |
| Spec `capture.{format,options,isolation,split}`            | `identity → invocation.{format,options,target,normalize}` (no `isolation`; moved to plugin) AND `identity → environment → plugin.isolation` |
| Spec `browser.{name,version,user_agent,...,prefs,extensions,dns}` | `identity → environment → plugin.browser.*` + `.dns` + `.extensions`                  |
| Spec `host.{os,kernel,arch,libc,fonts_digest,gpu}`         | `identity → environment → host.{os,kernel,arch,libc}` (core); `fonts_digest`, `gpu` move under `identity → environment → plugin.browser.*` (web-specific) |
| Spec `capturer.{name,version,capabilities_id}`             | `identity → environment → binary.{name,version,capabilities_id}`                          |
| Envelope `url`                                             | DROPPED (redundant with `identity → invocation.target`)                                   |
| Envelope `captured_at`                                     | `outcome.datetime`                                                                          |
| Envelope `http.{status,headers,timing_ms,resolved_ip}`     | `outcome → plugin.http.*` (note: `headers` shape changes from map to array of `{name, value}` to preserve duplicate-header multiplicity and order; `timing_ms` sub-keys all become OPTIONAL) |
| Envelope `http.final_url` (chrest-emitted for redirect resolution) | `outcome → plugin.http.final_url` (now first-class in the schema)                          |
| Envelope `stripped.<format>`                               | `outcome.stripped.<format>`                                                                |

The `split` flag is replaced by `normalize`. Old `split=true` maps
to new `normalize=true`; old `split=false` maps to new
`normalize=false`. The semantic difference is that the outcome
artifact is now always emitted (RFC 0002 §Architecture Overview);
`normalize` controls only payload normalization, not envelope
emission.

### Environment/outcome node v2 — `command_line` relocation (2026-07-12)

`browser.command_line` moved from the identity tree's plugin node to
the outcome tree's plugin node (`process.command_line`), bumping both
type-strings per RFC 0010's horizontal-versioning rules:

| Node                                | v1                                     | v2 (current)                            |
|-------------------------------------|-----------------------------------------|------------------------------------------|
| `identity → environment → plugin`   | `!jcs-<plugin>-capture-environment-v1` (has `browser.command_line`) | `!jcs-<plugin>-capture-environment-v2` (field removed) |
| `outcome → plugin`                  | `!jcs-<plugin>-capture-outcome-v1` (no `process`) | `!jcs-<plugin>-capture-outcome-v2` (+ `process.command_line`) |

Rationale: real argv embeds per-launch values (a randomly generated
temp profile directory, discovery ports), so under v1 two
functionally identical captures **never** shared an identity
markl-id — defeating the identity/dedup model (found via chrest#102
during RFC 0008 conformance work). Identity fields must be stable
under re-run; argv is an observation of what ran. The configuration
signal argv was standing in for is carried by the structured
identity fields (`prefs`, `isolation`, `dns`, `extensions`).

Per RFC 0010: implementations MUST retain readers for both `-v1`
node types (published receipts referencing them are immutable), and
new captures MUST write `-v2`. No stored receipt is rewritten.

### Forward Compatibility

Adding new payload formats to the [catalog](#payload-format-catalog)
is a non-breaking change. New formats get new type-strings; old
consumers ignore them.

Adding new fields to existing plugin nodes follows RFC 0002's
forward-compatibility rules.

## References

### Normative References

- [RFC 2119: Key words for use in RFCs to Indicate Requirement Levels][rfc-2119]
- [cutting-garden RFC 0002: Capture Plugin Protocol](./0002-capture-plugin-protocol.md)
- [dodder RFC 0001: Hyphence Serialization Format][dodder-rfc-0001-hyphence]
- [dodder FDR-0001: Object Locks][dodder-fdr-0001]

### Informative References

- [nebulous RFC 0001: Web Capture Archive Protocol][nebulous-rfc-0001]
  — the pre-merkle source of truth for web-specific field
  semantics; superseded by this RFC paired with RFC 0002.
- [chrest][chrest-repo] — the reference web-archive plugin
  implementation.

[rfc-2119]: https://www.rfc-editor.org/rfc/rfc2119
[nebulous-rfc-0001]: https://github.com/amarbel-llc/nebulous/blob/master/docs/rfcs/0001-web-capture-archive-protocol.md
[dodder-rfc-0001-hyphence]: https://github.com/friedenberg/dodder/blob/master/docs/rfcs/0001-hyphence-format.md
[dodder-fdr-0001]: https://github.com/friedenberg/dodder/blob/master/docs/features/0001-object-locks.md
[rfc-0002-iana]: ./0002-capture-plugin-protocol.md#iana-media-type-interface
[chrest-repo]: https://github.com/amarbel-llc/chrest
