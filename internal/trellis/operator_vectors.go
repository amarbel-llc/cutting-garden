package trellis

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
