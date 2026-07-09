// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
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
		fmt.Fprintln(os.Stderr, friendlyError(err))
		os.Exit(1)
	}
}

// friendlyError converts known noisy error types into concise messages.
func friendlyError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "Error: timed out waiting for the API server to respond. The endpoint may be unreachable. Try --timeout to adjust the deadline."
	}
	if errors.Is(err, context.Canceled) {
		return "Error: operation cancelled."
	}
	return "Error: " + err.Error()
}
