package tui

import (
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type queueRow struct {
	job      QueueJob
	waiting  bool
	isHeader bool
	header   string
	idx      int
	pos      int
}

type queueResultMsg struct {
	err error
}

func queueCmd(f func() error) tea.Cmd {
	return func() tea.Msg {
		return queueResultMsg{err: f()}
	}
}

func buildQueueRows(snap QueueSnapshot) []queueRow {
	rows := make([]queueRow, 0, len(snap.Running)+len(snap.Waiting)+2)

	running := append([]QueueJob(nil), snap.Running...)
	sort.Slice(running, func(a, b int) bool {
		if !running[a].SubmittedAt.Equal(running[b].SubmittedAt) {
			return running[a].SubmittedAt.Before(running[b].SubmittedAt)
		}
		return running[a].ID < running[b].ID
	})

	idx := 0
	if len(running) > 0 {
		rows = append(rows, queueRow{isHeader: true, header: "RUNNING"})
	}
	for _, j := range running {
		rows = append(rows, queueRow{job: j, idx: idx})
		idx++
	}
	if len(snap.Waiting) > 0 {
		rows = append(rows, queueRow{isHeader: true, header: "QUEUE"})
	}
	for i, j := range snap.Waiting {
		rows = append(rows, queueRow{job: j, waiting: true, idx: idx, pos: i + 1})
		idx++
	}
	return rows
}

func (m DashboardModel) queueRows() []queueRow {
	if m.deps.QueueSnapshot == nil {
		return nil
	}
	return buildQueueRows(m.deps.QueueSnapshot())
}

func queueJobCountOf(rows []queueRow) int {
	n := 0
	for _, r := range rows {
		if !r.isHeader {
			n++
		}
	}
	return n
}

func (m DashboardModel) queueJobCount() int {
	return queueJobCountOf(m.queueRows())
}

func clampQueueSel(sel, total int) int {
	if sel > total-1 {
		sel = total - 1
	}
	if sel < 0 {
		sel = 0
	}
	return sel
}

func (m DashboardModel) selectedQueueJob() (job QueueJob, waiting, ok bool) {
	rows := m.queueRows()
	sel := clampQueueSel(m.queueSel, queueJobCountOf(rows))
	for _, r := range rows {
		if r.isHeader {
			continue
		}
		if r.idx == sel {
			return r.job, r.waiting, true
		}
	}
	return QueueJob{}, false, false
}

func queueUsedMB(snap QueueSnapshot) int {
	used := 0
	for _, j := range snap.Running {
		used += j.WeightMB
	}
	return used
}

func queueSummaryLine(snap QueueSnapshot, used int) string {
	pct := 0.0
	if snap.Budget > 0 {
		pct = float64(used) / float64(snap.Budget) * 100
	}
	line := fmt.Sprintf("%s  %d/%dMB  run %d  queue %d",
		renderBar(pct, barWidth), used, snap.Budget, len(snap.Running), len(snap.Waiting))
	if len(snap.Warnings) > 0 {
		line += fmt.Sprintf("  warn %d", len(snap.Warnings))
	}
	return line
}

func (m DashboardModel) queueChrome() int {
	n := 4
	if m.deps.ConnState != nil && m.deps.ConnState() != "" {
		n++
	}
	if m.queueFlash != "" {
		n++
	}
	return n
}

func formatQueueDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d / time.Second)
	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	mins := total / 60
	if mins < 60 {
		return fmt.Sprintf("%dm%ds", mins, total%60)
	}
	return fmt.Sprintf("%dh%dm", mins/60, mins%60)
}

func queueScrollOffset(rows []queueRow, sel, visible int) int {
	if visible <= 0 || len(rows) <= visible {
		return 0
	}
	selRow := len(rows) - 1
	for i, r := range rows {
		if !r.isHeader && r.idx == sel {
			selRow = i
			break
		}
	}
	offset := selRow - visible + 1
	if offset < 0 {
		return 0
	}
	if max := len(rows) - visible; offset > max {
		return max
	}
	return offset
}

func (m DashboardModel) renderQueueRow(r queueRow, selected bool) string {
	if r.isHeader {
		return subtitleStyle.Render(r.header)
	}
	cursor := " "
	if selected {
		cursor = selectedStyle.Render(">")
	}
	if r.waiting {
		return fmt.Sprintf("%s #%-3d %-16s %-5s %-20s %6dMB p %-2d waits %s",
			cursor, r.pos, r.job.ID, r.job.Kind, r.job.Client, r.job.WeightMB, r.job.Priority,
			formatQueueDur(time.Since(r.job.SubmittedAt)))
	}
	return fmt.Sprintf("%s %s   %-16s %-5s %-20s %6dMB p %-2d lease %s",
		cursor, runningStyle.Render("*"), r.job.ID, r.job.Kind, r.job.Client, r.job.WeightMB, r.job.Priority,
		formatQueueDur(time.Until(r.job.LeaseDeadline)))
}

func (m DashboardModel) viewQueue() string {
	w, h := m.width, m.height
	snap := m.deps.QueueSnapshot()

	title := " GPU Queue"
	if snap.Budget > 0 {
		title = " GPU Queue  " + queueSummaryLine(snap, queueUsedMB(snap))
	}

	var lines []string
	lines = append(lines, headerBg.Render(title))
	if m.deps.ConnState != nil {
		if banner := m.deps.ConnState(); banner != "" {
			lines = append(lines, progressYellow.Render(" "+banner+" "))
		}
	}
	lines = append(lines, sep(w))

	if snap.Budget <= 0 {
		lines = append(lines, helpStyle.Render("  GPU queue disabled"))
		lines = append(lines, sep(w))
		lines = append(lines, helpStyle.Render("[q] back"))
		return fillScreen(w, h, lines)
	}

	rows := buildQueueRows(snap)
	total := queueJobCountOf(rows)
	sel := clampQueueSel(m.queueSel, total)
	visible := h - m.queueChrome()
	if visible < 0 {
		visible = 0
	}
	offset := queueScrollOffset(rows, sel, visible)
	end := offset + visible
	if end > len(rows) {
		end = len(rows)
	}

	if len(rows) == 0 {
		lines = append(lines, helpStyle.Render("  no jobs"))
	} else {
		for i := offset; i < end; i++ {
			lines = append(lines, m.renderQueueRow(rows[i], !rows[i].isHeader && rows[i].idx == sel))
		}
	}

	padTo := h - 2
	if m.queueFlash != "" {
		padTo--
	}
	for len(lines) < padTo {
		lines = append(lines, "")
	}

	lines = append(lines, sep(w))
	if m.queueFlash != "" {
		lines = append(lines, progressRed.Render(m.queueFlash))
	}

	posInfo := "job 0/0"
	if total > 0 {
		posInfo = fmt.Sprintf("job %d/%d", sel+1, total)
	}
	footer := helpStyle.Render("[j/k] select  [u/d] priority  [x] cancel/release  [q] back")
	footer += "  " + labelStyle.Render(posInfo)
	lines = append(lines, footer)

	return fillScreen(w, h, lines)
}
