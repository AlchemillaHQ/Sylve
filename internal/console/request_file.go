// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func loadStrictJSONFile[T any](path, kind string) (T, error) {
	var request T

	file, err := os.Open(path)
	if err != nil {
		return request, fmt.Errorf("open %s file %q: %w", kind, path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("decode %s file %q: %w", kind, path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return request, fmt.Errorf("decode %s file %q: contains more than one JSON value", kind, path)
		}
		return request, fmt.Errorf("decode %s file %q: %w", kind, path, err)
	}

	return request, nil
}
