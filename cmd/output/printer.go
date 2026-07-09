// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output

import "io"

// Printer handles formatting and writing command output.
type Printer interface {
	// Print formats the value v and writes it to the configured writer.
	Print(v any) error
	// PrintError writes a fatal error in the configured output format.
	// For json/yaml this produces a machine-parseable {"error":"..."} document.
	// For text this produces a plain "Error: ..." line.
	PrintError(err error)
	// StartSpinner starts a spinner with the given label and returns a stop function.
	// Calling stop() halts the spinner. In non-interactive printers this is a no-op.
	StartSpinner(label string) (stop func())
}

// New returns the appropriate Printer for the given format and TTY state.
// The writer w receives successful output (typically cmd.OutOrStdout()).
//
//   - JSON format    → jsonPrinter  (no spinner)
//   - YAML format    → yamlPrinter  (no spinner)
//   - text + TTY     → interactivePrinter (spinner + styled table)
//   - text + non-TTY → plainPrinter (plain text, no ANSI)
func New(format Format, isTTY bool, w io.Writer) Printer {
	switch format {
	case FormatJSON:
		return &jsonPrinter{w: w}
	case FormatYAML:
		return &yamlPrinter{w: w}
	default:
		if isTTY {
			return &interactivePrinter{w: w}
		}
		return &plainPrinter{w: w}
	}
}

// NewForError returns a Printer that writes to errW (typically os.Stderr).
// Use this printer solely for PrintError calls when the output format is
// known but the success writer is separate from the error writer.
func NewForError(format Format, errW io.Writer) Printer {
	return New(format, false, errW)
}
