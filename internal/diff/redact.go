package diff

import (
	"github.com/example42/piace/internal/config"
	"github.com/example42/piace/internal/model"
)

// publishChanges is the only conversion from raw comparison evidence to
// the report model. Sensitivity from either catalog applies to both sides.
func publishChanges(selectors []config.RedactionSelector, changes []rawResourceChange, before, after map[model.ResourceIdentity]model.Resource) []model.ResourceChange {
	out := make([]model.ResourceChange, len(changes))
	for i, raw := range changes {
		change := model.ResourceChange{Kind: raw.Kind, Identity: raw.Identity, Parameter: raw.Parameter, Fingerprint: raw.Fingerprint}
		br, ar := before[raw.Identity], after[raw.Identity]
		selected := parameterSensitive(br, raw.Parameter) || parameterSensitive(ar, raw.Parameter) || matchesRedactionSelector(selectors, raw.Identity.Type, raw.Parameter)
		switch raw.Kind {
		case model.ChangeResourceAdded:
			change.After = publishParameters(selectors, ar)
		case model.ChangeResourceRemoved:
			change.Before = publishParameters(selectors, br)
		case model.ChangeParameterChanged:
			if raw.FileContent == nil {
				if selected {
					change.Before, change.After = model.RedactedValue, model.RedactedValue
				} else {
					change.Before, change.After = redactSensitivePair(raw.Before, raw.After)
				}
			}
		}
		if raw.FileContent != nil {
			for _, name := range model.FileContentBearingParameters() {
				selected = selected || parameterSensitive(br, name) || parameterSensitive(ar, name) || model.ContainsSensitive(br.Parameters[name]) || model.ContainsSensitive(ar.Parameters[name]) || matchesRedactionSelector(selectors, raw.Identity.Type, name)
			}
			evidence := *raw.FileContent
			if selected {
				evidence.Algorithm = ""
				if evidence.Before != nil {
					evidence.BeforeDigest = model.RedactedValue
				}
				if evidence.After != nil {
					evidence.AfterDigest = model.RedactedValue
				}
				evidence.Redacted = true
			}
			change.FileContent = &evidence
		}
		out[i] = change
	}
	return out
}

func publishParameters(selectors []config.RedactionSelector, resource model.Resource) map[string]any {
	out := make(map[string]any, len(resource.Parameters))
	for name, value := range resource.Parameters {
		if (resource.Identity.Type == fileResourceType && model.FileContentBearingParameter(name)) || parameterSensitive(resource, name) || matchesRedactionSelector(selectors, resource.Identity.Type, name) {
			out[name] = model.RedactedValue
		} else {
			out[name], _ = redactSensitivePair(value, value)
		}
	}
	return out
}

func parameterSensitive(resource model.Resource, name string) bool {
	for _, sensitive := range resource.SensitiveParameters {
		if sensitive == name {
			return true
		}
	}
	return false
}

// A wrapper on either side protects the corresponding value on both sides.
// Incompatible container shapes require masking the whole parameter because
// there is no reliable correspondence between their descendants.
func redactSensitivePair(before, after any) (any, any) {
	bm, bok := before.(map[string]any)
	am, aok := after.(map[string]any)
	if (bok && model.IsSensitiveWrapper(bm)) || (aok && model.IsSensitiveWrapper(am)) {
		return model.RedactedValue, model.RedactedValue
	}
	if bok && aok {
		names := unionParameterNames(bm, am)
		for _, key := range names {
			_, inBefore := bm[key]
			_, inAfter := am[key]
			if inBefore != inAfter && (model.ContainsSensitive(before) || model.ContainsSensitive(after)) {
				return model.RedactedValue, model.RedactedValue
			}
		}
		b, a := map[string]any{}, map[string]any{}
		for _, key := range names {
			bv, av := redactSensitivePair(bm[key], am[key])
			if _, ok := bm[key]; ok {
				b[key] = bv
			}
			if _, ok := am[key]; ok {
				a[key] = av
			}
		}
		return b, a
	}
	bs, bok := before.([]any)
	as, aok := after.([]any)
	if bok && aok && len(bs) == len(as) {
		b, a := make([]any, len(bs)), make([]any, len(as))
		for i := range bs {
			b[i], a[i] = redactSensitivePair(bs[i], as[i])
		}
		return b, a
	}
	if model.ContainsSensitive(before) || model.ContainsSensitive(after) {
		return model.RedactedValue, model.RedactedValue
	}
	return before, after
}

// matchesRedactionSelector reports whether any configured selector names
// this exact resource type and parameter name. Both comparisons are
// exact and case-sensitive. A change with no parameter name (a resource
// added/removed entry) never matches, since a selector always names a
// parameter.
func matchesRedactionSelector(selectors []config.RedactionSelector, resourceType, parameter string) bool {
	if parameter == "" {
		return false
	}
	for _, s := range selectors {
		if s.Type == resourceType && s.Parameter == parameter {
			return true
		}
	}
	return false
}
