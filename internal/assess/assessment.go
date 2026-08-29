package assess

// Risk is a change assessment's closed-enum judgement for one aggregate
// group or for the run. It is a model's opinion about a change, not a
// measurement of it, and it never affects a comparison outcome or exit
// status.
type Risk string

const (
	RiskLow     Risk = "low"
	RiskMedium  Risk = "medium"
	RiskHigh    Risk = "high"
	RiskUnknown Risk = "unknown"
)

// Valid reports whether r is one of the four permitted risk indications.
// Anything else that arrives from an inference service becomes
// RiskUnknown plus a diagnostic; it never reaches a renderer as prose.
func (r Risk) Valid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh, RiskUnknown:
		return true
	}
	return false
}
