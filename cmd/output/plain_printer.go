// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"fmt"
	"io"
)

type plainPrinter struct {
	w io.Writer
}

func (p *plainPrinter) Print(v any) error {
	var writeErr error
	w := func(format string, a ...any) {
		if writeErr != nil {
			return
		}
		_, writeErr = fmt.Fprintf(p.w, format, a...)
	}
	switch t := v.(type) {
	case SyncResult:
		total := t.Synced + t.Skipped + t.Failed

		// List clusters that need attention first.
		for _, c := range t.Clusters {
			switch c.Status {
			case ClusterSyncStatusSkipped:
				reason := c.Reason
				if reason == "" {
					reason = "not ready"
				}
				w("  [-] %s — skipped (%s)\n", c.Name, reason)
			case ClusterSyncStatusFailed:
				reason := c.Reason
				if reason == "" {
					reason = "unknown error"
				}
				w("  [!] %s — failed: %s\n", c.Name, reason)
			}
		}

		if total == 0 {
			w("No clusters found to sync.\n")
			break
		}

		// Summary sentence.
		switch {
		case t.Failed > 0 && t.Skipped > 0:
			w("Synced %d of %d cluster(s). %d skipped, %d failed.\n",
				t.Synced, total, t.Skipped, t.Failed)
		case t.Failed > 0:
			w("Synced %d of %d cluster(s). %d failed.\n",
				t.Synced, total, t.Failed)
		case t.Skipped > 0:
			w("Synced %d of %d cluster(s). %d skipped (not ready).\n",
				t.Synced, total, t.Skipped)
		default:
			if total == 1 {
				w("Synced 1 cluster successfully.\n")
			} else {
				w("Synced all %d clusters successfully.\n", total)
			}
		}

	case SyncDryRunResult:
		w("Dry-run: no changes will be written.\n\n")
		total := t.Added + t.Removed + t.Modified
		if total == 0 {
			w("No changes detected.\n")
			break
		}
		printDiffSection := func(title string, entries []DiffEntry) {
			if len(entries) == 0 {
				return
			}
			n := 0
			for _, e := range entries {
				if e.ChangeType != "" {
					n++
				}
			}
			w("%s (%d change(s))\n", title, n)
			for _, e := range entries {
				switch e.ChangeType {
				case "added":
					w("  + %s\n", e.Name)
				case "removed":
					w("  - %s\n", e.Name)
				case "modified":
					w("  ~ %s\n", e.Name)
					for _, f := range e.Fields {
						if f.Old != "" {
							w("      %-12s  - %s\n", f.Field+":", f.Old)
						}
						if f.New != "" {
							w("      %-12s  + %s\n", f.Field+":", f.New)
						}
					}
				}
			}
			w("\n")
		}
		printDiffSection("CLUSTERS", t.Clusters)
		printDiffSection("CONTEXTS", t.Contexts)
		printDiffSection("AUTH INFOS", t.AuthInfos)
		w("Summary: %d added, %d removed, %d modified. No changes will be written.\n",
			t.Added, t.Removed, t.Modified)

	case ClusterVersionResult:
		w("Kubernetes version: %s\n", t.Version)

	case VersionInfo:
		w("cloudctl %s\n", t.Version)
		w("  git commit: %s\n", t.GitCommit)
		w("  build date: %s\n", t.BuildDate)
		w("  go:         %s %s %s\n", t.GoVersion, t.Compiler, t.Platform)

	default:
		w("%v\n", v)
	}
	return writeErr
}

func (p *plainPrinter) PrintError(err error) {
	fmt.Fprintf(p.w, "Error: %s\n", err.Error())
}

func (p *plainPrinter) StartSpinner(_ string) func() {
	return func() {}
}
