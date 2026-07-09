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
				fmt.Fprintf(p.w, "  [-] %s — skipped (%s)\n", c.Name, reason)
			case ClusterSyncStatusFailed:
				reason := c.Reason
				if reason == "" {
					reason = "unknown error"
				}
				fmt.Fprintf(p.w, "  [!] %s — failed: %s\n", c.Name, reason)
			}
		}

		if total == 0 {
			fmt.Fprintln(p.w, "No clusters found to sync.")
			break
		}

		// Summary sentence.
		switch {
		case t.Failed > 0 && t.Skipped > 0:
			fmt.Fprintf(p.w, "Synced %d of %d cluster(s). %d skipped, %d failed.\n",
				t.Synced, total, t.Skipped, t.Failed)
		case t.Failed > 0:
			fmt.Fprintf(p.w, "Synced %d of %d cluster(s). %d failed.\n",
				t.Synced, total, t.Failed)
		case t.Skipped > 0:
			fmt.Fprintf(p.w, "Synced %d of %d cluster(s). %d skipped (not ready).\n",
				t.Synced, total, t.Skipped)
		default:
			if total == 1 {
				fmt.Fprintf(p.w, "Synced 1 cluster successfully.\n")
			} else {
				fmt.Fprintf(p.w, "Synced all %d clusters successfully.\n", total)
			}
		}

	case ClusterVersionResult:
		fmt.Fprintf(p.w, "Kubernetes version: %s\n", t.Version)

	case VersionInfo:
		fmt.Fprintf(p.w, "cloudctl %s\n", t.Version)
		fmt.Fprintf(p.w, "  git commit: %s\n", t.GitCommit)
		fmt.Fprintf(p.w, "  build date: %s\n", t.BuildDate)
		fmt.Fprintf(p.w, "  go:         %s %s %s\n", t.GoVersion, t.Compiler, t.Platform)

	default:
		fmt.Fprintf(p.w, "%v\n", v)
	}
	return nil
}

func (p *plainPrinter) PrintError(err error) {
	fmt.Fprintf(p.w, "Error: %s\n", err.Error())
}

func (p *plainPrinter) StartSpinner(_ string) func() {
	return func() {}
}
