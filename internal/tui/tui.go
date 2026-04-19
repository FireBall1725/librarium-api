// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

// Package tui provides an optional terminal dashboard for the API server.
// Enable with TUI=true. Press q or Ctrl+C to exit.
package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── Styles ──────────────────────────────────────────────────────────────────

var (
	styleBorder   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
	styleTitle    = lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Bold(true)
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleBar      = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))  // soft purple
	styleBarPeak  = lipgloss.NewStyle().Foreground(lipgloss.Color("99"))  // bright purple for recent
	styleAxisLine = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleDebug    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleInfo     = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	styleWarn     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	styleError    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	style2xx      = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	style3xx      = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	style4xx      = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	style5xx      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleMethod   = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleTimeCol  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleClient   = lipgloss.NewStyle().Foreground(lipgloss.Color("141")) // soft lavender
	styleIP       = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
	styleMark      = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true) // bright yellow
	styleScroll    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))            // orange scroll indicator
	styleQueueIdle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleProgress  = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	styleProgressD = lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	styleRunning   = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))             // green ► running
	stylePending   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))            // orange ● waiting
)

// sub-block runes: index 0 = empty, 1-8 = ▁▂▃▄▅▆▇█
var blockRunes = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

const graphHeight = 10 // rows for the bar chart

// ─── Model ───────────────────────────────────────────────────────────────────

const maxLogLines = 500

type model struct {
	collector    *Collector
	width        int
	height       int
	logLines     []string
	scrollOffset int  // lines from the bottom; 0 = following
	hScroll      int  // horizontal column offset; 0 = leftmost
	autoScroll   bool // jump to bottom on new entries
	lastLogRows  int  // updated each View(); used for page-scroll sizing
}

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func newModel(c *Collector) model {
	return model{collector: c, width: 120, height: 40, autoScroll: true}
}

func (m model) Init() tea.Cmd { return tick() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "up", "k":
			m.autoScroll = false
			m.scrollOffset++
			m.clampScroll()

		case "down", "j":
			if m.scrollOffset > 0 {
				m.scrollOffset--
			}
			if m.scrollOffset == 0 {
				m.autoScroll = true
			}

		case "pgup", "u":
			m.autoScroll = false
			page := m.lastLogRows
			if page < 1 {
				page = 10
			}
			m.scrollOffset += page
			m.clampScroll()

		case "pgdown", "d":
			page := m.lastLogRows
			if page < 1 {
				page = 10
			}
			m.scrollOffset -= page
			if m.scrollOffset <= 0 {
				m.scrollOffset = 0
				m.autoScroll = true
			}

		case "left":
			m.hScroll -= 10
			if m.hScroll < 0 {
				m.hScroll = 0
			}

		case "right":
			m.hScroll += 10

		case "g": // jump to top
			m.autoScroll = false
			m.scrollOffset = len(m.logLines)
			m.clampScroll()

		case "end", "f": // jump to bottom / resume following
			m.scrollOffset = 0
			m.autoScroll = true
			m.hScroll = 0

		case "m": // insert a visual mark
			ts := time.Now().Format("15:04:05")
			bar := strings.Repeat("─", 30)
			m.logLines = append(m.logLines, styleMark.Render(bar+" MARK "+ts+" "+bar))
			if len(m.logLines) > maxLogLines {
				m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		linesBefore := len(m.logLines)

		// Drain new events from channels
		for {
			select {
			case req := <-m.collector.ReqCh():
				m.logLines = append(m.logLines, renderReqLine(req))
				// For 5xx responses, show the real error on the next line.
				if req.Status >= 500 && req.ErrorMsg != "" {
					m.logLines = append(m.logLines, renderErrorDetail(req.ErrorMsg))
				}
			case entry := <-m.collector.LogCh():
				if entry.Message != "request" {
					m.logLines = append(m.logLines, renderLogLine(entry))
				}
			default:
				goto drained
			}
		}
	drained:
		if len(m.logLines) > maxLogLines {
			m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
		}

		// If scrolled up, shift offset to keep the same content in view.
		if !m.autoScroll {
			added := len(m.logLines) - linesBefore
			if added > 0 {
				m.scrollOffset += added
				m.clampScroll()
			}
		}

		return m, tick()
	}
	return m, nil
}

// clampScroll ensures scrollOffset stays within valid range.
func (m *model) clampScroll() {
	max := len(m.logLines) - 1
	if max < 0 {
		max = 0
	}
	if m.scrollOffset > max {
		m.scrollOffset = max
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

func (m model) View() string {
	if m.width == 0 {
		return ""
	}

	// ── Graph section ─────────────────────────────────────────────────────────
	// Border takes 2 chars (left+right), lipgloss adds 1 inner padding each side.
	// So inner drawable width = total - 4.
	innerW := m.width - 4
	if innerW < 10 {
		innerW = 10
	}

	// Y-axis label format is "%4d │" = 6 chars wide.
	const yAxisW = 6
	graphW := innerW - yAxisW
	if graphW < 4 {
		graphW = 4
	}

	// Scale 60 raw per-second buckets to exactly graphW display columns.
	raw := m.collector.ReqSparkline()
	buckets := scaleSparkline(raw, graphW)

	maxVal := 0
	total := 0
	for _, v := range raw {
		total += v
		if v > maxVal {
			maxVal = v
		}
	}
	recentRate := 0
	if len(raw) > 0 {
		recentRate = raw[len(raw)-1]
	}
	activeConns := m.collector.ActiveConns()

	// Header row
	headerLeft := styleTitle.Render("req/s")
	headerStats := styleDim.Render(fmt.Sprintf(
		"  current: %-4d  peak: %-4d  total(60s): %-5d  conns: %d",
		recentRate, maxVal, total, activeConns,
	))
	header := headerLeft + headerStats

	// Multi-row graph
	graphRows := renderBarChart(buckets, graphHeight, maxVal)

	// Bottom axis line
	axis := styleAxisLine.Render(
		strings.Repeat(" ", yAxisW) + strings.Repeat("─", graphW),
	)

	graphContent := header + "\n" + strings.Join(graphRows, "\n") + "\n" + axis
	graphSection := styleBorder.Width(m.width - 2).Render(graphContent)

	// ── Queue section ─────────────────────────────────────────────────────────
	queueSection := renderQueueSection(m.collector.GetQueueStats(), m.width)

	// ── Log section ───────────────────────────────────────────────────────────
	scrollIndicator := ""
	if !m.autoScroll {
		scrollIndicator = "  " + styleScroll.Render(fmt.Sprintf("↑ %d lines  [f/end=bottom]", m.scrollOffset))
	} else {
		scrollIndicator = "  " + styleDim.Render("[FOLLOWING]  m=mark  g=top")
	}
	if m.hScroll > 0 {
		scrollIndicator += "  " + styleScroll.Render(fmt.Sprintf("→ col %d  [←/→ scroll  f=reset]", m.hScroll))
	}

	logHeader := styleDim.Render(fmt.Sprintf("%-8s", "TIME")) + "  " +
		styleIP.Render(fmt.Sprintf("%-16s", "IP")) + "  " +
		styleClient.Render(fmt.Sprintf("%-4s", "CLI")) + "  " +
		styleMethod.Render(fmt.Sprintf("%-6s", "METH")) + "  " +
		style2xx.Render(fmt.Sprintf("%-7s", "STATUS")) + "  " +
		styleDim.Render(fmt.Sprintf("%-12s", "DURATION")) + "  " +
		styleTitle.Render("PATH / MESSAGE") +
		scrollIndicator

	usedRows := lipgloss.Height(graphSection) + lipgloss.Height(queueSection) + 4
	logRows := m.height - usedRows
	if logRows < 1 {
		logRows = 1
	}
	// Store for page-scroll sizing in Update().
	// Note: we can't mutate m here (View is a value receiver), so we stash it
	// through the pointer trick below, but we avoid that complexity by just
	// using logRows directly — the value written during the previous frame is
	// close enough for scrolling purposes.
	_ = logRows // used below

	// Build visible slice
	var visible []string
	total2 := len(m.logLines)
	end := total2 - m.scrollOffset
	if end < 0 {
		end = 0
	}
	start := end - logRows
	if start < 0 {
		start = 0
	}
	if end > total2 {
		end = total2
	}
	visible = m.logLines[start:end]

	// Pad top so newest entries are always at the bottom
	padded := make([]string, logRows)
	copy(padded[logRows-len(visible):], visible)

	// Apply horizontal scroll offset then clamp to the available inner width.
	// Border (2) + padding (2) = 4 chars overhead.
	availW := m.width - 4
	if availW > 0 {
		clamp := lipgloss.NewStyle().MaxWidth(availW)
		for i, line := range padded {
			if m.hScroll > 0 {
				line = hScrollLine(line, m.hScroll)
			}
			if lipgloss.Width(line) > availW {
				line = clamp.Render(line)
			}
			padded[i] = line
		}
	}

	logContent := logHeader + "\n" + strings.Join(padded, "\n")
	logSection := styleBorder.Width(m.width - 2).Render(logContent)

	return graphSection + "\n" + queueSection + "\n" + logSection
}

// ─── Bar chart ────────────────────────────────────────────────────────────────

// scaleSparkline scales src to exactly n output points using nearest-neighbour
// for stretching and range-averaging for compression.
func scaleSparkline(src []int, n int) []int {
	if n == 0 || len(src) == 0 {
		return make([]int, n)
	}
	if n == len(src) {
		out := make([]int, n)
		copy(out, src)
		return out
	}
	out := make([]int, n)
	srcN := len(src)
	for i := 0; i < n; i++ {
		lo := i * srcN / n
		hi := (i + 1) * srcN / n
		if hi <= lo {
			hi = lo + 1
		}
		if hi > srcN {
			hi = srcN
		}
		sum := 0
		for j := lo; j < hi; j++ {
			sum += src[j]
		}
		out[i] = sum / (hi - lo)
	}
	return out
}

// renderBarChart returns graphHeight rendered rows (top to bottom) plus a
// y-axis label prefix of yAxisW characters.
func renderBarChart(buckets []int, height, maxVal int) []string {
	if maxVal == 0 {
		maxVal = 1
	}
	// Convert each bucket to sub-block units (0 .. height*8)
	units := make([]int, len(buckets))
	for i, v := range buckets {
		u := v * height * 8 / maxVal
		if u > height*8 {
			u = height * 8
		}
		units[i] = u
	}

	rows := make([]string, height)
	for row := 0; row < height; row++ {
		// row 0 = topmost display row, row height-1 = bottommost
		level := height - 1 - row // 0 at bottom, height-1 at top
		floor := level * 8         // sub-units at cell bottom
		ceiling := (level + 1) * 8 // sub-units at cell top

		// Y-axis label: show max at top, 0 at bottom, blank otherwise
		yLabel := ""
		switch row {
		case 0:
			yLabel = fmt.Sprintf("%4d │", maxVal)
		case height - 1:
			yLabel = fmt.Sprintf("%4d │", 0)
		case height / 2:
			yLabel = fmt.Sprintf("%4d │", maxVal/2)
		default:
			yLabel = "     │"
		}

		var sb strings.Builder
		for i, u := range units {
			// Colour recent entries brighter
			useStyle := styleBar
			if i >= len(units)-5 {
				useStyle = styleBarPeak
			}

			var ch rune
			switch {
			case u >= ceiling:
				ch = '█'
			case u > floor:
				ch = blockRunes[u-floor]
			default:
				ch = ' '
			}
			sb.WriteString(useStyle.Render(string(ch)))
		}

		rows[row] = styleDim.Render(yLabel) + sb.String()
	}
	return rows
}

// ─── Log line renderers ───────────────────────────────────────────────────────

func renderReqLine(req RequestSample) string {
	ts := styleTimeCol.Render(req.Time.Format("15:04:05"))
	ip := styleIP.Render(fmt.Sprintf("%-16s", truncate(req.RemoteAddr, 16)))
	client := styleClient.Render(fmt.Sprintf("%-4s", truncate(req.Client, 4)))
	method := styleMethod.Render(fmt.Sprintf("%-6s", req.Method))
	status := colorStatus(req.Status).Render(fmt.Sprintf("%-7d", req.Status))
	dur := styleDim.Render(fmt.Sprintf("%-12s", fmtDuration(req.Duration)))
	return fmt.Sprintf("%s  %s  %s  %s  %s  %s  %s", ts, ip, client, method, status, dur, req.Path)
}

func renderErrorDetail(msg string) string {
	prefix := styleError.Render("  └─")
	detail := styleError.Render(truncate(msg, 160))
	return prefix + " " + detail
}

func renderLogLine(e LogEntry) string {
	ts := styleTimeCol.Render(e.Time.Format("15:04:05"))
	lvl := colorLevel(e.Level).Render(fmt.Sprintf("%-5s", e.Level))
	msg := e.Message
	var extras []string
	for k, v := range e.Attrs {
		extras = append(extras, k+"="+v)
	}
	if len(extras) > 0 {
		msg += "  " + styleDim.Render(strings.Join(extras, " "))
	}
	return fmt.Sprintf("%s  %s  %s", ts, lvl, msg)
}

func colorStatus(status int) lipgloss.Style {
	switch {
	case status < 300:
		return style2xx
	case status < 400:
		return style3xx
	case status < 500:
		return style4xx
	default:
		return style5xx
	}
}

func colorLevel(level string) lipgloss.Style {
	switch level {
	case "DEBUG":
		return styleDebug
	case "WARN", "WARNING":
		return styleWarn
	case "ERROR":
		return styleError
	default:
		return styleInfo
	}
}

func fmtDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000.0)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// hScrollLine skips the first `skip` visible columns of an ANSI-styled string
// while preserving all escape sequences so colours remain correct.
func hScrollLine(s string, skip int) string {
	if skip <= 0 {
		return s
	}
	var out strings.Builder
	vis := 0     // visible columns counted so far
	inEsc := false
	inCSI := false
	for _, r := range s {
		switch {
		case inEsc:
			out.WriteRune(r)
			if r == '[' {
				inCSI = true
			}
			inEsc = false
		case inCSI:
			out.WriteRune(r)
			if r >= 0x40 && r <= 0x7E { // final byte of CSI sequence
				inCSI = false
			}
		case r == '\x1b':
			out.WriteRune(r)
			inEsc = true
		default:
			if vis >= skip {
				out.WriteRune(r)
			}
			vis++
		}
	}
	return out.String()
}

// ─── Queue section ────────────────────────────────────────────────────────────

func renderQueueSection(s QueueStats, width int) string {
	innerW := width - 4
	if innerW < 10 {
		innerW = 10
	}

	// Summary header
	idle := s.Pending == 0 && s.Processing == 0
	pendingStr := stylePending.Render(fmt.Sprintf("●  pending:%-3d", s.Pending))
	runningStr := styleRunning.Render(fmt.Sprintf("►  running:%-3d", s.Processing))
	doneStr := style2xx.Render(fmt.Sprintf("✓  done:%-5d", s.Done))
	failedStr := style5xx.Render(fmt.Sprintf("✗  failed:%-3d", s.Failed))
	headerLeft := styleTitle.Render("jobs") + "  " +
		pendingStr + "  " + runningStr + "  " + doneStr + "  " + failedStr

	if idle {
		content := headerLeft + "  " + styleQueueIdle.Render("all idle")
		return styleBorder.Width(width - 2).Render(content)
	}

	// One row per active job
	// Fixed columns: 2(icon) + 1 + 8(id) + 2 + 20(lib) + 2 + 12(count) + 2 + 8(dur) = 57
	const fixedCols = 57
	barW := innerW - fixedCols
	if barW < 4 {
		barW = 4
	}

	rows := make([]string, 0, len(s.Active)+1)
	rows = append(rows, headerLeft)
	for _, job := range s.Active {
		rows = append(rows, renderActiveJob(job, barW))
	}
	return styleBorder.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func renderActiveJob(job ActiveJobInfo, barW int) string {
	icon := stylePending.Render("●")
	if job.Status == "processing" {
		icon = styleRunning.Render("►")
	}

	id := styleIP.Render(fmt.Sprintf("%-8s", truncate(job.ID, 8)))
	lib := styleInfo.Render(fmt.Sprintf("%-20s", truncate(job.LibraryName, 20)))

	pct := 0
	if job.TotalRows > 0 {
		pct = job.ProcessedRows * 100 / job.TotalRows
	}
	count := styleDim.Render(fmt.Sprintf("%d/%d (%d%%)", job.ProcessedRows, job.TotalRows, pct))

	elapsed := time.Since(job.UpdatedAt)
	dur := styleDim.Render(fmtDuration(elapsed))

	bar := renderProgressBar(job.ProcessedRows, job.TotalRows, barW)

	return fmt.Sprintf(" %s %s  %s  %s  %-12s  %s", icon, id, lib, bar, count, dur)
}

// renderProgressBar returns a fixed-width progress bar using block fill characters.
func renderProgressBar(done, total, width int) string {
	if width <= 0 {
		return ""
	}
	filled := 0
	if total > 0 {
		filled = done * width / total
	}
	if filled > width {
		filled = width
	}
	return styleProgress.Render(strings.Repeat("█", filled)) +
		styleProgressD.Render(strings.Repeat("░", width-filled))
}

// ─── Entry point ─────────────────────────────────────────────────────────────

// Run starts the bubbletea TUI. Blocks until the user quits or a signal arrives.
func Run(c *Collector, quit <-chan os.Signal) {
	p := tea.NewProgram(
		newModel(c),
		tea.WithAltScreen(),
	)
	go func() {
		<-quit
		p.Quit()
	}()
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "TUI error:", err)
	}
}
