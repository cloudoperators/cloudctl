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
		for _, c := range t.Clusters {
			icon := "[+]"
			switch c.Status {
			case ClusterSyncStatusSkipped:
				icon = "[-]"
			case ClusterSyncStatusFailed:
				icon = "[!]"
			}
			if c.Reason != "" {
				fmt.Fprintf(p.w, "%s %s (%s): %s\n", icon, c.Name, c.Status, c.Reason)
			} else {
				fmt.Fprintf(p.w, "%s %s (%s)\n", icon, c.Name, c.Status)
			}
		}
		fmt.Fprintf(p.w, "synced=%d skipped=%d failed=%d\n", t.Synced, t.Skipped, t.Failed)

	case ClusterVersionResult:
		fmt.Fprintln(p.w, t.Version)

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

func (p *plainPrinter) StartSpinner(_ string) func() {
	return func() {}
}
