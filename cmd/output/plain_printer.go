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

		// Determine column width for the name column.
		nameWidth := len("NAME")
		for _, a := range t.Accesses {
			if len(a.Name) > nameWidth {
				nameWidth = len(a.Name)
			}
		}
		nameWidth += 2

		// Print added/removed entries with server URL inline.
		hasAddedOrRemoved := t.Added > 0 || t.Removed > 0
		if hasAddedOrRemoved {
			w("  %-*s  %s\n", nameWidth, "NAME", "SERVER")
			for _, a := range t.Accesses {
				switch a.ChangeType {
				case "added":
					w("  + %-*s  %s\n", nameWidth, a.Name, a.Server)
				case "removed":
					w("  - %-*s  (removed)\n", nameWidth, a.Name)
				}
			}
		}

		// Print modified entries. Entries with only credential changes are
		// shown as compact single-line table rows. Entries with field-level
		// values (server, ca, etc.) expand with old → new detail lines.
		if t.Modified > 0 {
			if hasAddedOrRemoved {
				w("\n")
			}
			w("  %-*s  %s\n", nameWidth, "NAME", "CHANGES")
			for _, a := range t.Accesses {
				if a.ChangeType != "modified" {
					continue
				}
				if hasDetailFields(a) {
					w("  ~ %s\n", a.Name)
					for _, f := range a.Fields {
						if f.Field == "Credentials" {
							w("      %-14s  changed\n", "credentials:")
						} else if f.Old != "" || f.New != "" {
							label := strings.ToLower(f.Field) + ":"
							w("      %-14s  %s → %s\n", label, orElse(f.Old, "(none)"), orElse(f.New, "(none)"))
						}
					}
				} else {
					w("  ~ %-*s  %s\n", nameWidth, a.Name, accessChangeSummary(a))
				}
			}
		}

		// Build summary with per-change-type breakdown for modified entries.
		modBreakdown := modifiedBreakdown(t.Accesses)
		modSuffix := ""
		if t.Modified > 0 && len(modBreakdown) > 0 {
			modSuffix = " (" + strings.Join(modBreakdown, ", ") + ")"
		}
		w("\nSummary: %d added, %d removed, %d modified%s. No changes will be written.\n",
			t.Added, t.Removed, t.Modified, modSuffix)

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

// hasDetailFields returns true when the access diff contains at least one
// non-Credentials field that has old/new values worth printing inline.
func hasDetailFields(a AccessDiff) bool {
	for _, f := range a.Fields {
		if f.Field != "Credentials" && (f.Old != "" || f.New != "") {
			return true
		}
	}
	return false
}

// orElse returns s if non-empty, otherwise fallback.
func orElse(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

// accessChangeSummary returns a compact, human-readable string describing what
// changed in a modified AccessDiff entry (e.g. "credentials", "server", "config").
func accessChangeSummary(a AccessDiff) string {
	if len(a.Fields) == 0 {
		return "config"
	}
	seen := make(map[string]struct{}, len(a.Fields))
	for _, f := range a.Fields {
		switch f.Field {
		case "Credentials":
			seen["credentials"] = struct{}{}
		case "Server":
			seen["server"] = struct{}{}
		case "CA":
			seen["ca"] = struct{}{}
		case "Labels":
			seen["labels"] = struct{}{}
		default:
			seen["config"] = struct{}{}
		}
	}
	parts := make([]string, 0, len(seen))
	for k := range seen {
		parts = append(parts, k)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// modifiedBreakdown counts distinct change reasons across all modified accesses
// and returns them as sorted "N reason" strings (e.g. ["45 credentials", "129 config"]).
func modifiedBreakdown(accesses []AccessDiff) []string {
	counts := make(map[string]int)
	for _, a := range accesses {
		if a.ChangeType != "modified" {
			continue
		}
		counts[accessChangeSummary(a)]++
	}
	if len(counts) == 0 {
		return nil
	}
	// Collect keys and sort for deterministic output.
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
