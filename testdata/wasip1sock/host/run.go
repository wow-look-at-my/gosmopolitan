// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"io"

	"github.com/tetratelabs/wazero"
	wazerosys "github.com/tetratelabs/wazero/sys"
)

// RunConfig configures one module execution.
type RunConfig struct {
	Module []byte   // the wasm binary
	Args   []string // argv, including argv[0]
	Env    []string // KEY=VALUE pairs
	Stdout io.Writer
	Stderr io.Writer
	Trace  bool // log host-side socket activity to os.Stderr
}

// Run instantiates and runs a GOWASI=wasmedgesock wasip1 module under
// the custom WASI host, returning the module's exit code.
func Run(ctx context.Context, cfg RunConfig) (int, error) {
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	h := newWASIHost(cfg.Args, cfg.Env, cfg.Stdout, cfg.Stderr, cfg.Trace)
	if err := h.register(ctx, r); err != nil {
		return -1, err
	}

	compiled, err := r.CompileModule(ctx, cfg.Module)
	if err != nil {
		return -1, err
	}
	_, err = r.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_start"))
	var exitErr *wazerosys.ExitError
	if errors.As(err, &exitErr) {
		return int(exitErr.ExitCode()), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
