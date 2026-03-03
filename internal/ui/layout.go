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
	prefHistProjectWidth = 30
	fixedBranchWidth     = 12
	fixedHistTimeWidth   = 7 // "HH:MM" + padding
	fixedDurationWidth   = 10
	fixedMsgsWidth       = 5
)

// historyLayout holds the computed column widths for the history table.
type historyLayout struct {
	project    int
	branch     int
	startTime  int
	duration   int
	msgs       int
	totalWidth int
}

// calcHistoryLayout computes column widths for the history table.
func calcHistoryLayout(width int) historyLayout {
	l := historyLayout{
		branch:    fixedBranchWidth,
		startTime: fixedHistTimeWidth,
		duration:  fixedDurationWidth,
		msgs:      fixedMsgsWidth,
	}

	// 4 gaps between 5 columns
	const columnGaps = 4
	fixed := l.branch + l.startTime + l.duration + l.msgs + columnGaps
	remaining := width - fixed
	if remaining < minHistProjectWidth {
		remaining = minHistProjectWidth
	}
	l.project = remaining
	if l.project > prefHistProjectWidth {
		l.project = prefHistProjectWidth
	}

	l.totalWidth = l.project + l.branch + l.startTime + l.duration + l.msgs + columnGaps

	return l
}
