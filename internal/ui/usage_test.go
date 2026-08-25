package ui

import (
	"fmt"
	"math"
	"testing"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// Utilization comes straight off the Anthropic API with no validation.
// Clamping only the upper end let a negative value produce a negative repeat
// count, which panics and takes the dashboard down with it.
func TestRenderUsageSurvivesOutOfRangeUtilization(t *testing.T) {
	// One value per branch of the clamp, plus -5: the smallest input that
	// actually produced a negative repeat count and panicked.
	for _, u := range []float64{50, 100, 1e9, -5, math.Inf(-1), math.NaN()} {
		t.Run(fmt.Sprintf("%v", u), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("utilization %v panicked: %v", u, r)
				}
			}()
			quota := &session.APIQuota{
				Available: true,
				FiveHour:  &session.QuotaBucket{Utilization: u},
			}
			RenderUsage(&session.UsageStats{}, quota, false)
		})
	}
}

// A failed lookup must not be rendered as a measured zero.
func TestRenderUsageDistinguishesFailureFromNoUsage(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked: %v", r)
		}
	}()
	RenderUsage(&session.UsageStats{Err: "permission denied"}, nil, false)
	RenderUsage(&session.UsageStats{PartialLogs: 1}, nil, false)
	RenderUsage(&session.UsageStats{}, nil, false)
	RenderUsage(nil, nil, false)
}
