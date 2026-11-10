package trellis

import "strings"

// CompareFieldOp applies a non-regex field operator to one field value got
// and one predicate value want: bytes compare for the ordering operators,
// exact for `=`/`!=`, substring/prefix/suffix for `*=`/`^=`/`$=`. It is the
// one operator semantics both evaluators of field predicates use
// (internal/trellis_eval over plugin trees, pkgs/ajour over stencils); `~=`
// is the caller's, since each caches compiled patterns its own way, and
// answers false here.
func CompareFieldOp(op FieldOp, got, want string) bool {
	switch op {
	case FieldOpEq:
		return got == want
	case FieldOpNotEq:
		return got != want
	case FieldOpContains:
		return strings.Contains(got, want)
	case FieldOpPrefix:
		return strings.HasPrefix(got, want)
	case FieldOpSuffix:
		return strings.HasSuffix(got, want)
	case FieldOpLt:
		return got < want
	case FieldOpLte:
		return got <= want
	case FieldOpGt:
		return got > want
	case FieldOpGte:
		return got >= want
	default:
		return false
	}
}

// ValueText is a predicate value's text, the string the operators compare:
// a String's or Bareword's content, `@digest`, `purpose@digest`; empty for a
// Qualifier.
func ValueText(v Value) string {
	switch t := v.(type) {
	case StringValue:
		return t.Value
	case Bareword:
		return t.Name
	case DigestTerm:
		return "@" + t.Digest
	case MarklTerm:
		return t.Purpose + "@" + t.Digest
	default:
		return ""
	}
}

// An OperatorVector is one row of the field-operator conformance table: does
// got, a field's value, satisfy the operator against want, one predicate
// value?
type OperatorVector struct {
	Op    FieldOp
	Got   string
	Want  string
	Holds bool
}

// OperatorVectors is the one table both evaluators of trellis field
// predicates run (cutting-garden FDR 0028 vector A-16): internal/trellis_eval
// over plugin trees and pkgs/ajour over capability stencils. It pins that
// they keep one operator semantics: bytes compare for the ordering
// operators, RE2 unanchored for `~=`.
var OperatorVectors = []OperatorVector{
	{FieldOpEq, "master", "master", true},
	{FieldOpEq, "Master", "master", false},
	{FieldOpNotEq, "dev", "master", true},
	{FieldOpNotEq, "master", "master", false},
	{FieldOpContains, "origin/main", "gin/m", true},
	{FieldOpContains, "origin/main", "x", false},
	{FieldOpPrefix, "origin/main", "origin/", true},
	{FieldOpPrefix, "main", "origin/", false},
	{FieldOpSuffix, "a.go", ".go", true},
	{FieldOpSuffix, "a.rs", ".go", false},
	{FieldOpLt, "20260131", "20260201", true},
	{FieldOpLt, "9", "10", false},
	{FieldOpLte, "b", "b", true},
	{FieldOpGt, "b", "a", true},
	{FieldOpGte, "a", "b", false},
	{FieldOpRegex, "v12", "^v[0-9]+$", true},
	{FieldOpRegex, "v1x", "^v[0-9]+$", false},
	{FieldOpRegex, "xv1y", "v[0-9]", true},
}
