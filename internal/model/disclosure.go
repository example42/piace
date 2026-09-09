package model

import "sort"

// pcoreTypeKey and SensitiveWrapperType are the reserved Pcore
// generic-data keys Puppet's Ruby serializer emits for a
// Sensitive-wrapped value. They live in this package rather than in the
// differ that first needed them because two stages have to recognize the
// same shape: the differ, which redacts it on the way into a report, and
// the reader of a stored report, which must refuse a document carrying
// one. A second copy of the literals would be a second definition of
// what counts as sensitive.
const (
	pcoreTypeKey = "__ptype"
	// SensitiveWrapperType is the `__ptype` value naming a wrapped
	// sensitive value.
	SensitiveWrapperType = "Sensitive"
)

// IsSensitiveWrapper reports whether m is a Pcore Sensitive wrapper.
func IsSensitiveWrapper(m map[string]Value) bool {
	ptype, ok := m[pcoreTypeKey]
	if !ok {
		return false
	}
	name, ok := ptype.(string)
	return ok && name == SensitiveWrapperType
}

// ContainsSensitive reports whether v is, or contains at any depth, a
// Pcore Sensitive wrapper. A value that does has not been through
// redaction, whatever else is true of it.
func ContainsSensitive(v any) bool {
	switch v := v.(type) {
	case map[string]any:
		if IsSensitiveWrapper(v) {
			return true
		}
		for _, child := range v {
			if ContainsSensitive(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if ContainsSensitive(child) {
				return true
			}
		}
	}
	return false
}

// FileResourceType is the exact Puppet resource type whose content-bearing
// parameters are reported as one unit and never published as values.
const FileResourceType = "File"

// fileContentBearingParameters is the exact set of File parameter names
// internal/filecontent inspects. A published report carries evidence
// about them (state, evidence source, digests) but never their values:
// `content` is the managed bytes themselves, and `source`, `checksum`
// and `checksum_value` are how those bytes are identified.
var fileContentBearingParameters = map[string]bool{
	"content":        true,
	"source":         true,
	"checksum":       true,
	"checksum_value": true,
}

// FileContentBearingParameter reports whether name is one of the File
// parameters whose value never reaches a published report.
func FileContentBearingParameter(name string) bool {
	return fileContentBearingParameters[name]
}

// FileContentBearingParameters lists those parameter names in a fixed
// order, for a caller that has to iterate them rather than test one.
func FileContentBearingParameters() []string {
	names := make([]string, 0, len(fileContentBearingParameters))
	for name := range fileContentBearingParameters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
