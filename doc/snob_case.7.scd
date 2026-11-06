snob_case(7)

# NAME

snob_case - hyphen splits a name into segments, underscore does not

# DESCRIPTION

*snob_case* is the naming convention cutting-garden's tag vocabularies use,
a combination of snake case and kebab case: a hyphen (*-*) is a segment
BOUNDARY, and an underscore (*\_*) is an ordinary character INSIDE a segment.
It is the *dodder-hyphen* tag interpreter's reading of a tag (RFC 0019 §6, §7;
*docs/rfcs/0019-tag-interpreter-contract.md* is the authority, and this page
does not restate it). Under the *naive* interpreter a tag is one opaque
string and none of this applies.

# THE RULES

*A hyphen separates segments.* The segments form a path:

	project-client-acme

is *project*, then *client*, then *acme*.

*An underscore is literal.* It is part of a segment and never splits it,
anywhere in a name: a leading underscore, a leading underscore and a space
(the pin-to-top convention), and an in-word underscore are all ordinary
characters, and no interpreter rewrites, lifts or aliases them. *\_inbox* and
*inbox* are different tags, and *\_ inbox* is a third.

```
friends_and_family      one segment
friends-and-family      three segments: friends, and, family
```

*A leading hyphen marks a continuation* in an organize document: a heading
that is the rest of a name whose start is already on the page above it (see
CONTINUATION HEADINGS). It is a presentation of the hierarchy, not part of
the stored name.

# GROUPING AND MATCHING

Grouping by a namespace buckets each tag by the IMMEDIATE next segment under
it; a tag deeper than that rolls up to its immediate child. Over the tags

```
project-cutting_garden
project-client-acme
project-client-baxter
```

grouping by *project* gives two buckets, *-cutting_garden* and *-client*
(the last two roll up into it). Drilling down is grouping by the deeper
namespace: *project-client* separates *-acme* from *-baxter*. A tag exactly
equal to the namespace (*project*) has no segment beneath it, so it sits
directly under the namespace's own heading rather than in a bucket.

A bare term matches along whole segments, from the front: *project* matches
*project-client-acme*, and *project-client* matches it too but does not match
*project-cutting_garden*.

Moving an object under a rollup bucket adds the NAMESPACE tag of that bucket
(*project-client* for the *-client* bucket of a *project* grouping), never a
deeper one: the grouping names no leaf, so the write cannot invent one. To
place an object at a leaf, group one level deeper.

# CONTINUATION HEADINGS

*cutting-garden-organize*(1) renders a namespace grouping's buckets as
continuations of the namespace, with the shared prefix elided and no equals
sign:

```
# project
## -client
## -cutting_garden
```

A flat tag renders bare (*## work*), and quoted when it holds a space
(*## "\_ inbox"*). The *(tree)* layout nests a plugin's nodes by their own
full hyphen names the same way, one heading per segment: *trips-26-01-a* is
the heading *trips*, then *-26*, then *-01*. A heading is a box when a node
of exactly that name exists, and a synthetic heading when none does.

# THE PITFALL

A hyphen inside a name is ALWAYS a boundary, whether or not it was meant as
part of one word. *prior-years* is *prior* then *years*, and in a nested
layout it files *years* under *prior*. A name that is one segment but reads
as several words takes an underscore instead: *prior_years*, *2025_records*.
Tags written as kebab case before this convention was settled split wrongly
for exactly this reason; the tag itself has to be renamed, since the
splitting happens wherever the hyphen is.

# SEE ALSO

*cutting-garden-organize*(1), *cutting-garden-plugins*(7)
