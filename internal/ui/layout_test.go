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
	// All remaining space goes to project (minus 3 column gaps)
	expectedProject := 140 - fixedStatusWidth - fixedContextWidth - fixedActivityWidth - 3
	if l.project != expectedProject {
		t.Errorf("expected project=%d, got %d", expectedProject, l.project)
	}
	if l.totalWidth != 140 {
		t.Errorf("expected totalWidth=140, got %d", l.totalWidth)
	}
}

func TestCalcSessionLayout_NarrowTerminal(t *testing.T) {
	l := calcSessionLayout(80)

	if l.status != 14 {
		t.Errorf("expected status=14, got %d", l.status)
	}
	if l.totalWidth != 80 {
		t.Errorf("expected totalWidth=80, got %d (status=%d project=%d context=%d activity=%d)",
			l.totalWidth, l.status, l.project, l.context, l.activity)
	}
}

func TestCalcSessionLayout_VeryNarrowTerminal(t *testing.T) {
	l := calcSessionLayout(55)

	if l.totalWidth != 55 {
		t.Errorf("expected totalWidth=55, got %d", l.totalWidth)
	}
}

func TestCalcSessionLayout_MinWidth(t *testing.T) {
	l := calcSessionLayout(40)

	// At tiny widths, project uses whatever space remains (minus 3 column gaps)
	expected := 40 - fixedStatusWidth - fixedContextWidth - fixedActivityWidth - 3
	if expected < 1 {
		expected = 1
	}
	if l.project != expected {
		t.Errorf("expected project=%d, got %d", expected, l.project)
	}
}

func TestCalcHistoryLayout_WideTerminal(t *testing.T) {
	l := calcHistoryLayout(120)

	// Project should be capped at preferred width
	if l.project != prefHistProjectWidth {
		t.Errorf("expected project=%d, got %d", prefHistProjectWidth, l.project)
	}
	// totalWidth = project + branch + startTime + duration + msgs + 4 gaps
	expected := l.project + l.branch + l.startTime + l.duration + l.msgs + 4
	if l.totalWidth != expected {
		t.Errorf("expected totalWidth=%d, got %d", expected, l.totalWidth)
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

	// At narrow widths, project gets whatever remains (may be clamped to min)
	if l.project < minHistProjectWidth {
		t.Errorf("expected project >= %d, got %d", minHistProjectWidth, l.project)
	}
	expected := l.project + l.branch + l.startTime + l.duration + l.msgs + 4
	if l.totalWidth != expected {
		t.Errorf("expected totalWidth=%d, got %d", expected, l.totalWidth)
	}
}
