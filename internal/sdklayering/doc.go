// Package sdklayering holds module-layering invariant checks for the
// plugin SDK (RFC 0009 §4). It carries no runtime code — only tests that
// assert structural rules about the import graph. The package lives under
// internal/ because the rules it enforces are about internal/ itself.
package sdklayering
