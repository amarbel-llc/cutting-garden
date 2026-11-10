package org

import "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

var _ cutting_garden_plugins.ConcurrentReader = Plugin{}

// MaxConcurrentReads lets a consumer overlap up to 4 of this plugin's reads
// (cutting-garden#313). The read paths are safe under concurrent callers: the
// parse cache and the password map are locked, a parsed file is read-only once
// built, and concurrent loads of one file share a single ETag confirmation
// (target.file's flight), so overlapping reads cost one PROPFIND and one GET
// per file per call, as a sequential walk does. The bound is small because the
// source is one WebDAV server, typically a hosted one.
func (Plugin) MaxConcurrentReads() int { return 4 }
