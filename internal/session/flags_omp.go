package session

import (
	"encoding/json"
	"strings"
	"time"
)

// Flags for Oh My Pi sessions.
//
// What omp gives and Claude Code does not: `tool_execution_start` carries the
// bash command in `data.args.command`, in full, on its own line. The command
// classifier runs straight off it, so omp gets the whole of flags_command.go
// without decoding a single message body -- the assistant's `toolCall` block
// holds the same arguments, but reading it would mean widening ompContentText
// and paying for it on the parse path that runs for every session every tick.
//
// What omp does not give: any record of a permission or approval decision.
// There is no such member in its entry union and none appears in any log on
// disk; the approval gate blocks the UI and is never persisted. So every rule in
// flags.go that re-reports Claude Code's verdicts has no counterpart here, and
// an omp session's flags are csm's own reading of its commands, plus the one
// verdict omp does write down.
//
// mode_change was considered and rejected: it appears three times across 53 real
// sessions, and omp's plan mode carries no permission semantics, so leaving it
// is not an escalation. A rule that rare and that weak is not worth the reader's
// attention.

// RuleUserRuleViolation: a user-defined omp rule matched a tool call's
// arguments. omp writes the verdict into the tool result, including whether the
// call was interrupted or allowed to run.
const RuleUserRuleViolation = "user-rule-violation"

// ompFlagNeedles are the substrings an omp log line must contain to produce a
// flag. Same trick and same compact-JSON assumption as flagNeedles.
var ompFlagNeedles = [][]byte{
	[]byte(`"tool_execution_start"`),
	[]byte(`rule_violation`),
}

// ompFlagLine is the decode shape the omp rules need: the tool-start payload and
// a tool result's text. Deliberately not ompEntry, which drops `data.args` and
// flattens content at parse time.
type ompFlagLine struct {
	Type       string    `json:"type"`
	CustomType string    `json:"customType"`
	Timestamp  time.Time `json:"timestamp"`
	Data       *struct {
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		Args       *struct {
			Command string `json:"command"`
		} `json:"args"`
	} `json:"data"`
	Message *struct {
		Role       string          `json:"role"`
		ToolCallID string          `json:"toolCallId"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

// scanOMPFlags is scanClaudeFlags for an omp session log. It shares the cache:
// the two harnesses' logs never share a path, and the accumulate-from-an-offset
// policy is the same one.
func scanOMPFlags(logFile string, size int64) ([]Flag, error) {
	return scanFlagsWith(logFile, size, ompLineFlags)
}

// ompLineFlags applies every omp rule to one raw log line.
func ompLineFlags(line []byte, _ *flagScan) []Flag {
	if !hasNeedle(line, ompFlagNeedles) {
		return nil
	}
	var fl ompFlagLine
	if json.Unmarshal(line, &fl) != nil {
		return nil
	}

	if fl.Type == "custom" && fl.CustomType == "tool_execution_start" {
		if fl.Data == nil || fl.Data.ToolName != "bash" || fl.Data.Args == nil {
			return nil
		}
		return commandFlags(fl.Data.Args.Command, fl.Timestamp, fl.Data.ToolCallID)
	}
	if fl.Message != nil && fl.Message.Role == "toolResult" {
		return ruleViolationFlags(&fl)
	}
	return nil
}

// ruleViolationTag opens the reminder omp injects into a tool result when one of
// the user's own rules matched the call's arguments.
const ruleViolationTag = `<system-reminder reason="rule_violation"`

// ruleViolationFlags reports every user rule that matched this tool call.
//
// The verdict is prose inside the result text rather than a field, so this reads
// the tag's attributes. As with Claude Code's refusal messages, that makes the
// rule version-sensitive: a reworded reminder stops matching and the flag
// disappears rather than turning into a wrong one.
func ruleViolationFlags(fl *ompFlagLine) []Flag {
	text := blockText(fl.Message.Content)
	var flags []Flag
	for rest := text; ; {
		i := strings.Index(rest, ruleViolationTag)
		if i < 0 {
			return flags
		}
		rest = rest[i+len(ruleViolationTag):]
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			return flags
		}
		attrs, body := rest[:end], rest[end:]
		detail := tagAttr(attrs, "rule")
		if detail == "" {
			detail = "a user-defined rule"
		}
		// omp says in the body whether the rule stopped the call or merely
		// warned. "Ran anyway" is the case worth reading.
		if strings.Contains(body[:min(len(body), 200)], "tool ran") {
			detail += " matched; the tool ran anyway"
		} else {
			detail += " matched"
		}
		flags = append(flags, Flag{
			Rule:      RuleUserRuleViolation,
			Severity:  SeverityMedium,
			At:        fl.Timestamp,
			Detail:    excerpt(detail),
			ToolUseID: fl.Message.ToolCallID,
		})
		rest = body
	}
}

// tagAttr reads name="value" out of an XML-ish attribute run.
func tagAttr(attrs, name string) string {
	i := strings.Index(attrs, name+`="`)
	if i < 0 {
		return ""
	}
	rest := attrs[i+len(name)+2:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}
