//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// Reject local struct copies made by dereferencing a model pointer.
func defensiveModelCopy(m dsl.Matcher) {
	m.Match(
		`$copy := *$model`,
		`$copy = *$model`,
		`var $copy = *$model`,
		`var $copy $typ = *$model`,
	).
		Where(m["copy"].Node.Is("Ident") && m["copy"].Type.Underlying().Is(`struct{$*_}`)).
		Report("defensive model copy: mutate the model pointer directly")
}

// Reject copying an existing struct into another model through its pointer.
func modelPointerAssignment(m dsl.Matcher) {
	m.Match(`*$destination = $model`).
		Where(m["model"].Node.Is("Ident") && m["model"].Type.Underlying().Is(`struct{$*_}`)).
		Report("defensive model copy: populate or validate the existing model pointer directly")
}
