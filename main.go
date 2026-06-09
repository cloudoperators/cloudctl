// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudoperators/cloudctl/cmd"

	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

func main() {
	// Graceful cancellation on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := cmd.Execute(ctx); err != nil {
		// Print errors to stderr
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
