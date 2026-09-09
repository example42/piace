package diff

import (
	"reflect"

	"github.com/example42/piace/internal/model"
)

// fidelity records whether each side of a comparison holds Pcore rich
// data or the lossy strings Puppet's PuppetDB terminus stores in its
// place. See model.ProjectStringifiedRich for the mechanism and the
// measurement.
//
// When both sides agree, comparison is exact and this does nothing. The
// interesting case is a PuppetDB baseline against a compiled candidate,
// which is the ordinary configuration: one side holds
// `{"__ptype":"Regexp","__pvalue":"^abc$"}` and the other holds
// `"/^abc$/"`, and the only comparison available is at the poorer
// fidelity of the two.
type fidelity struct {
	before, after bool
}

// equal reports whether two values that are not identical are the same
// value seen at two fidelities.
//
// The second return value names a rich type whose stringification this
// codebase has not measured, when one stood in the way of answering.
// The caller reports the difference in that case, which is the safe
// direction: a value PIACE cannot project is a value it cannot claim is
// unchanged.
func (f fidelity) equal(before, after model.Value) (bool, string) {
	if f.before == f.after {
		return false, ""
	}
	rich, stringified := after, before
	if f.after {
		rich, stringified = before, after
	}
	if !model.ContainsRichData(rich) {
		// The two differ for an ordinary reason, and no projection
		// would change that.
		return false, ""
	}
	projected, unmeasured := model.ProjectStringifiedRich(rich)
	if unmeasured != "" {
		return false, unmeasured
	}
	return reflect.DeepEqual(projected, stringified), ""
}

// fidelityDiagnostic reports that a parameter could not be compared
// because one side holds a rich type whose stringified form this
// codebase has not measured. The difference is reported alongside it:
// this says why it may not be one, not that it is not.
//
// The severity is a warning and the operation is normalize. It describes
// the wire representations the two catalogs arrived in, which is
// normalization's subject, and it is not a failure of the run.
func fidelityDiagnostic(certname string, identity model.ResourceIdentity, parameter, typeName string) model.Diagnostic {
	return model.Diagnostic{
		Severity:  model.SeverityWarning,
		Operation: model.OperationNormalize,
		Certname:  certname,
		Message: identity.String() + " " + parameter + ": the two catalogs hold this value at different fidelities, " +
			"and the stringified form of Pcore " + typeName + " has not been measured, so a difference here may be " +
			"representation rather than change",
	}
}
