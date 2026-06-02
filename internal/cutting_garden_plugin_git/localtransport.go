package cutting_garden_plugin_git

import (
	"path/filepath"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

// init installs an in-process server as go-git's "file" transport so that
// capturing or diffing a LOCAL git path serves objects directly from the
// repository's object database instead of spawning `git-upload-pack`. This
// removes the plugin's last `git`-binary dependency: network remotes
// (http/ssh/git://) are already pure-Go, and local paths now are too.
//
// go-git's stock file transport (plumbing/transport/file) execs
// git-upload-pack, and its bundled server.NewFilesystemLoader only resolves
// bare repositories (it roots the storer at the endpoint path, so a
// non-bare repo's `.git` is missed). localRepoLoader handles both layouts.
// The in-process server still performs `have`/`want` negotiation, so the
// seeded-storer delta fetch behind diff and incremental capture keeps
// transferring only the delta for local sources too.
func init() {
	client.InstallProtocol("file", server.NewClient(localRepoLoader{}))
}

// localRepoLoader resolves a local filesystem path to its git object store,
// supporting both bare repositories (metadata at the path root) and
// non-bare repositories (metadata under .git).
type localRepoLoader struct{}

func (localRepoLoader) Load(ep *transport.Endpoint) (storer.Storer, error) {
	if s, ok := loadGitDir(ep.Path); ok {
		return s, nil
	}
	if s, ok := loadGitDir(filepath.Join(ep.Path, ".git")); ok {
		return s, nil
	}
	return nil, transport.ErrRepositoryNotFound
}

// loadGitDir returns a filesystem storer rooted at dir when dir looks like a
// git object store (a `config` file is present), else ok=false.
func loadGitDir(dir string) (storer.Storer, bool) {
	fs := osfs.New(dir)
	if _, err := fs.Stat("config"); err != nil {
		return nil, false
	}
	return filesystem.NewStorage(fs, cache.NewObjectLRUDefault()), true
}
