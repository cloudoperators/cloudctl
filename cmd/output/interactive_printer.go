// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Styles used for interactive output.
var (
	styleHeader = lipgloss.NewStyle().Bold(true).Underline(true)
	styleGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleFaint  = lipgloss.NewStyle().Faint(true)
)

type interactivePrinter struct {
	w io.Writer
}

// spinnerModel is a minimal bubbletea model for the spinner.
type spinnerModel struct {
	spinner spinner.Model
	label   string
	done    bool
}

type quitSpinnerMsg struct{}

func (m spinnerModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case quitSpinnerMsg:
		m.done = true
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m spinnerModel) View() string {
	if m.done {
		return ""
	}
	return fmt.Sprintf("%s %s\n", m.spinner.View(), m.label)
}

func (p *interactivePrinter) StartSpinner(label string) func() {
	sp := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("5"))),
	)
	m := spinnerModel{spinner: sp, label: label}
	prog := tea.NewProgram(m, tea.WithOutput(p.w), tea.WithInput(nil))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = prog.Run()
	}()

	// Give the program a moment to start before returning.
	time.Sleep(10 * time.Millisecond)

	return func() {
		prog.Send(quitSpinnerMsg{})
		wg.Wait()
	}
}

func (p *interactivePrinter) Print(v any) error {
	switch t := v.(type) {
	case SyncResult:
		p.printSyncResult(t)
	case ClusterVersionResult:
		fmt.Fprintf(p.w, "%s %s\n", styleFaint.Render("cluster version:"), styleBold.Render(t.Version))
	case VersionInfo:
		fmt.Fprintf(p.w, "%s\n", styleHeader.Render("cloudctl "+t.Version))
		fmt.Fprintf(p.w, "  git commit: %s\n", t.GitCommit)
		fmt.Fprintf(p.w, "  build date: %s\n", t.BuildDate)
		fmt.Fprintf(p.w, "  go:         %s %s %s\n", t.GoVersion, t.Compiler, t.Platform)
	default:
		fmt.Fprintf(p.w, "%v\n", v)
	}
	return nil
}

func (p *interactivePrinter) printSyncResult(r SyncResult) {
	// Column widths
	const (
		colCluster = 30
		colContext = 30
		colStatus  = 9
	)

	// Header
	header := fmt.Sprintf("%-*s  %-*s  %-*s  %s",
		colCluster, "CLUSTER",
		colContext, "CONTEXT",
		colStatus, "STATUS",
		"REASON",
	)
	fmt.Fprintln(p.w, styleHeader.Render(header))

	for _, c := range r.Clusters {
		var icon, statusStr string
		switch c.Status {
		case ClusterSyncStatusSynced:
			icon = styleGreen.Render("✓")
			statusStr = styleGreen.Render(string(c.Status))
		case ClusterSyncStatusFailed:
			icon = styleRed.Render("✗")
			statusStr = styleRed.Render(string(c.Status))
		default:
			icon = styleYellow.Render("!")
			statusStr = styleYellow.Render(string(c.Status))
		}

		name := c.Name
		if len(name) > colCluster-2 {
			name = name[:colCluster-5] + "..."
		}
		ctx := c.Context
		if len(ctx) > colContext-2 {
			ctx = ctx[:colContext-5] + "..."
		}

		fmt.Fprintf(p.w, "%s %-*s  %-*s  %-*s  %s\n",
			icon,
			colCluster-1, name,
			colContext, ctx,
			colStatus, statusStr,
			c.Reason,
		)
	}

	// Summary footer
	summary := fmt.Sprintf("synced=%s  skipped=%s  failed=%s",
		styleGreen.Render(fmt.Sprintf("%d", r.Synced)),
		styleYellow.Render(fmt.Sprintf("%d", r.Skipped)),
		styleRed.Render(fmt.Sprintf("%d", r.Failed)),
	)
	fmt.Fprintf(p.w, "\n%s\n", summary)
}
