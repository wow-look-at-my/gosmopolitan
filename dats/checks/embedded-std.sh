#!/usr/bin/env bash
# embedded-std.sh -- the embedded standard library end to end: go tool
# embedstd writes the blob, a cosmo go command links it in, and with no
# GOROOT that command lists std from its manifest, builds a program byte
# for byte as the source tree does, links it the same way twice, vets and
# tests it, and refuses to test std. Run from the repository root with the
# toolchain built.
set -euo pipefail

root=$PWD
export PATH="$root/bin:$root/misc/cosmo:$PATH"
work=$(mktemp -d)
mkdir -p "$work/hello" "$work/embedded" "$work/source" "$work/again"
cat >"$work/hello/go.mod" <<'EOF'
module hello

go 1.27
EOF
cat >"$work/hello/hello.go" <<'EOF'
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() { fmt.Println(strings.ToUpper("hello"), len(os.Args)) }
EOF
cat >"$work/hello/hello_test.go" <<'EOF'
package main

import "testing"

func TestHello(t *testing.T) { t.Log("ran") }
EOF

echo "== the blob"
go tool embedstd -o "$work/std.blob"
ls -la "$work/std.blob"

echo "== a cosmo go command carrying it"
GOOS=cosmo GOCOSMOAPPEND="$work/std.blob" go build -trimpath -o "$work/go.com" cmd/go/main
ls -la "$work/go.com"

# embedded runs the go command that carries the blob, with no GOROOT and
# its own build cache, from the hello module.
embedded() {
	(cd "$work/hello" && env -u GOROOT GOCACHE="$work/cache" /bin/sh "$work/go.com" "$@")
}

echo "== the manifest lists what the source tree lists"
GOOS=cosmo go list -deps std | sort >"$work/source-std.txt"
embedded list std | sort >"$work/embedded-std.txt"
diff "$work/source-std.txt" "$work/embedded-std.txt"

echo "== a build with no GOROOT compiles only the program"
embedded build -x -trimpath -ldflags=-buildid= -o "$work/embedded/hello.com" . 2>"$work/build.log"
if grep -E "compile .* -p (fmt|runtime|os) " "$work/build.log"; then
	echo "a standard package was compiled from source" >&2
	exit 1
fi
grep -q "self:std/" "$work/build.log"
/bin/sh "$work/embedded/hello.com" one two

echo "== byte for byte the source tree's build"
(cd "$work/hello" && GOOS=cosmo go build -trimpath -ldflags=-buildid= -o "$work/source/hello.com" .)
cmp "$work/embedded/hello.com" "$work/source/hello.com"

echo "== two links of the same inputs are one file"
embedded build -trimpath -ldflags=-buildid= -o "$work/again/hello.com" .
cmp "$work/embedded/hello.com" "$work/again/hello.com"

echo "== vet and test run through it"
embedded vet .
embedded test .

echo "== std itself is refused"
if embedded test fmt 2>"$work/refuse.log"; then
	echo "testing an embedded std package was accepted" >&2
	exit 1
fi
grep -q "embedded in this go command" "$work/refuse.log"
rm -rf "$work"
