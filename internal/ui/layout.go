package ui

// Column width constraints for session table
const (
	fixedStatusWidth   = 14 // "● Needs Input" = 13 chars + 1 padding
	fixedContextWidth  = 16 // progress bar (10) + " 100%" (5) + 1 padding
	fixedActivityWidth = 15 // "LAST ACTIVITY" header + padding
	minProjectWidth    = 15
)

// sessionLayout holds the computed column widths for the session table.
// Last message is rendered on a separate line, so no message column is needed.
type sessionLayout struct {
	status     int
	project    int
	context    int
	activity   int
	totalWidth int
}

// calcSessionLayout computes column widths for the given terminal width.
// Fixed columns (status, context, activity) keep their size.
// All remaining space goes to the project column.
// Accounts for 3 separator spaces between the 4 columns.
func calcSessionLayout(width int) sessionLayout {
	l := sessionLayout{
		status:   fixedStatusWidth,
		context:  fixedContextWidth,
		activity: fixedActivityWidth,
	}

	const columnGaps = 3 // spaces between 4 columns
	fixed := l.status + l.context + l.activity + columnGaps
	remaining := width - fixed
	if remaining < 1 {
		remaining = 1
	}
	l.project = remaining

	l.totalWidth = l.status + l.project + l.context + l.activity + columnGaps

	return l
}

// Column width constraints for history table
const (
	minHistProjectWidth  = 15
	prefHistProjectWidth = 27
	fixedBranchWidth     = 12
	fixedDurationWidth   = 10
	fixedMsgsWidth       = 6
	minHistContextWidth  = 15
	prefHistContextWidth = 35
)

// historyLayout holds the computed column widths for the history table.
type historyLayout struct {
	project     int
	branch      int
	duration    int
	msgs        int
	context     int
	showContext bool
	totalWidth  int
}

// calcHistoryLayout computes column widths for the history table.
func calcHistoryLayout(width int) historyLayout {
	l := historyLayout{
		branch:   fixedBranchWidth,
		duration: fixedDurationWidth,
		msgs:     fixedMsgsWidth,
	}

	fixed := l.branch + l.duration + l.msgs
	remaining := width - fixed

	// Try to fit both project and context columns
	minBoth := minHistProjectWidth + minHistContextWidth
	if remaining >= minBoth {
		l.showContext = true

		l.project = prefHistProjectWidth
		if l.project > remaining-minHistContextWidth {
			l.project = remaining - minHistContextWidth
		}
		if l.project < minHistProjectWidth {
			l.project = minHistProjectWidth
		}
		l.context = remaining - l.project

		if l.context > prefHistContextWidth {
			extra := l.context - prefHistContextWidth
			l.context = prefHistContextWidth
			l.project += extra
		}
	} else {
		l.showContext = false
		l.context = 0
		l.project = remaining
		if l.project < 1 {
			l.project = 1
		}
	}

	l.totalWidth = l.project + l.branch + l.duration + l.msgs
	if l.showContext {
		l.totalWidth += l.context
	}

	return l
}
