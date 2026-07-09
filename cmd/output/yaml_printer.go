// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package output

import (
	"fmt"
	"io"

	"sigs.k8s.io/yaml"
)

type yamlPrinter struct {
	w io.Writer
}

func (p *yamlPrinter) Print(v any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	fmt.Fprint(p.w, string(b))
	return nil
}

func (p *yamlPrinter) StartSpinner(_ string) func() {
	return func() {}
}
