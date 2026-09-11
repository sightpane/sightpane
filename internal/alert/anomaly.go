// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package alert

import (
	"fmt"
	"math"
)

// AnomalyEvaluation holds the results of evaluating an anomaly spike rule.
type AnomalyEvaluation struct {
	CurrentValue float64
	BaselineValue float64
	Multiplier   float64
	IsAnomaly    bool
	Reason       string
}

// EvaluateAnomaly evaluates whether currentValue represents an anomalous spike relative to baselineValue.
// spikeThreshold is the multiplier k (e.g. 3.0 means 3x higher than baseline).
// minCount is the minimum required value (e.g. 5) to prevent false alerts on low volume (e.g. 0 -> 1).
func EvaluateAnomaly(currentValue, baselineValue, spikeThreshold float64, minCount float64) AnomalyEvaluation {
	if spikeThreshold <= 1.0 {
		spikeThreshold = 2.0 // default at least 2x
	}
	if minCount <= 0 {
		minCount = 5.0 // default minimum 5 occurrences
	}

	var mult float64
	if baselineValue > 0 {
		mult = currentValue / baselineValue
	} else if currentValue >= minCount {
		// If baseline is 0 and current is at or above minCount, consider it a 10x spike
		mult = 10.0
	} else {
		mult = 0.0
	}

	isAnomaly := currentValue >= minCount && mult >= spikeThreshold
	var reason string
	if isAnomaly {
		reason = fmt.Sprintf("Current metric (%.2f) is %.1fx higher than historical baseline (%.2f, threshold: %.1fx)",
			currentValue, mult, math.Max(baselineValue, 0.1), spikeThreshold)
	} else {
		reason = fmt.Sprintf("Current metric (%.2f) within baseline limits (baseline: %.2f, multiplier: %.1fx)",
			currentValue, baselineValue, mult)
	}

	return AnomalyEvaluation{
		CurrentValue:  currentValue,
		BaselineValue: baselineValue,
		Multiplier:    mult,
		IsAnomaly:     isAnomaly,
		Reason:        reason,
	}
}
