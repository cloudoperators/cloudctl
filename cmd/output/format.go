// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output

import "fmt"

// Format represents the output format for commands.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
	FormatYAML Format = "yaml"
)

// ParseFormat parses a format string and returns the corresponding Format.
// Returns an error for unknown values.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatText, FormatJSON, FormatYAML:
		return Format(s), nil
	default:
		return "", fmt.Errorf("unknown output format %q: must be one of text, json, yaml", s)
	}
}
