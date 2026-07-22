package cutting_garden_plugin_file

import (
	"net/url"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// pathFromURL returns the filesystem path encoded in u.
//
// Accepted forms:
//   - schemeless: u.Scheme == "" — path is u.Path or u.Opaque.
//   - file:///abs/path — u.Host == "" or "localhost", u.Path is absolute.
//   - file:relative — u.Opaque is the path; u.Path is empty.
//
// Rejected forms:
//   - non-empty scheme other than "file".
//   - host other than "" or "localhost" (e.g. file://example.com/x is
//     not supported; we have no remote-fs semantics).
func pathFromURL(u *url.URL) (string, error) {
	// Malformed source URIs are CALLER mistakes: bad requests
	// (errors.Is400BadRequest true, -32602 over the RFC 0013 wire), not
	// plugin failures that invite a futile retry (cutting-garden#187).
	if u.Scheme != "" && u.Scheme != "file" {
		return "", errors.BadRequestf(
			"file plugin: unsupported scheme %q in %q",
			u.Scheme, u.String(),
		)
	}

	if u.Host != "" && u.Host != "localhost" {
		return "", errors.BadRequestf(
			"file plugin: file:// with non-empty host is not supported: %q",
			u.String(),
		)
	}

	if u.Path != "" {
		return u.Path, nil
	}
	return u.Opaque, nil
}
