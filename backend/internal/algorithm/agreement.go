package algorithm

import (
	"errors"
	"math"
	"sort"

	"corpus-annotation-agreement-control/backend/internal/dto"
)

type RatingSet struct {
	Coder   string
	Ratings map[string]string
}

func ChooseMetric(sets []RatingSet) string {
	if len(sets) == 2 && !hasMissingRatings(sets) {
		return "cohen_kappa"
	}
	return "krippendorff_alpha"
}

func CohenKappa(left, right RatingSet) (dto.AgreementDetails, error) {
	keys := unionUnitKeys([]RatingSet{left, right})
	if len(keys) == 0 {
		return dto.AgreementDetails{}, errors.New("Cohen's Kappa requires at least one rated unit")
	}
	leftCounts := map[string]int{}
	rightCounts := map[string]int{}
	matches := 0
	sampleSize := 0
	missing := 0
	for _, key := range keys {
		leftValue, leftOK := left.Ratings[key]
		rightValue, rightOK := right.Ratings[key]
		if !leftOK || !rightOK || leftValue == "" || rightValue == "" {
			missing++
			continue
		}
		sampleSize++
		leftCounts[leftValue]++
		rightCounts[rightValue]++
		if leftValue == rightValue {
			matches++
		}
	}
	if sampleSize == 0 {
		return dto.AgreementDetails{}, errors.New("Cohen's Kappa has no units rated by both annotators")
	}
	observed := float64(matches) / float64(sampleSize)
	chance := 0.0
	labels := map[string]bool{}
	for label := range leftCounts {
		labels[label] = true
	}
	for label := range rightCounts {
		labels[label] = true
	}
	for label := range labels {
		chance += (float64(leftCounts[label]) / float64(sampleSize)) *
			(float64(leftCounts[label]) / float64(sampleSize))
	}
	// Cohen's Kappa: κ = (Po − Pe) / (1 − Pe), the agreement beyond chance
	// normalized by the headroom chance leaves above perfect agreement.
	score := normalizedAgreement(observed, chance)
	return dto.AgreementDetails{
		Metric: "cohen_kappa", Score: round6(score), ObservedAgreement: round6(observed),
		ChanceAgreement: round6(chance), SampleSize: sampleSize, CoderCount: 2,
		MissingValueCount: missing,
		Applicability:     "Cohen's Kappa compares exactly two annotators on units both rated; missing units are reported and excluded.",
	}, nil
}

func KrippendorffAlpha(sets []RatingSet) (dto.AgreementDetails, error) {
	if len(sets) < 2 {
		return dto.AgreementDetails{}, errors.New("Krippendorff's Alpha requires at least two annotators")
	}
	keys := unionUnitKeys(sets)
	if len(keys) == 0 {
		return dto.AgreementDetails{}, errors.New("Krippendorff's Alpha requires rated units")
	}
	categoryCounts := map[string]int{}
	totalRatings := 0
	disagreementPairs := 0
	totalPairs := 0
	unitsCompared := 0
	missing := 0
	for _, key := range keys {
		values := make([]string, 0, len(sets))
		for _, set := range sets {
			value, ok := set.Ratings[key]
			if !ok || value == "" {
				missing++
				continue
			}
			values = append(values, value)
			categoryCounts[value]++
			totalRatings++
		}
		if len(values) < 2 {
			continue
		}
		unitsCompared++
		for left := 0; left < len(values); left++ {
			for right := left + 1; right < len(values); right++ {
				totalPairs++
				if values[left] != values[right] {
					disagreementPairs++
				}
			}
		}
	}
	if totalPairs == 0 || totalRatings < 2 {
		return dto.AgreementDetails{}, errors.New("Krippendorff's Alpha requires two ratings on at least one unit")
	}
	observedDisagreement := float64(disagreementPairs) / float64(totalPairs)
	// Expected disagreement uses the marginal category distribution over all
	// ratings: De = 1 − Σc n_c(n_c−1) / (N(N−1)), where N is the total rating
	// count. The pair count is the wrong denominator here — it scales with the
	// number of raters per unit, not the overall sample, and can exceed 1.
	expectedAgreement := 0.0
	ratingCount := float64(totalRatings)
	if ratingCount > 1 {
		normalizer := ratingCount * (ratingCount - 1)
		for _, count := range categoryCounts {
			expectedAgreement += float64(count*(count-1)) / normalizer
		}
	}
	expectedDisagreement := 1 - expectedAgreement
	score := 1.0
	if expectedDisagreement > 1e-12 {
		score = 1 - observedDisagreement/expectedDisagreement
	} else if observedDisagreement > 1e-12 {
		score = 0
	}
	return dto.AgreementDetails{
		Metric: "krippendorff_alpha", Score: round6(clamp(score)), ObservedAgreement: round6(1 - observedDisagreement),
		ChanceAgreement: round6(expectedAgreement), SampleSize: unitsCompared, CoderCount: len(sets),
		MissingValueCount: missing,
		Applicability:     "Nominal Krippendorff's Alpha supports multiple annotators and missing ratings; units with fewer than two ratings do not contribute to observed disagreement.",
	}, nil
}

func Compute(metric string, sets []RatingSet) (dto.AgreementDetails, error) {
	selected := metric
	if selected == "" || selected == "auto" {
		selected = ChooseMetric(sets)
	}
	switch selected {
	case "cohen_kappa":
		if len(sets) != 2 {
			return dto.AgreementDetails{}, errors.New("Cohen's Kappa requires exactly two annotation sets")
		}
		return CohenKappa(sets[0], sets[1])
	case "krippendorff_alpha":
		return KrippendorffAlpha(sets)
	default:
		return dto.AgreementDetails{}, errors.New("unsupported agreement metric")
	}
}

func hasMissingRatings(sets []RatingSet) bool {
	keys := unionUnitKeys(sets)
	for _, key := range keys {
		for _, set := range sets {
			if set.Ratings[key] == "" {
				return true
			}
		}
	}
	return false
}

func unionUnitKeys(sets []RatingSet) []string {
	unique := map[string]bool{}
	for _, set := range sets {
		for key := range set.Ratings {
			unique[key] = true
		}
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// normalizedAgreement computes (observed − chance) / (1 − chance), the share of
// agreement beyond what chance would produce, scaled by the headroom left
// above chance. When chance is ~1 the denominator collapses; fall back to the
// observed agreement itself so perfect agreement stays 1 and any shortfall 0.
func normalizedAgreement(observed, chance float64) float64 {
	if math.Abs(1-chance) <= 1e-12 {
		if math.Abs(observed-1) <= 1e-12 {
			return 1
		}
		return 0
	}
	return clamp((observed - chance) / (1 - chance))
}

func clamp(value float64) float64 {
	return math.Max(-1, math.Min(1, value))
}

// round6 rounds to six decimal places for stable storage while preserving
// the precision the metrics actually carry.
func round6(value float64) float64 {
	return math.Round(value*1e6) / 1e6
}
