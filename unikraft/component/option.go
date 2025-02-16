// SPDX-License-Identifier: BSD-3-Clause
// Copyright (c) 2022, Unikraft GmbH and The KraftKit Authors.
// Licensed under the BSD-3-Clause License (the "License").
// You may not use this file expect in compliance with the License.
package component

import (
	"context"

	"kraftkit.sh/unikraft"
)

type ComponentOption func(cc *ComponentConfig) error

func WithWorkdir(path string) ComponentOption {
	return func(cc *ComponentConfig) error {
		cc.workdir = path
		return nil
	}
}

func WithContext(ctx context.Context) ComponentOption {
	return func(cc *ComponentConfig) error {
		cc.ctx = ctx
		return nil
	}
}

func WithType(t unikraft.ComponentType) ComponentOption {
	return func(cc *ComponentConfig) error {
		cc.ctype = t
		return nil
	}
}
