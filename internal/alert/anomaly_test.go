// sightpane — error tracking, product analytics and session replay you host yourself.
// Copyright (C) 2026 Can Us
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package alert

import (
	"testing"
)

func TestEvaluateAnomaly(t *testing.T) {
	// 1. Normal: current 10, baseline 10 -> mult 1.0, not anomaly
	res := EvaluateAnomaly(10.0, 10.0, 3.0, 5.0)
	if res.IsAnomaly {
		t.Fatalf("expected not anomaly, got true")
	}
	if res.Multiplier != 1.0 {
		t.Fatalf("expected mult 1.0, got %f", res.Multiplier)
	}

	// 2. Spike: current 40, baseline 10 -> mult 4.0 >= 3.0 -> is anomaly
	res = EvaluateAnomaly(40.0, 10.0, 3.0, 5.0)
	if !res.IsAnomaly {
		t.Fatalf("expected anomaly, got false")
	}
	if res.Multiplier != 4.0 {
		t.Fatalf("expected mult 4.0, got %f", res.Multiplier)
	}

	// 3. Low volume suppression: current 2, baseline 0.5 -> mult 4.0 >= 3.0, but current < minCount (5)
	res = EvaluateAnomaly(2.0, 0.5, 3.0, 5.0)
	if res.IsAnomaly {
		t.Fatalf("expected suppressed by minCount, got anomaly")
	}

	// 4. Zero baseline with high current: current 15, baseline 0 -> mult 10.0 >= 3.0 -> is anomaly
	res = EvaluateAnomaly(15.0, 0.0, 3.0, 5.0)
	if !res.IsAnomaly {
		t.Fatalf("expected anomaly for high current over zero baseline, got false")
	}
}
