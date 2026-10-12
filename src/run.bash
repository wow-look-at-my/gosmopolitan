#!/usr/bin/env bash
# Copyright 2009 The Go Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Environment variables that control run.bash:
#
# GO_TEST_SHARDS: number of "dist test" test shards that the
# $GOROOT/test directory will be sliced up into for parallel
# execution. Defaults to 1, unless GO_BUILDER_NAME is also specified,
# in which case it defaults to 10.
#
# GO_BUILDER_NAME: the name of the Go builder that's running the tests.
# Some tests are conditionally enabled or disabled based on the builder
# name or the builder name being non-empty.
#
# GO_TEST_TIMEOUT_SCALE: a non-negative integer factor to scale test timeout by.
# Defaults to 1.
#
# GO_TEST_ASMFLAGS: Additional go tool asm arguments to use when running the tests.
# This environment variable is an internal implementation detail between the
# Go build system (x/build) and cmd/dist to enable builders that need to control this,
# and will be removed if it stops being needed, or if a more general-purpose
# GO_ASMFLAGS environment variable gets added to make.bash and supersedes this
# test-only subset of it. See go.dev/issue/77427.

set -e

if [ ! -f ../bin/go ]; then
	echo 'run.bash must be run from $GOROOT/src after installing cmd/go' 1>&2
	exit 1
fi

export GOENV=off
eval $(../bin/go tool dist env)

unset CDPATH	# in case user has it set

export GOHOSTOS
export GOHOSTARCH
export CC

# GOOS and GOARCH name the port dist test is testing, and every go command it
# starts has to agree. This fork's go defaults to GOOS=cosmo, so an unexported
# GOOS left every test binary an APE, which the host cannot exec. dist test
# tests the host port, so GOOS and GOARCH are pinned to the host values.
GOOS=$GOHOSTOS
GOARCH=$GOHOSTARCH
export GOOS
export GOARCH

# dist test sets GOPATH, PATH and the resource limits itself.
exec ../bin/go tool dist test "$@"
