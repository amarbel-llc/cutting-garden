---
author:
-
date: April 2026
title: CAPTURE-RECEIPT(7) cutting-garden \| Miscellaneous
---

# NAME

capture-receipt - cutting-garden filesystem-tree capture manifest format

# SYNOPSIS

    ---
    ! cutting_garden-capture_receipt-fs-v1
    ---

    {"path":".","root":"./src","type":"dir","mode":"0755"}
    {"path":"foo.go","root":"./src","type":"file","mode":"0644","size":1234,"blob_id":"<markl-id>"}
    {"path":"link","root":"./src","type":"symlink","mode":"0777","target":"../bar"}
    ...

# DESCRIPTION

A capture receipt is a hyphence-wrapped NDJSON manifest produced by
**cutting-garden capture** (see **cutting-garden**(1)). It enumerates every filesystem
entry visited under one capture-root or set of capture-roots, recording each
entry's relative path, type, POSIX permission bits, and either its
content-addressable blob ID (regular files) or its target string
(symbolic links).

Receipts are blobs themselves: every capture run writes its receipt
into the active store and reports the receipt's markl-id on stdout. A
single markl-id is therefore enough to enumerate every blob produced by
that run.

The format is hyphence (see **hyphence**(7)). The metadata block declares
the type tag **cutting_garden-capture_receipt-fs-v1**; the body is one JSON
object per line (NDJSON), one per filesystem entry.

# RECORD SCHEMA

Each line of the body is a single JSON object. Fields:

**path**
:   String. Path of the entry relative to its capture-root, with **/** as
    the separator regardless of host platform. The capture-root itself
    appears as **"."**.

**root**
:   String. The capture-root argument as the user typed it on the command
    line. Disambiguates entries when one receipt contains multiple roots.
    For a zero-arg capture (the default-store / PWD case) **root** is
    **"."**.

**type**
:   String. One of:

    * **"file"** --- regular file. Carries **size** and **blob_id**.
    * **"dir"** --- directory.
    * **"symlink"** --- symbolic link. Carries **target**; the target is
      not dereferenced.
    * **"other"** --- device, fifo, socket, or any other non-regular,
      non-directory, non-symlink entry. Carries no extra fields and no
      blob is written.

**mode**
:   String. POSIX permission bits in zero-padded octal, e.g. **"0644"**,
    **"0755"**, **"0007"**. Only **info.Mode().Perm()** bits are emitted
    --- type bits (ModeDir, ModeSymlink, etc.) are stripped because the
    **type** field already disambiguates them.

**size**
:   Integer. Bytes copied into the blob store. Present only for
    **type:"file"** entries.

**blob_id**
:   String. Content-addressable identifier (see **markl-id**(7)) of the
    blob produced from this file's bytes. Present only for **type:"file"**
    entries. Suitable for round-tripping through **madder cat**.

**target**
:   String. Literal **readlink**(2) result for the symlink. Present only
    for **type:"symlink"** entries. Not interpreted or normalized.

# DETERMINISM

Entries within a receipt are sorted lexicographically by **(root, path)**
before encoding. Two captures of identical input trees produce
byte-identical receipts and therefore identical receipt blob IDs. This
is the primary tool for verifying that two trees match.

The receipt does not include a timestamp, hostname, owner, or group
field --- determinism is preserved at the cost of provenance. Callers
that need provenance can wrap the receipt in their own outer envelope.

# WALK SEMANTICS

The walk underlying a receipt is **filepath.WalkDir** rooted at each
capture-root. Symbolic links are recorded but not followed; the linked
file's bytes are never copied. Hidden files (dotfiles) are captured.
Special files (devices, fifos, sockets) appear with **type:"other"**
and no blob.

A symbolic link passed as a capture-root is rejected with an error ---
**capture** uses **lstat**(2) when classifying its arguments and
will not silently produce a one-entry symlink receipt. Resolve such
arguments with **realpath**(1) (or pass the linked directory directly)
if the symlink's target is what you wanted.

# READING A RECEIPT

A receipt blob is plain text. To read one:

    madder cat <receipt-markl-id>

The hyphence header occupies the first three lines plus a blank
separator; the remaining lines are NDJSON records. To extract just the
file entries from a receipt:

    madder cat <receipt-markl-id> | tail -n +5 | jq 'select(.type=="file")'

To list every blob the receipt references:

    madder cat <receipt-markl-id> | tail -n +5 | jq -r 'select(.blob_id) | .blob_id'

# VERSIONING

The type tag carries a version suffix (**-v1**). New schema versions
introduce a new tag (**-v2**, etc.); old tags remain decodable
indefinitely. A consumer that does not recognize a receipt's tag
should refuse to interpret it rather than guess.

# EXAMPLES

A receipt for a small two-file directory:

    ---
    ! cutting_garden-capture_receipt-fs-v1
    ---

    {"path":".","root":"./src","type":"dir","mode":"0755"}
    {"path":"main.go","root":"./src","type":"file","mode":"0644","size":482,"blob_id":"blake2b256-9ft3m74l5t…"}
    {"path":"go.mod","root":"./src","type":"file","mode":"0644","size":92,"blob_id":"blake2b256-2ppwjrvfg3…"}

A receipt covering a symlink and a non-regular file:

    ---
    ! cutting_garden-capture_receipt-fs-v1
    ---

    {"path":".","root":"vendor","type":"dir","mode":"0755"}
    {"path":"latest","root":"vendor","type":"symlink","mode":"0777","target":"v1.2.3"}
    {"path":"v1.2.3","root":"vendor","type":"dir","mode":"0755"}
    {"path":"v1.2.3/lib.so","root":"vendor","type":"file","mode":"0755","size":18432,"blob_id":"blake2b256-wp380jqj2z…"}

# SEE ALSO

**cutting-garden**(1), **madder**(1), **hyphence**(7), **markl-id**(7), **blob-store**(7)
