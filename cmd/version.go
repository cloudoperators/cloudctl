// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	// These can be overridden at build time via -ldflags
	//   -ldflags="-X 'github.com/cloudoperators/cloudctl/cmd.Version=v1.2.3' -X 'github.com/cloudoperators/cloudctl/cmd.GitCommit=abcdef' -X 'github.com/cloudoperators/cloudctl/cmd.BuildDate=2025-08-22T12:34:56Z'"
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

type versionInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	Compiler  string `json:"compiler"`
	Platform  string `json:"platform"`
}

var (
	versionShort bool
	versionJSON  bool
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the cloudctl version information",
	RunE: func(cmd *cobra.Command, args []string) error {
		info := versionInfo{
			Version:   Version,
			GitCommit: GitCommit,
			BuildDate: BuildDate,
			GoVersion: runtime.Version(),
			Compiler:  runtime.Compiler,
			Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		}

		if versionJSON {
			b, err := json.MarshalIndent(info, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(b))
			return nil
		}

		if versionShort {
			fmt.Println(info.Version)
			return nil
		}

		fmt.Printf("cloudctl %s\n", info.Version)
		fmt.Printf("  git commit: %s\n", info.GitCommit)
		fmt.Printf("  build date: %s\n", info.BuildDate)
		fmt.Printf("  go:         %s %s %s/%s\n", info.GoVersion, info.Compiler, runtime.GOOS, runtime.GOARCH)
		return nil
	},
}

func init() {
	versionCmd.Flags().BoolVar(&versionShort, "short", false, "print only the version number")
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "print version information as JSON")
}
