package org

import (
	"net/url"
	"strconv"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// nodeKind is what an org:// URI addresses.
type nodeKind int

const (
	kindFolder nodeKind = iota
	kindFile
	kindHeadline
)

// nodeRef is a classified org:// URI (FDR 0026 D1):
//
//	org://<account>/                       the account root folder
//	org://<account>/<dir>/…/               a folder
//	org://<account>/<path>.org             a file
//	org://<account>/<path>.org?id=<ID>     a headline by its :ID:
//	org://<account>/<path>.org?h=<a/b>     a headline by outline path
//	                         [&n=<k>]      k-th (1-based) of same-path siblings
type nodeRef struct {
	account string
	kind    nodeKind
	path    string // slash path relative to the account root; folders end in "/"
	id      string
	outline []string
	nth     int // 1 when absent
}

// classifyURI parses an org:// URI. A malformed URI is a bad request.
func classifyURI(u *url.URL) (nodeRef, error) {
	if u.Scheme != schemeOrg {
		return nodeRef{}, errors.BadRequestf("org plugin: not an org:// URI: %q", u.String())
	}
	if u.Host == "" {
		return nodeRef{}, errors.BadRequestf("org plugin: %q names no account", u.String())
	}
	ref := nodeRef{account: u.Host, nth: 1}
	p := strings.TrimPrefix(u.Path, "/")
	q := u.Query()

	switch {
	case p == "" || strings.HasSuffix(p, "/"):
		if len(q) > 0 {
			return nodeRef{}, errors.BadRequestf("org plugin: a folder URI takes no query: %q", u.String())
		}
		ref.kind, ref.path = kindFolder, p
	case strings.HasSuffix(p, ".org"):
		ref.path = p
		ref.kind = kindFile
		if id, h := q.Get("id"), q.Get("h"); id != "" || h != "" {
			ref.kind = kindHeadline
			if id != "" && h != "" {
				return nodeRef{}, errors.BadRequestf("org plugin: %q has both id= and h=", u.String())
			}
			ref.id = id
			if h != "" {
				for _, seg := range strings.Split(h, "/") {
					title, err := url.PathUnescape(seg)
					if err != nil {
						return nodeRef{}, errors.BadRequestf("org plugin: bad h= segment %q in %q", seg, u.String())
					}
					ref.outline = append(ref.outline, title)
				}
			}
			if n := q.Get("n"); n != "" {
				k, err := strconv.Atoi(n)
				if err != nil || k < 1 {
					return nodeRef{}, errors.BadRequestf("org plugin: bad n=%q in %q", n, u.String())
				}
				ref.nth = k
			}
		}
	default:
		return nodeRef{}, errors.BadRequestf(
			"org plugin: %q is neither a folder (trailing /) nor a .org file", u.String())
	}
	return ref, nil
}

// folderURI mints org://<account>/<dir>/ (dir "" for the root).
func folderURI(account, dir string) *url.URL {
	u := &url.URL{Scheme: schemeOrg, Host: account, Path: "/" + dir}
	if dir != "" && !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u
}

// fileURI mints org://<account>/<path>.org.
func fileURI(account, path string) *url.URL {
	return &url.URL{Scheme: schemeOrg, Host: account, Path: "/" + path}
}

// headlineURI mints a headline URI: by :ID: when it has one, else by its
// outline path (titles joined by "/", a title's own "/" and "%"
// percent-escaped), with n=k when it is the k-th (k>1)
// sibling sharing that path.
func headlineURI(account, path, id string, outline []string, nth int) *url.URL {
	u := fileURI(account, path)
	q := url.Values{}
	if id != "" {
		q.Set("id", id)
	} else {
		segs := make([]string, len(outline))
		for i, title := range outline {
			// Escape so a "/" inside a title cannot split the path.
			segs[i] = strings.ReplaceAll(strings.ReplaceAll(title, "%", "%25"), "/", "%2F")
		}
		q.Set("h", strings.Join(segs, "/"))
		if nth > 1 {
			q.Set("n", strconv.Itoa(nth))
		}
	}
	u.RawQuery = q.Encode()
	return u
}
