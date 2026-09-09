package diff

import (
	"fmt"

	"github.com/example42/piace/internal/model"
)

// rawResourceChange exists only inside the comparison and exclusion passes.
// Publication requires the explicit projection in publishChanges.
type rawResourceChange struct {
	Kind          model.ChangeKind
	Identity      model.ResourceIdentity
	Parameter     string
	Before, After any
	FileContent   *model.FileContentEvidence
	Fingerprint   string
}

func (rawResourceChange) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("raw comparison evidence cannot be serialized")
}

func (rawResourceChange) String() string { return "<raw comparison evidence>" }

func (rawResourceChange) GoString() string { return "<raw comparison evidence>" }
