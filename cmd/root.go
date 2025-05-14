// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cloudctl",
	Short: "A CLI tool to access Greenhouse clusters",
	Long: `cloudctl is a command line interface that helps:
    
    1) Fetch and merge kubeconfigs from central Greenhouse cluster`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Add subcommands here
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(clusterVersionCmd)
}
