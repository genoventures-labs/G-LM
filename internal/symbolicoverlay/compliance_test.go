package symbolicoverlay

import "testing"

func TestCheckComplianceDetectsViolations(t *testing.T) {
	artifact := OverlayArtifact{
		ConstraintSet: ConstraintSet{Items: []Constraint{
			{Kind: "required", Text: "must include rollback", Keywords: []string{"rollback"}},
			{Kind: "prohibited", Text: "must not disable security", Keywords: []string{"disable", "security"}},
		}},
		RiskLens: RiskLens{Signals: []RiskSignal{{Trigger: "security", Severity: "high"}}},
	}
	res := checkCompliance("we will disable security and proceed", artifact)
	if !res.Checked {
		t.Fatal("expected checked=true")
	}
	if res.ViolationCount == 0 {
		t.Fatal("expected violations")
	}
	if res.Score >= 1.0 {
		t.Fatalf("expected score lower than 1.0, got %f", res.Score)
	}
}

func TestCheckCompliancePassesWhenAligned(t *testing.T) {
	artifact := OverlayArtifact{
		ConstraintSet: ConstraintSet{Items: []Constraint{{Kind: "required", Text: "must include rollback", Keywords: []string{"rollback"}}}},
	}
	res := checkCompliance("we include rollback and monitoring", artifact)
	if res.ViolationCount != 0 {
		t.Fatalf("expected zero violations, got %d", res.ViolationCount)
	}
}
