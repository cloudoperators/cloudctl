// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/cloudoperators/cloudctl/cmd/output"
)

var (
	// These can be overridden at build time via -ldflags
	//   -ldflags="-X 'github.com/cloudoperators/cloudctl/cmd.Version=v1.2.3' -X 'github.com/cloudoperators/cloudctl/cmd.GitCommit=abcdef' -X 'github.com/cloudoperators/cloudctl/cmd.BuildDate=2025-08-22T12:34:56Z'"
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print cloudctl version and build information",
	Long: `Prints the cloudctl version, git commit, build date, and Go toolchain details.

Use --output to get machine-readable output for scripting:

  cloudctl version -o json | jq .version
  cloudctl version -o yaml

Use --short to print only the version number (useful in shell scripts):

  $(cloudctl version --short)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		info := output.VersionInfo{
			Version:   Version,
			GitCommit: GitCommit,
			BuildDate: BuildDate,
			GoVersion: runtime.Version(),
			Compiler:  runtime.Compiler,
			Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		}

		if viper.GetBool("short") {
			fmt.Println(info.Version)
			return nil
		}

		format, err := output.ParseFormat(viper.GetString("output"))
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		printer := output.New(format, output.IsTTYWriter(w), w)
		return printer.Print(info)
	},
}

func init() {
	versionCmd.Flags().Bool("short", false, "Print only the version number")

	// BindPFlags can theroretically return an error if called with `nil` as an argument
	// which should never happened after at least one flag was defined. That's why the output
	// there is ignored.
	viper.BindPFlags(versionCmd.Flags())
}
