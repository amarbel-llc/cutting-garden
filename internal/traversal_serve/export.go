package traversal_serve

// The public facade for this package lives at
// code.linenisgreat.com/cutting-garden/pkgs/traversal_serve. An
// out-of-tree Go wire plugin (RFC 0013 + RFC 0009) imports it for the
// plugin side of the traversal transport — CookieFromEnv,
// ListenRendezvous, AnnounceLine, Serve — so the same wire code speaks
// both ends of the session. (dagnabit exports the whole package
// surface, so the host side — Launch, Session, WirePlugin — rides
// along exactly as capture_serve's does; out-of-tree consumers host
// nothing and simply never call it.)
//
//go:generate dagnabit export
