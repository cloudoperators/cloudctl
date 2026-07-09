// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudoperators/cloudctl/cmd"
	"github.com/cloudoperators/cloudctl/cmd/output"

	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

func main() {
	// Graceful cancellation on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := cmd.Execute(ctx); err != nil {
		// Build a printer in the requested output format so errors are
		// machine-parseable when -o json or -o yaml is used.
		format, fmtErr := output.ParseFormat(cmd.OutputFormat())
		if fmtErr != nil {
			format = output.FormatText
		}
		p := output.NewForError(format, os.Stderr)

		if errors.Is(err, context.DeadlineExceeded) {
			p.PrintError(errors.New("timed out waiting for the API server to respond — the endpoint may be unreachable; use --timeout to adjust the deadline"))
		} else if errors.Is(err, context.Canceled) {
			p.PrintError(errors.New("operation cancelled"))
		} else {
			p.PrintError(err)
		}
		os.Exit(1)
	}
}
