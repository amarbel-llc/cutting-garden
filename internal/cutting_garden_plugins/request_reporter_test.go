package cutting_garden_plugins

import "testing"

// Each request struct must carry an opt-in Reporter the orchestrator can
// populate. This test fixes the field name/type across all four and proves
// ReporterOrNop works off the field.
func TestRequests_CarryReporterField(t *testing.T) {
	rec := &recordingReporter{}

	cr := CaptureRootRequest{Reporter: rec}
	pr := ProtocolCaptureRequest{Reporter: rec}
	rr := RestoreRequest{Reporter: rec}
	dr := DiffScanRequest{Reporter: rec}

	for _, r := range []Reporter{cr.Reporter, pr.Reporter, rr.Reporter, dr.Reporter} {
		ReporterOrNop(r).Plan(ReportPlan{})
	}
	if rec.plans != 4 {
		t.Errorf("expected 4 Plan calls through the request fields, got %d", rec.plans)
	}

	// A zero-value request has a nil Reporter that ReporterOrNop tolerates.
	ReporterOrNop(CaptureRootRequest{}.Reporter).Log("no panic")
}
