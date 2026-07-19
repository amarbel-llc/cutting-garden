cutting-garden-config(5)

# NAME

cutting-garden-config - wire-plugin stanza schema for cutting-garden's config.toml

# SYNOPSIS

*$XDG_CONFIG_HOME*/cutting-garden/config.toml

```
[[traversal_plugins]]
name = "fj"
command = ["fj-cg", "traversal-serve"]
schemes = ["fj"]
config_section = "fj"

[[plugins]]
name = "web"
command = ["chrest"]
schemes = ["web"]
protocols = ["capture"]
```

# DESCRIPTION

cutting-garden reads a single TOML file at *$XDG_CONFIG_HOME*/cutting-garden/config.toml
(falling back to *$HOME*/.config on Linux when *$XDG_CONFIG_HOME* is unset). A
missing file is a valid, empty config — cutting-garden runs with only its
built-in file-scheme plugin. This is the RFC 0007 configuration subsystem;
see *cutting-garden-plugins*(7) for the concepts behind what this file
configures.

This page documents ONE part of that file: the *[[traversal_plugins]]* and
*[[plugins]]* stanzas, which declare OUT-OF-PROCESS wire plugins (RFC 0013 -
the traversal transport - and RFC 0008 - the capture transport). It does
NOT document the sections owned by cutting-garden's own LINKED (in-process)
plugins, such as *[caldav]*/*[[caldav.accounts]]* or *[jira]*/*[[jira.accounts]]*.
Those are a different, in-tree contract, decoded directly by cutting-garden
itself rather than handed to a spawned plugin.

A malformed *[[traversal_plugins]]*/*[[plugins]]* entry aborts config loading
with an EX_USAGE (64) exit, naming the stanza and the problem.

# THE STANZA FIELDS

Each *[[traversal_plugins]]* or *[[plugins]]* entry is a TOML table with these
keys:

*name*
	String. REQUIRED. Identifies the stanza in diagnostics and is the default
	*config_section*. MUST be unique across every stanza in the file
	(*[[traversal_plugins]]* and *[[plugins]]* share one namespace) and MUST
	match the bare-TOML-key grammar (letters, digits, underscore, hyphen).

*command*
	Array of strings. REQUIRED, non-empty. The argv used to spawn the plugin,
	resolved via *$PATH* when the first element is not an absolute path. Its
	MEANING depends on which table the stanza is under - see *THE ARGV
	DISTINCTION* below, the part people get wrong.

*schemes*
	Array of strings. REQUIRED, non-empty. The URI schemes routed to this
	plugin. Validated against the plugin's own *initialize* echo at first
	spawn (RFC 0013 §Handshake); a mismatch is rejected. A scheme claimed
	by two stanzas, or by a stanza and a linked (in-process) plugin, is a
	configuration error at startup.

*config_section*
	String. OPTIONAL, defaults to *name*. Names the top-level table (elsewhere
	in this SAME file) holding this plugin's OWN configuration - see *WHERE A
	PLUGIN'S OWN CONFIG SECTION LANDS* below. MUST match the bare-TOML-key
	grammar when set.

*protocols*
	Array of strings. OPTIONAL. Which wire protocol(s) the plugin binary
	speaks: *"traversal"* (RFC 0013) and/or *"capture"* (RFC 0008). Omitted or
	empty defaults to *["traversal"]*. A single stanza declaring BOTH
	protocols is rejected today (not yet supported) - use two stanzas, one
	per protocol, if a binary speaks both.

# THE ARGV DISTINCTION

This is the field people get wrong, because *command*'s shape is different
between the two tables:

- A *[[traversal_plugins]]* entry's *command* is the FULL argv VERBATIM,
  including whatever subcommand the plugin needs (e.g.
  *["fj-cg", "traversal-serve"]*). cutting-garden appends nothing; it runs
  exactly what is written.
- A *[[plugins]]* entry's *command* is the BASE binary invocation, with NO
  protocol subcommand (e.g. *["chrest"]*, not *["chrest", "capture-serve"]*).
  cutting-garden itself appends the subcommand per *protocols*:
  *"traversal-serve"* for the traversal protocol; for the capture protocol it
  tries *"capture-serve"* first and falls back to *"capture-batch"* (RFC 0008
  §Migration) if that fails.

*[[traversal_plugins]]* is a compatibility alias predating *protocols*; every
entry there implicitly defaults to *protocols = ["traversal"]* and keeps its
original verbatim-argv meaning. New configs speaking only the traversal
protocol MAY use either table; a plugin that also (or only) speaks the
capture protocol MUST use *[[plugins]]*, because the verbatim-argv form
cannot express the capture side's serve/batch fallback choice.

# WHERE A PLUGIN'S OWN CONFIG SECTION LANDS

When *config_section* (or the *name* it defaults to) is set, cutting-garden
looks for a top-level table of that name ELSEWHERE in this same config.toml
and hands it to the plugin, WRAPPER-STRIPPED, as part of the *initialize*
handshake (RFC 0013 §Handshake's *config_toml* parameter): the section's
own header disappears and its keys become top-level. A section named *[fj]*
with entries under *[[fj.roots]]* arrives at the plugin as:

```
[[roots]]
name = "forge"
url = "fj://forge.example/"
```

The plugin never sees its own section name. cutting-garden does not
interpret this text at all - it is passed through verbatim, and the plugin
parses and validates it with its own decoder. Because wrapper-stripping only
knows how to remove ONE header, *config_section* MUST be a bare top-level
key: a plugin wanting several repeated tables of its own uses a sub-array
under its section (*[[<config_section>.something]]*), never a bare top-level
*[[<config_section>]]* array-of-tables.

# A PLUGIN'S OWN SECTION IS ITS CONTRACT, NOT OURS

Everything above - *name*, *command*, *schemes*, *config_section*,
*protocols* - is cutting-garden's own, host-side contract, and this page is
the source of truth for it. The CONTENTS of *config_section*'s table are
not. That shape belongs entirely to the plugin being configured, and this
page (like RFC 0013's own illustrative examples, and every worked example
below) can only show what one plugin happened to accept - never what every
plugin accepts.

This is not a hypothetical caution. In production, forgejo-cli's *fj-cg*
plugin's root-entry type deserializes only a *uri* field - it has NO *name*
field, unlike the *name*/*url* shape shown as an example in RFC 0013's own
host-integration section (and echoed in the worked example below). A
deployer who copied that illustrative shape instead of checking fj-cg's own
struct got a config that failed fj-cg's *initialize* handshake in
production.

Before writing a *config_section* table for a given plugin, read THAT
PLUGIN's own documentation or config-decoding source (its struct
definitions, its own man page or README) - never an example from this page,
from RFC 0013, or from any other host-side document. Those examples exist to
show the STANZA shape (the fields above); they are not a promise about any
particular plugin's section shape.

# WORKED EXAMPLE

```
[[traversal_plugins]]
name = "fj"
command = ["fj-cg", "traversal-serve"]
schemes = ["fj"]
config_section = "fj"      # optional; defaults to "fj" anyway

# fj-cg's OWN section, per fj-cg's OWN source - illustrative only, verify
# against the plugin you are actually configuring:
[[fj.roots]]
uri = "fj://forge.example/linenisgreat/cutting-garden"
```

```
[[plugins]]
name = "web"
command = ["chrest"]        # base invocation - no subcommand
schemes = ["web"]
protocols = ["capture"]
```

The first stanza registers *fj-cg* on the *fj* scheme via the traversal
protocol, with its own root list handed through in *[[fj.roots]]*. The
second registers *chrest* on the *web* scheme via the capture protocol,
letting cutting-garden choose *capture-serve*/*capture-batch* itself.

# SEE ALSO

*cutting-garden-plugins*(7), *cutting-garden*(1), *cutting-garden-list*(1),
*cutting-garden-mcp*(1)
