package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// approvalView is the fully rendered permission prompt: every styled line plus the number of
// trailing lines (question + options + hint) that must never be truncated. The anchoring renderer
// keeps the head (title + content preview) from the top and the tail from the bottom, dropping the
// content middle when the block overflows — so the action options stay actionable on any diff size.
type approvalView struct {
	lines     []lineRuns
	tailCount int
}

// buildApprovalView renders the pending tool-permission request in the Claude Code style: a card
// titled by the operation ("Create file" / "Update file" / "Edit file"), the relative path, a
// preview (line-numbered content for new files, a colored diff otherwise), then the question and a
// vertical list of numbered Yes/No options. The leading top rule is painted by the layout, so the
// card body starts at the title.
func buildApprovalView(a *App, width int) approvalView {
	if a.asking == nil {
		return approvalView{}
	}
	ask := *a.asking
	info := parseToolInput(ask.toolName, ask.input)

	var head []lineRuns
	addLine := func(text string, st Style) {
		head = append(head, lineRuns{runs: []styledRun{{text: text, style: st}}})
	}
	addBlank := func() { head = append(head, lineRuns{}) }

	var questionRuns []styledRun
	sessionOpt := i18n.T("approval.opt.yes_session")

	switch ask.toolName {
	case "write", "edit":
		sessionOpt = i18n.T("approval.opt.yes_edits")
		base := filepath.Base(info.path)
		isCreate := ask.toolName == "write" && !fileExists(info.path)

		title := i18n.T("approval.title.edit")
		qFmt := i18n.T("approval.q.edit")
		if ask.toolName == "write" {
			if isCreate {
				title, qFmt = i18n.T("approval.title.create"), i18n.T("approval.q.create")
			} else {
				title, qFmt = i18n.T("approval.title.update"), i18n.T("approval.q.update")
			}
		}
		addLine(title, styleDiffFile())
		if rel := relDisplayPath(info.path); rel != "" {
			addLine(rel, styleToolDim())
		}
		addBlank()
		addLine(approvalSep(width), styleToolDim())
		if isCreate {
			head = append(head, numberedContentLines(info.newString, width)...)
		} else {
			for _, ln := range strings.Split(renderDiffHunked(fullFileDiff(info), width), "\n") {
				addLine(ln, diffLineStyle(ln))
			}
		}
		addLine(approvalSep(width), styleToolDim())
		questionRuns = approvalQuestionRuns(qFmt, base)

	case "bash":
		cmdW := maxi(40, width) - 2
		addLine("$ "+truncateStr(info.command, cmdW), styleDiffFile())
		questionRuns = approvalQuestionRuns(i18n.T("approval.q.bash"), "")

	case "task":
		// Surface what the sub-agent will actually do: its one-line label plus the full instructions
		// (wrapped). The anchored renderer drops the prompt middle when it overflows, keeping the
		// label, the question and the options visible regardless of prompt length.
		label := strings.TrimSpace(info.description)
		if label == "" {
			label = ask.toolName
		}
		addLine(i18n.T("approval.task.label")+": "+label, styleDiffFile())
		addBlank()
		addLine(approvalSep(width), styleToolDim())
		prompt := strings.TrimSpace(info.prompt)
		if prompt == "" {
			prompt = i18n.T("approval.task.no_prompt")
		}
		for _, ln := range wrapPlain(prompt, maxi(20, width)) {
			addLine(ln, styleText())
		}
		addLine(approvalSep(width), styleToolDim())
		questionRuns = approvalQuestionRuns(i18n.T("approval.q.task"), "")

	default:
		questionRuns = approvalQuestionRuns(i18n.T("approval.q.generic"), ask.toolName)
	}

	var tail []lineRuns
	tail = append(tail, lineRuns{})
	tail = append(tail, lineRuns{runs: questionRuns})
	tail = append(tail, renderApprovalOptions(a.approveIdx, sessionOpt)...)
	tail = append(tail, lineRuns{runs: []styledRun{{text: i18n.T("approval.hint"), style: styleToolDim()}}})

	return approvalView{lines: append(head, tail...), tailCount: len(tail)}
}

// renderApprovalOptions renders the three permission choices as a vertical numbered list with a "❯"
// pointer on the focused row, mirroring Claude Code's Select. The session option label is supplied
// by the caller so it can read "all edits" for file ops and "this tool" otherwise.
func renderApprovalOptions(sel int, sessionLabel string) []lineRuns {
	labels := []string{i18n.T("approval.opt.yes"), sessionLabel, i18n.T("approval.opt.no")}
	out := make([]lineRuns, 0, len(labels))
	for i, label := range labels {
		text := fmt.Sprintf("%d. %s", i+1, label)
		if i == sel {
			st := styleApprove()
			if i == len(labels)-1 {
				st = styleReject()
			}
			out = append(out, lineRuns{runs: []styledRun{{text: "❯ " + text, style: st}}})
		} else {
			out = append(out, lineRuns{runs: []styledRun{{text: "  " + text, style: styleToolDim()}}})
		}
	}
	return out
}

// numberedContentLines renders file content with a right-aligned line-number gutter, used for the
// "Create file" preview where there is no prior content to diff against. Tabs are expanded so the
// gutter stays aligned, and each row is truncated (not wrapped) to keep numbers column-stable.
func numberedContentLines(content string, width int) []lineRuns {
	lines := splitLines(content)
	if len(lines) == 0 {
		return []lineRuns{{runs: []styledRun{{text: i18n.T("approval.empty_file"), style: styleToolDim()}}}}
	}
	gutterW := len(fmt.Sprintf("%d", len(lines)))
	if gutterW < 2 {
		gutterW = 2
	}
	contentW := width - gutterW - 1
	if contentW < 1 {
		contentW = 1
	}
	out := make([]lineRuns, 0, len(lines))
	for i, ln := range lines {
		shown := truncateStrW(strings.ReplaceAll(ln, "\t", "    "), contentW)
		out = append(out, lineRuns{runs: []styledRun{
			{text: fmt.Sprintf("%*d ", gutterW, i+1), style: styleToolDim()},
			{text: shown, style: styleText()},
		}})
	}
	return out
}

// approvalQuestionRuns formats the confirmation question, emphasizing the file name (or tool name)
// substituted for the single %s verb in bold, matching Claude Code's bolded basename.
func approvalQuestionRuns(format, name string) []styledRun {
	if !strings.Contains(format, "%s") {
		return []styledRun{{text: format, style: styleText()}}
	}
	parts := strings.SplitN(format, "%s", 2)
	runs := make([]styledRun, 0, 3)
	if parts[0] != "" {
		runs = append(runs, styledRun{text: parts[0], style: styleText()})
	}
	runs = append(runs, styledRun{text: name, style: styleDiffFile()})
	if len(parts) > 1 && parts[1] != "" {
		runs = append(runs, styledRun{text: parts[1], style: styleText()})
	}
	return runs
}

// approvalSep is the dim horizontal divider between the card sections.
func approvalSep(width int) string {
	if width < 1 {
		width = 1
	}
	return strings.Repeat("─", width)
}

// relDisplayPath returns path relative to the current working directory for display, falling back
// to the original path when it lies outside the cwd or the cwd cannot be resolved.
func relDisplayPath(path string) string {
	if path == "" {
		return ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// fileExists reports whether path names an existing regular file (not a directory).
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
