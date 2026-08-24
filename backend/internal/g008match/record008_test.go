package g008match

import (
	"math"
	"testing"

	"corpus-annotation-agreement-control/backend/internal/algorithm"
	"corpus-annotation-agreement-control/backend/internal/constants"
	"corpus-annotation-agreement-control/backend/internal/dto"
	"corpus-annotation-agreement-control/backend/internal/matching"
)

func closeEnough(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

func TestG008KappaScoreCorrect(t *testing.T) {
	left := algorithm.RatingSet{Coder: "left", Ratings: map[string]string{
		"u1": "A", "u2": "A", "u3": "B", "u4": "B", "u5": "C",
	}}
	right := algorithm.RatingSet{Coder: "right", Ratings: map[string]string{
		"u1": "A", "u2": "B", "u3": "B", "u4": "B", "u5": "C",
	}}
	result, err := algorithm.CohenKappa(left, right)
	if err != nil {
		t.Fatalf("cohen kappa failed: %v", err)
	}
	if !closeEnough(result.Score, 0.6875) {
		t.Fatalf("expected cohen kappa score 0.6875, got %v", result.Score)
	}
	if !closeEnough(result.ChanceAgreement, 0.36) {
		t.Fatalf("expected chance agreement 0.36, got %v", result.ChanceAgreement)
	}
}

func TestG008AlphaScoreCorrect(t *testing.T) {
	sets := []algorithm.RatingSet{
		{Coder: "a", Ratings: map[string]string{"u1": "X", "u2": "Y", "u3": "X", "u4": "Y"}},
		{Coder: "b", Ratings: map[string]string{"u1": "X", "u2": "X", "u3": "X", "u4": "X"}},
	}
	result, err := algorithm.KrippendorffAlpha(sets)
	if err != nil {
		t.Fatalf("krippendorff failed: %v", err)
	}
	if !closeEnough(result.Score, -0.166667) {
		t.Fatalf("expected krippendorff alpha score -0.166667 for this fixture, got %v", result.Score)
	}
}

func TestG008BoundaryDisagreementDetected(t *testing.T) {
	left := []dto.AnnotationLabel{
		{UnitKey: "s", Label: "PARTY", Start: 0, End: 8},
	}
	right := []dto.AnnotationLabel{
		{UnitKey: "s", Label: "PARTY", Start: 0, End: 9},
	}
	evidence, _, dtype, _ := matching.CompareAnnotations(left, right)
	if len(evidence) != 1 || evidence[0].Type != constants.DisagreementBoundary {
		t.Fatalf("expected one boundary disagreement, got %+v", evidence)
	}
	if dtype != constants.DisagreementBoundary {
		t.Fatalf("expected dominant disagreement boundary, got %q", dtype)
	}
}

func TestG008ClusterKeyLowercase(t *testing.T) {
	key := matching.ClusterKey("LEGAL-ENTITY", "label", "RISK~CLEAR")
	if key != "legal-entity:label:risk~clear" {
		t.Fatalf("expected lowercased cluster key, got %q", key)
	}
}
