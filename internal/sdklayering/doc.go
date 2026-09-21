// Package sdklayering holds module-layering invariant checks for the
// plugin SDK (RFC 0009 §4) and for the godyn invalidation-cone splits
// that keep a busy package's edits off unrelated dependents
// (docs/plans/2026-09-21-invalidation-cone-moves.md). It carries no
// runtime code — only tests that assert structural rules about the import
// graph. The package lives under internal/ because the rules it enforces
// are about internal/ itself.
package sdklayering
