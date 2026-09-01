package puppetdb

import (
	"encoding/json"
	"fmt"

	"github.com/example42/piace/internal/snapshot"
)

// FactsetIdentity computes a stable identity for fs: the snapshot
// package's SHA-256 canonical-JSON checksum over fs's own encoding. This
// is used as a catalog envelope's `input_factset_identity` and as a
// candidate request's model.CandidateProvenance.FactsetIdentity,
// regardless of whether fs came from PuppetDB or a file-backed snapshot
// — unlike fs.Hash (which PuppetDB computes server-side and which a
// file-backed Factset may not populate at all), this value is always
// computable from the Factset carrier alone.
//
// Exported from this package (rather than internal/capture, where it
// first appeared) so both the capture workflow and the compiler adapter
// (internal/compiler) compute a target's factset identity with exactly
// one algorithm, instead of two independently-written copies drifting
// apart.
func FactsetIdentity(fs Factset) (string, error) {
	payload, err := json.Marshal(fs)
	if err != nil {
		return "", fmt.Errorf("encoding factset for identity: %w", err)
	}
	sum, err := snapshot.Checksum(payload)
	if err != nil {
		return "", fmt.Errorf("computing factset identity: %w", err)
	}
	return sum, nil
}
