package main

import (
	"fmt"
	"os"

	"github.com/spf13/viper"

	"github.com/cloudoperators/cloudctl/cmd"

	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

func main() {
	// Optionally read environment variables, config files, etc.
	viper.SetEnvPrefix("CLOUDCTL")
	viper.AutomaticEnv()

	if err := cmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
