// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"io"
	"os"

	"golang.org/x/term"
)

// IsTTY returns true when os.Stdout is a real terminal.
func IsTTY() bool {
	return IsTTYWriter(os.Stdout)
}

// IsTTYWriter returns true when w is os.Stdout and that file descriptor is a
// real terminal. Use this instead of IsTTY() when the writer may have been
// redirected (e.g. cmd.OutOrStdout()).
func IsTTYWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
