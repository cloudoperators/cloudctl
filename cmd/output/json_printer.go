// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"encoding/json"
	"fmt"
	"io"
)

type jsonPrinter struct {
	w io.Writer
}

func (p *jsonPrinter) Print(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(p.w, string(b))
	return nil
}

func (p *jsonPrinter) StartSpinner(_ string) func() {
	return func() {}
}
