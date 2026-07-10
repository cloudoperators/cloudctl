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
	_, err = fmt.Fprint(p.w, string(b))
	return err
}

func (p *yamlPrinter) PrintError(err error) {
	b, _ := yaml.Marshal(ErrorResult{Error: err.Error()})
	_, _ = fmt.Fprint(p.w, string(b))
}

func (p *yamlPrinter) StartSpinner(_ string) func() {
	return func() {}
}
