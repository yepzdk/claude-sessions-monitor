package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

// RenderFlags renders the flags feed: what the agents on this machine did that
// csm recognised, newest first, across both harnesses.
//
// When showFooter is true it uses \r\n for raw terminal mode.
// errMsg, when non-empty, explains why the list below may be incomplete -- as in
// RenderHistory, "nothing flagged" is a claim, and it must not be printed when
// the reason for the empty list is that the search failed.
func RenderFlags(events []session.FlaggedEvent, days int, showFooter bool, errMsg string) {
	nl := newlineFor(showFooter)

	if errMsg != "" {
		fmt.Printf("%sCannot read flags: %s%s%s", Red, sanitizeForTerminal(errMsg), Reset, nl)
		if len(events) == 0 {
			return
		}
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "%sFlags%s %s(past %d days)%s%s", Bold, Reset, Dim, days, Reset, nl)
	// The disclaimer is part of the view, not decoration. csm reads logs on a
	// timer: everything here already happened, and a command it does not
	// recognise leaves no trace at all.
	fmt.Fprintf(&buf, "%sAlready happened; csm reads logs after the fact. Absence is not safety.%s%s%s",
		Dim, Reset, nl, nl)

	if len(events) == 0 {
		fmt.Fprintf(&buf, "Nothing recognised in the past %d days.%s", days, nl)
		if showFooter {
			fmt.Fprintf(&buf, "%s%sl: live | h: history | u: usage | Ctrl+C: quit%s%s", nl, Dim, Reset, nl)
		}
		fmt.Print(buf.String())
		return
	}

	l := calcFlagLayout(getTerminalWidth())

	fmt.Fprintf(&buf, "%s%-*s %-*s %-*s %-*s %-*s%s%s",
		Bold,
		l.when, "WHEN",
		l.severity, "SEVERITY",
		l.project, "PROJECT",
		l.rule, "RULE",
		l.detail, "DETAIL",
		Reset, nl)
	fmt.Fprintf(&buf, "%s%s%s%s", Dim, strings.Repeat("─", l.totalWidth), Reset, nl)

	rows := events
	if showFooter {
		// Reserve: heading (1) + disclaimer (1) + blank (1) + column header (1)
		// + rule (1) + footer (2).
		if budget := getTerminalHeight() - 7; budget > 0 && len(rows) > budget {
			rows = rows[:budget]
		}
	}

	for _, e := range rows {
		colour := flagColor(e.Severity)
		fmt.Fprintf(&buf, "%-*s %s%-*s%s %-*s %s%-*s%s %s%s",
			l.when, e.At.Format("Jan 02 15:04"),
			colour, l.severity, string(e.Severity), Reset,
			l.project, truncate(sanitizeForTerminal(e.Project), l.project),
			Dim, l.rule, truncate(e.Rule, l.rule), Reset,
			truncate(sanitizeForTerminal(e.Detail), l.detail),
			nl)
	}

	if len(rows) < len(events) {
		fmt.Fprintf(&buf, "%s... and %d more%s%s", Dim, len(events)-len(rows), Reset, nl)
	}

	if showFooter {
		fmt.Fprintf(&buf, "%s%sl: live | h: history | u: usage | Ctrl+C: quit%s%s", nl, Dim, Reset, nl)
	}

	fmt.Print(buf.String())
}

// FlagFeedRefresh is how often the live loop may re-run the flags feed. It walks
// every log in the window, which is cheap on a cache hit and not free on a miss,
// and a feed of things that already happened does not need a two-second tick.
const FlagFeedRefresh = 30 * time.Second
