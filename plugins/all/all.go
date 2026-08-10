// Package all blank-imports every standard in-repo cutting-garden plugin,
// so importing it once registers the full built-in plugin set. The in-repo
// binaries (cmd/cutting-garden, cmd/cg, cmd/cutting-garden-gen)
// blank-import this package from their main; cgapp.Build() itself does NOT
// (RFC 0009 §5 step 3), so an external binary built on the SDK
// (pkgs/cgapp.Build) inherits only the plugins it explicitly links.
//
// This is the single source of the built-in plugin set — the three in-repo
// mains import it rather than each listing the plugins, so they cannot
// drift apart (the anti-drift role cgapp.Build plays for subcommands).
//
// It lives OUTSIDE internal/ on purpose (the no-inversion rule,
// RFC 0009 §4): as the in-tree plugins migrate to plugins/<scheme>/ and
// begin consuming the pkgs/ facade, an aggregator under internal/ would
// force internal/ -> pkgs/. Keeping it here keeps that edge legal. Update
// the import list as each plugin migrates out of internal/.
package all

import (
	// All plugins now live outside internal/ and consume the pkgs/ SDK
	// (RFC 0009 §5 migration complete).
	_ "code.linenisgreat.com/cutting-garden/plugins/caldav"
	_ "code.linenisgreat.com/cutting-garden/plugins/fastmail"
	_ "code.linenisgreat.com/cutting-garden/plugins/file"
	_ "code.linenisgreat.com/cutting-garden/plugins/git"
	_ "code.linenisgreat.com/cutting-garden/plugins/googlephotos"
	_ "code.linenisgreat.com/cutting-garden/plugins/jira"
	_ "code.linenisgreat.com/cutting-garden/plugins/optical"
	_ "code.linenisgreat.com/cutting-garden/plugins/ytdlp"
)
