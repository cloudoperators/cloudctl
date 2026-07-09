// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"os"

	"golang.org/x/term"
)

// IsTTY returns true when os.Stdout is a real terminal.
func IsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
