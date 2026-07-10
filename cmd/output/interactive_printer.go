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
	var writeErr error
	w := func(format string, a ...any) {
		if writeErr != nil {
			return
		}
		_, writeErr = fmt.Fprintf(p.w, format, a...)
	}
	switch t := v.(type) {
	case SyncResult:
		writeErr = p.printSyncResult(t)
	case SyncDryRunResult:
		writeErr = p.printSyncDryRunResult(t)
	case ClusterVersionResult:
		w("%s %s\n", styleFaint.Render("Kubernetes version:"), styleBold.Render(t.Version))
	case VersionInfo:
		w("%s\n", styleHeader.Render("cloudctl "+t.Version))
		w("  git commit: %s\n", t.GitCommit)
		w("  build date: %s\n", t.BuildDate)
		w("  go:         %s %s %s\n", t.GoVersion, t.Compiler, t.Platform)
	default:
		w("%v\n", v)
	}
	return writeErr
}

func (p *interactivePrinter) PrintError(err error) {
	fmt.Fprintf(p.w, "%s %s\n", styleRed.Render("Error:"), err.Error())
}

func (p *interactivePrinter) printSyncResult(r SyncResult) error {
	var writeErr error
	w := func(format string, a ...any) {
		if writeErr != nil {
			return
		}
		_, writeErr = fmt.Fprintf(p.w, format, a...)
	}
	total := r.Synced + r.Skipped + r.Failed

	// Collect only clusters that need attention.
	var issues []ClusterSyncResult
	for _, c := range r.Clusters {
		if c.Status != ClusterSyncStatusSynced {
			issues = append(issues, c)
		}
	}

	if len(issues) > 0 {
		const (
			colCluster = 30
			colStatus  = 9
		)
		header := fmt.Sprintf("%-*s  %-*s  %s", colCluster, "CLUSTER", colStatus, "STATUS", "REASON")
		w("%s\n", styleHeader.Render(header))

		for _, c := range issues {
			var icon, statusStr string
			switch c.Status {
			case ClusterSyncStatusFailed:
				icon = styleRed.Render("✗")
				statusStr = styleRed.Render("failed")
			default:
				icon = styleYellow.Render("!")
				statusStr = styleYellow.Render("skipped")
			}

			name := c.Name
			if len(name) > colCluster-2 {
				name = name[:colCluster-5] + "..."
			}
			reason := c.Reason
			if reason == "" {
				if c.Status == ClusterSyncStatusSkipped {
					reason = "not ready"
				} else {
					reason = "unknown error"
				}
			}

			w("%s %-*s  %-*s  %s\n",
				icon, colCluster-1, name, colStatus, statusStr, reason,
			)
		}
		w("\n")
	}

	// Summary sentence.
	var summary string
	switch {
	case total == 0:
		summary = styleFaint.Render("No clusters found to sync.")
	case r.Failed > 0 && r.Skipped > 0:
		summary = fmt.Sprintf("%s  %s  %s",
			styleGreen.Render(fmt.Sprintf("Synced %d of %d cluster(s).", r.Synced, total)),
			styleYellow.Render(fmt.Sprintf("%d skipped,", r.Skipped)),
			styleRed.Render(fmt.Sprintf("%d failed.", r.Failed)),
		)
	case r.Failed > 0:
		summary = fmt.Sprintf("%s  %s",
			styleGreen.Render(fmt.Sprintf("Synced %d of %d cluster(s).", r.Synced, total)),
			styleRed.Render(fmt.Sprintf("%d failed.", r.Failed)),
		)
	case r.Skipped > 0:
		summary = fmt.Sprintf("%s  %s",
			styleGreen.Render(fmt.Sprintf("Synced %d of %d cluster(s).", r.Synced, total)),
			styleYellow.Render(fmt.Sprintf("%d skipped (not ready).", r.Skipped)),
		)
	default:
		if total == 1 {
			summary = styleGreen.Render("Synced 1 cluster successfully.")
		} else {
			summary = styleGreen.Render(fmt.Sprintf("Synced all %d clusters successfully.", total))
		}
	}
	w("%s\n", summary)
	return writeErr
}

func (p *interactivePrinter) printSyncDryRunResult(r SyncDryRunResult) error {
	var writeErr error
	w := func(format string, a ...any) {
		if writeErr != nil {
			return
		}
		_, writeErr = fmt.Fprintf(p.w, format, a...)
	}

	total := r.Added + r.Removed + r.Modified
	if total == 0 {
		w("%s\n", styleFaint.Render("No changes detected."))
		return writeErr
	}

	w("%s\n\n", styleFaint.Render("Dry-run: no changes will be written."))
	w("%s (%d change(s))\n", styleHeader.Render("CLUSTER ACCESSES"), total)

	for _, a := range r.Accesses {
		switch a.ChangeType {
		case "added":
			w("  %s %-20s  %s\n", styleGreen.Render("+"), a.Name, styleFaint.Render(a.Server))
		case "removed":
			w("  %s %-20s  %s\n", styleRed.Render("-"), a.Name, styleFaint.Render("(removed)"))
		case "modified":
			w("  %s %s\n", styleYellow.Render("~"), a.Name)
			for _, f := range a.Fields {
				if f.Field == "Credentials" {
					w("      %-16s  %s\n", "Credentials:", styleYellow.Render("changed"))
				} else {
					if f.Old != "" {
						w("      %-16s  %s\n", f.Field+":", styleRed.Render("- "+f.Old))
					}
					if f.New != "" {
						w("      %-16s  %s\n", f.Field+":", styleGreen.Render("+ "+f.New))
					}
				}
			}
		}
	}

	w("\n")
	summaryParts := []string{
		styleGreen.Render(fmt.Sprintf("%d added", r.Added)),
		styleRed.Render(fmt.Sprintf("%d removed", r.Removed)),
		styleYellow.Render(fmt.Sprintf("%d modified", r.Modified)),
	}
	w("Summary: %s, %s, %s. %s\n",
		summaryParts[0], summaryParts[1], summaryParts[2],
		styleFaint.Render("No changes will be written."))

	return writeErr
}
