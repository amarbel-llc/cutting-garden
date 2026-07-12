package capture_serve

// The public facade for this package lives at
// code.linenisgreat.com/cutting-garden/pkgs/capture_serve. An
// out-of-tree capture plugin (chrest's capture-serve subcommand) imports
// it for the plugin side of the RFC 0008 transport — CookieFromEnv,
// ListenRendezvous, AnnounceLine, Serve — so the same wire code speaks
// both ends of the session.
//
//go:generate dagnabit export
