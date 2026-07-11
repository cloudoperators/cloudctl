// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"fmt"
	"io"
	"sort"
	"strings"
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
		total := t.Added + t.Removed + t.Modified
		if total == 0 {
			w("No changes detected.\n")
			break
		}
		w("Dry-run: no changes will be written.\n\n")
		w("CLUSTER ACCESSES (%d change(s))\n", total)
		p.printDryRunDiff(w, t)

	case ClusterVersionResult:
		w("Kubernetes version: %s\n", t.Version)

	case VersionInfo:
		w("cloudctl %s\n", t.Version)
		w("  git commit: %s\n", t.GitCommit)
		w("  build date: %s\n", t.BuildDate)
		w("  go:         %s %s %s\n", t.GoVersion, t.Compiler, t.Platform)

	case UpdateResult:
		switch t.Status {
		case UpdateStatusUpToDate:
			w("cloudctl is up to date (%s).\n", t.CurrentVersion)
		case UpdateStatusAvailable:
			w("A new version is available: %s (current: %s)\n", t.LatestVersion, t.CurrentVersion)
			w("Run `cloudctl update` to install it.\n")
		case UpdateStatusUpdated:
			w("cloudctl updated: %s -> %s\n", t.CurrentVersion, t.LatestVersion)
		default:
			w("cloudctl update status: %s (current: %s, latest: %s)\n", t.Status, t.CurrentVersion, t.LatestVersion)
		}

	default:
		w("%v\n", v)
	}
	return writeErr
}

func (p *plainPrinter) PrintError(err error) {
	_, _ = fmt.Fprintf(p.w, "Error: %s\n", err.Error())
}

func (p *plainPrinter) StartSpinner(_ string) func() {
	return func() {}
}

// printDryRunDiff renders dry-run output in git-style unified diff format:
// each changed field is shown as a - (old) and + (new) line.
func (p *plainPrinter) printDryRunDiff(w func(string, ...any), t SyncDryRunResult) {
	for _, a := range t.Accesses {
		switch a.ChangeType {
		case "added":
			w("+ %s\n", a.Name)
			if a.Server != "" {
				w("  + server:  %s\n", a.Server)
			}
		case "removed":
			w("- %s\n", a.Name)
			if a.Server != "" {
				w("  - server:  %s\n", a.Server)
			}
		case "modified":
			w("~ %s\n", a.Name)
			for _, f := range a.Fields {
				if f.Field == "Credentials" {
					w("  ~ credentials:  changed\n")
				} else if f.Old == f.New {
					// Both sides redacted to the same string — show as a generic change.
					w("  ~ %-12s  changed\n", strings.ToLower(f.Field)+":")
				} else {
					label := strings.ToLower(f.Field) + ":"
					// For per-argument Exec Args diffs, Old=="" means the arg was added
					// and New=="" means it was removed — print only the present side.
					if f.Field == "Exec Args" {
						if f.Old != "" {
							w("  - %-12s  %s\n", label, f.Old)
						} else {
							w("  + %-12s  %s\n", label, f.New)
						}
					} else {
						oldVal := f.Old
						if oldVal == "" {
							oldVal = "<empty>"
						}
						newVal := f.New
						if newVal == "" {
							newVal = "<empty>"
						}
						w("  - %-12s  %s\n", label, oldVal)
						w("  + %-12s  %s\n", label, newVal)
					}
				}
			}
		}
	}

	modBreakdown := modifiedBreakdown(t.Accesses)
	modSuffix := ""
	if t.Modified > 0 && len(modBreakdown) > 0 {
		modSuffix = " (" + strings.Join(modBreakdown, ", ") + ")"
	}
	w("\nSummary: %d added, %d removed, %d modified%s. No changes will be written.\n",
		t.Added, t.Removed, t.Modified, modSuffix)
}

// modifiedBreakdown counts individual change categories across all modified
// accesses and returns them as sorted "N reason" strings
// (e.g. ["45 credentials", "1 server"]).
// A single access entry can contribute to multiple categories.
func modifiedBreakdown(accesses []AccessDiff) []string {
	counts := make(map[string]int)
	for _, a := range accesses {
		if a.ChangeType != "modified" {
			continue
		}
		// Count each category at most once per access entry.
		seen := make(map[string]struct{})
		for _, f := range a.Fields {
			var cat string
			switch {
			case f.Field == "Credentials":
				cat = "credentials"
			case f.Field == "Server":
				cat = "server"
			case f.Field == "CA":
				cat = "ca"
			case f.Field == "Labels":
				cat = "labels"
			case f.Field == "Exec Args" || f.Field == "Auth type" || f.Field == "Auth provider":
				cat = "credentials"
			case strings.HasPrefix(f.Field, "Exec "):
				cat = "credentials"
			case strings.HasPrefix(f.Field, "auth-provider."):
				cat = "credentials"
			default:
				cat = "config"
			}
			if _, already := seen[cat]; !already {
				seen[cat] = struct{}{}
				counts[cat]++
			}
		}
		if len(a.Fields) == 0 {
			counts["config"]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	return parts
}
