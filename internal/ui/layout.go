package ui

// Column width constraints for session table
const (
	fixedStatusWidth   = 14 // "● Needs Input" = 13 chars + 1 padding
	fixedOriginWidth   = 10 // "Claude Desktop" truncated; most origins fit in 9
	fixedContextWidth  = 16 // progress bar (10) + " 100%" (5) + 1 padding
	fixedActivityWidth = 15 // "LAST ACTIVITY" header + padding
	minProjectWidth    = 15
	originColumnMinTTY = 90 // drop the origin column below this terminal width
)

// sessionLayout holds the computed column widths for the session table.
// Last message is rendered on a separate line, so no message column is needed.
type sessionLayout struct {
	status     int
	project    int
	origin     int
	context    int
	activity   int
	totalWidth int
}

// calcSessionLayout computes column widths for the given terminal width.
// Fixed columns (status, origin, context, activity) keep their size.
// All remaining space goes to the project column. The origin column is
// dropped on narrow terminals to keep the project column readable.
// Accounts for one separator space between each pair of adjacent columns.
func calcSessionLayout(width int) sessionLayout {
	l := sessionLayout{
		status:   fixedStatusWidth,
		context:  fixedContextWidth,
		activity: fixedActivityWidth,
	}
	if width >= originColumnMinTTY {
		l.origin = fixedOriginWidth
	}

	// One space between each pair of adjacent visible columns.
	gaps := 3 // status|project|context|activity
	if l.origin > 0 {
		gaps = 4 // status|project|origin|context|activity
	}
	fixed := l.status + l.origin + l.context + l.activity + gaps
	remaining := width - fixed
	if remaining < 1 {
		remaining = 1
	}
	l.project = remaining

	l.totalWidth = l.status + l.project + l.origin + l.context + l.activity + gaps

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

// Column width constraints for usage table
const (
	fixedUsageInputWidth  = 10
	fixedUsageOutputWidth = 10
	fixedUsageCacheWidth  = 10
	fixedUsageTotalWidth  = 10
	minUsageProjectWidth  = 15
)

// usageLayout holds the computed column widths for the usage per-session table.
type usageLayout struct {
	project    int
	input      int
	output     int
	cache      int
	total      int
	totalWidth int
}

// calcUsageLayout computes column widths for the usage table.
func calcUsageLayout(width int) usageLayout {
	l := usageLayout{
		input:  fixedUsageInputWidth,
		output: fixedUsageOutputWidth,
		cache:  fixedUsageCacheWidth,
		total:  fixedUsageTotalWidth,
	}

	// 4 gaps between 5 columns, plus 2-char indent
	const columnGaps = 4
	const indent = 2
	fixed := l.input + l.output + l.cache + l.total + columnGaps + indent
	remaining := width - fixed
	if remaining < minUsageProjectWidth {
		remaining = minUsageProjectWidth
	}
	l.project = remaining

	l.totalWidth = l.project + l.input + l.output + l.cache + l.total + columnGaps

	return l
}
