package impact

import (
	"context"
	"testing"
	"time"

	"github.com/example42/piace/internal/config/resolve"
	"github.com/example42/piace/internal/model"
)

// recordingQuerier records every Estimate call in order without any I/O.
type recordingQuerier struct {
	calls []struct {
		identity model.ResourceIdentity
		limits   Limits
	}
	fail map[model.ResourceIdentity]bool
}

func (r *recordingQuerier) Estimate(_ context.Context, identity model.ResourceIdentity, limits Limits) (model.ImpactEstimate, *model.Diagnostic) {
	r.calls = append(r.calls, struct {
		identity model.ResourceIdentity
		limits   Limits
	}{identity, limits})
	estimate := model.ImpactEstimate{Identity: identity, ResultLimit: limits.ResultLimit, Timeout: limits.Timeout.String()}
	if r.fail[identity] {
		estimate.Status = model.ImpactStatusFailed
		estimate.FailureReason = "stub failure"
		return estimate, estimateDiagnostic(identity, "stub failure")
	}
	estimate.Status = model.ImpactStatusCompleted
	return estimate, nil
}

func impactTarget(certname string, enabled bool, timeout time.Duration, resultLimit int) resolve.Target {
	return resolve.Target{
		Certname: certname,
		ImpactEstimate: resolve.ImpactEstimate{
			Enabled:     enabled,
			Timeout:     timeout,
			ResultLimit: resultLimit,
		},
	}
}

func changed(certname string, identities ...model.ResourceIdentity) model.NodeDiff {
	nd := model.NodeDiff{Certname: certname, HasDifference: len(identities) > 0}
	for _, id := range identities {
		nd.ResourceChanges = append(nd.ResourceChanges, model.ResourceChange{
			Kind:      model.ChangeParameterChanged,
			Identity:  id,
			Parameter: "ensure",
		})
	}
	return nd
}

func id(resourceType, title string) model.ResourceIdentity {
	return model.ResourceIdentity{Type: resourceType, Title: title}
}

func TestEstimateAll_DeduplicatesIdentitiesRunWideAndSortsThem(t *testing.T) {
	targets := []resolve.Target{
		impactTarget("web-01", true, time.Second, 5),
		impactTarget("web-02", true, time.Second, 5),
	}
	diffs := []model.NodeDiff{
		changed("web-01", id("Package", "nginx"), id("File", "/etc/motd")),
		changed("web-02", id("Package", "nginx")),
	}

	q := &recordingQuerier{}
	estimates, diags := EstimateAll(context.Background(), q, targets, diffs)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if len(estimates) != 2 {
		t.Fatalf("got %d estimates, want 2 unique identities: %+v", len(estimates), estimates)
	}
	if estimates[0].Identity != id("File", "/etc/motd") || estimates[1].Identity != id("Package", "nginx") {
		t.Errorf("estimates not sorted by canonical identity: %+v", estimates)
	}
	if len(q.calls) != 2 {
		t.Errorf("Package[nginx] should be queried once, not once per target: %d calls", len(q.calls))
	}
}

// Edge-only differences never trigger an estimate.
func TestEstimateAll_SkipsEdgeOnlyDifferences(t *testing.T) {
	targets := []resolve.Target{impactTarget("web-01", true, time.Second, 5)}
	diffs := []model.NodeDiff{{
		Certname:      "web-01",
		EdgeChanges:   []model.EdgeChange{{Kind: model.ChangeEdgeAdded, Edge: model.Edge{Source: "A[a]", Target: "B[b]"}}},
		HasDifference: true,
	}}

	q := &recordingQuerier{}
	estimates, diags := EstimateAll(context.Background(), q, targets, diffs)
	if len(q.calls) != 0 {
		t.Errorf("edge-only differences triggered %d queries", len(q.calls))
	}
	if len(estimates) != 0 || len(diags) != 0 {
		t.Errorf("expected nothing: %+v %+v", estimates, diags)
	}
}

// A disabled estimate produces no request and no failure.
func TestEstimateAll_DisabledTargetProducesNoRequest(t *testing.T) {
	targets := []resolve.Target{impactTarget("web-01", false, time.Second, 5)}
	diffs := []model.NodeDiff{changed("web-01", id("Package", "nginx"))}

	q := &recordingQuerier{}
	estimates, diags := EstimateAll(context.Background(), q, targets, diffs)
	if len(q.calls) != 0 {
		t.Errorf("a disabled target issued %d queries", len(q.calls))
	}
	if len(estimates) != 0 || len(diags) != 0 {
		t.Errorf("expected no estimate and no failure: %+v %+v", estimates, diags)
	}
}

// The documented tie-break: limits come from the first target in
// target-file order that both enables estimation and exhibits the
// identity.
func TestEstimateAll_UsesFirstEnablingTargetInFileOrder(t *testing.T) {
	targets := []resolve.Target{
		impactTarget("web-01", false, 9*time.Second, 99),
		impactTarget("web-02", true, 2*time.Second, 20),
		impactTarget("web-03", true, 3*time.Second, 30),
	}
	diffs := []model.NodeDiff{
		changed("web-01", id("Package", "nginx")),
		changed("web-02", id("Package", "nginx")),
		changed("web-03", id("Package", "nginx")),
	}

	q := &recordingQuerier{}
	EstimateAll(context.Background(), q, targets, diffs)
	if len(q.calls) != 1 {
		t.Fatalf("got %d calls, want 1: %+v", len(q.calls), q.calls)
	}
	got := q.calls[0].limits
	if got.Timeout != 2*time.Second || got.ResultLimit != 20 {
		t.Errorf("limits = %+v, want web-02's (first enabling target in file order)", got)
	}
}

// The tie-break is on target-file order, not on the order node diffs
// happen to arrive in.
func TestEstimateAll_TieBreakIgnoresNodeDiffOrder(t *testing.T) {
	targets := []resolve.Target{
		impactTarget("web-01", true, 1*time.Second, 10),
		impactTarget("web-02", true, 2*time.Second, 20),
	}
	diffs := []model.NodeDiff{
		changed("web-02", id("Package", "nginx")),
		changed("web-01", id("Package", "nginx")),
	}

	q := &recordingQuerier{}
	EstimateAll(context.Background(), q, targets, diffs)
	if len(q.calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(q.calls))
	}
	if q.calls[0].limits.ResultLimit != 10 {
		t.Errorf("limits = %+v, want web-01's regardless of diff order", q.calls[0].limits)
	}
}

// An identity exhibited only by disabled targets is skipped even when
// another target enables estimation for its own identities.
func TestEstimateAll_SkipsIdentityNoEnablingTargetExhibits(t *testing.T) {
	targets := []resolve.Target{
		impactTarget("web-01", false, time.Second, 5),
		impactTarget("web-02", true, time.Second, 5),
	}
	diffs := []model.NodeDiff{
		changed("web-01", id("File", "/etc/only-on-disabled")),
		changed("web-02", id("Package", "nginx")),
	}

	q := &recordingQuerier{}
	estimates, _ := EstimateAll(context.Background(), q, targets, diffs)
	if len(estimates) != 1 {
		t.Fatalf("got %d estimates, want 1: %+v", len(estimates), estimates)
	}
	if estimates[0].Identity != id("Package", "nginx") {
		t.Errorf("estimated %v, want only the identity an enabling target exhibits", estimates[0].Identity)
	}
}

// A failed estimate is reported both as an estimate and as a diagnostic
// that reduces to an operational outcome.
func TestEstimateAll_FailedEstimateIsReportedTwice(t *testing.T) {
	targets := []resolve.Target{impactTarget("web-01", true, time.Second, 5)}
	diffs := []model.NodeDiff{changed("web-01", id("Package", "nginx"), id("File", "/etc/motd"))}

	q := &recordingQuerier{fail: map[model.ResourceIdentity]bool{id("Package", "nginx"): true}}
	estimates, diags := EstimateAll(context.Background(), q, targets, diffs)

	if len(estimates) != 2 {
		t.Fatalf("a failed estimate must still be reported: %+v", estimates)
	}
	if estimates[1].Status != model.ImpactStatusFailed {
		t.Errorf("status = %s, want failed", estimates[1].Status)
	}
	if estimates[0].Status != model.ImpactStatusCompleted {
		t.Errorf("one failure must not suppress the other estimates: %+v", estimates[0])
	}
	if len(diags) != 1 || diags[0].Operation != model.OperationEstimateImpact {
		t.Fatalf("diagnostics = %+v", diags)
	}
	if diags[0].Source != "Package[nginx]" {
		t.Errorf("diagnostic source = %q", diags[0].Source)
	}
}

// A node diff with no matching target is skipped rather than attributed
// to arbitrary configuration.
func TestEstimateAll_UnknownCertnameIsSkipped(t *testing.T) {
	targets := []resolve.Target{impactTarget("web-01", true, time.Second, 5)}
	diffs := []model.NodeDiff{changed("stranger", id("Package", "nginx"))}

	q := &recordingQuerier{}
	estimates, diags := EstimateAll(context.Background(), q, targets, diffs)
	if len(q.calls) != 0 || len(estimates) != 0 || len(diags) != 0 {
		t.Errorf("expected nothing for an unmatched certname: %+v %+v", estimates, diags)
	}
}

func TestEstimateAll_IsDeterministicAcrossRuns(t *testing.T) {
	targets := []resolve.Target{
		impactTarget("web-01", true, time.Second, 5),
		impactTarget("web-02", true, 2*time.Second, 20),
	}
	identities := []model.ResourceIdentity{
		id("Package", "nginx"), id("File", "/etc/z"), id("File", "/etc/a"),
		id("Service", "nginx"), id("Exec", "reload"),
	}
	diffs := []model.NodeDiff{changed("web-01", identities...), changed("web-02", identities...)}

	var first []model.ResourceIdentity
	for i := 0; i < 30; i++ {
		q := &recordingQuerier{}
		EstimateAll(context.Background(), q, targets, diffs)
		got := make([]model.ResourceIdentity, 0, len(q.calls))
		for _, c := range q.calls {
			got = append(got, c.identity)
		}
		if i == 0 {
			first = got
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("run %d: %d calls, want %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d: call order differs at %d: %v vs %v", i, j, got[j], first[j])
			}
		}
	}
}

func TestLimitsFrom_ProjectsResolvedPolicy(t *testing.T) {
	got := LimitsFrom(resolve.ImpactEstimate{Enabled: true, Timeout: 7 * time.Second, ResultLimit: 42})
	if got.Timeout != 7*time.Second || got.ResultLimit != 42 {
		t.Errorf("limits = %+v", got)
	}
}
