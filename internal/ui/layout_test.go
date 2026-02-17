package ui

import "testing"

func TestCalcSessionLayout_WideTerminal(t *testing.T) {
	l := calcSessionLayout(140)

	if l.status != 14 {
		t.Errorf("expected status=14, got %d", l.status)
	}
	if l.context != 16 {
		t.Errorf("expected context=16, got %d", l.context)
	}
	if l.activity != 15 {
		t.Errorf("expected activity=15, got %d", l.activity)
	}
	// All remaining space goes to project
	expectedProject := 140 - fixedStatusWidth - fixedContextWidth - fixedActivityWidth
	if l.project != expectedProject {
		t.Errorf("expected project=%d, got %d", expectedProject, l.project)
	}
	total := l.status + l.project + l.context + l.activity
	if total != 140 {
		t.Errorf("expected columns to sum to 140, got %d", total)
	}
}

func TestCalcSessionLayout_NarrowTerminal(t *testing.T) {
	l := calcSessionLayout(80)

	if l.status != 14 {
		t.Errorf("expected status=14, got %d", l.status)
	}
	total := l.status + l.project + l.context + l.activity
	if total != 80 {
		t.Errorf("expected columns to sum to 80, got %d (status=%d project=%d context=%d activity=%d)",
			total, l.status, l.project, l.context, l.activity)
	}
}

func TestCalcSessionLayout_VeryNarrowTerminal(t *testing.T) {
	l := calcSessionLayout(55)

	total := l.status + l.project + l.context + l.activity
	if total != 55 {
		t.Errorf("expected columns to sum to 55, got %d", total)
	}
}

func TestCalcSessionLayout_MinWidth(t *testing.T) {
	l := calcSessionLayout(40)

	// At tiny widths, project uses whatever space remains
	expected := 40 - fixedStatusWidth - fixedContextWidth - fixedActivityWidth
	if expected < 1 {
		expected = 1
	}
	if l.project != expected {
		t.Errorf("expected project=%d, got %d", expected, l.project)
	}
}

func TestCalcHistoryLayout_WideTerminal(t *testing.T) {
	l := calcHistoryLayout(120)

	if !l.showContext {
		t.Error("expected context column visible at 120 cols")
	}
	total := l.project + l.branch + l.duration + l.msgs + l.context
	if total != 120 {
		t.Errorf("expected columns to sum to 120, got %d", total)
	}
}

func TestTruncate_NegativeMax(t *testing.T) {
	result := truncate("hello world", -5)
	if result != "" {
		t.Errorf("expected empty string for negative max, got %q", result)
	}
}

func TestTruncate_ZeroMax(t *testing.T) {
	result := truncate("hello world", 0)
	if result != "" {
		t.Errorf("expected empty string for zero max, got %q", result)
	}
}

func TestCalcHistoryLayout_NarrowTerminal(t *testing.T) {
	l := calcHistoryLayout(60)

	total := l.project + l.branch + l.duration + l.msgs
	if l.showContext {
		total += l.context
	}
	if total != 60 {
		t.Errorf("expected columns to sum to 60, got %d", total)
	}
}
