package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cloudctl",
	Short: "A CLI tool to manage clusters, including OIDC login and kubeconfig sync.",
	Long: `cloudctl is a command line interface that helps:
    
    1) Fetch and merge Kubeconfigs from a special cluster CRD`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Add subcommands here
	rootCmd.AddCommand(syncCmd)
}

// A utility function that might be used across multiple commands
func printDebugMessage(msg string) {
	fmt.Println("DEBUG:", msg)
}
