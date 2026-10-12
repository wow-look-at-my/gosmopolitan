#!/usr/bin/env bash
# embedded-std.sh -- the embedded standard library end to end: go tool embedstd writes the blob.
set -euo pipefail

root=$PWD
# cosmocc first: embedstd builds std with cgo on, and the source tree's own listing and builds must see the same compiler.
export PATH="$root/bin:$root/misc/cosmo:/opt/cosmocc/bin:$PATH"
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

echo "== a linked tool's ID names the tool, not the binary carrying it"
GOOS=cosmo GOCOSMOAPPEND="$work/std.blob" go build -trimpath -ldflags=-buildid=other -o "$work/go-again.com" cmd/go/main
if cmp -s "$work/go.com" "$work/go-again.com"; then
	echo "a different -buildid produced the same binary" >&2
	exit 1
fi
/bin/sh "$work/go.com" tool compile -V=full >"$work/compile-id.txt"
/bin/sh "$work/go-again.com" tool compile -V=full >"$work/compile-id-again.txt"
grep -q "buildID=." "$work/compile-id.txt"
diff "$work/compile-id.txt" "$work/compile-id-again.txt"

echo "== the manifest lists what the source tree lists"
GOOS=cosmo go list -deps std | sort >"$work/source-std.txt"
embedded list std | sort >"$work/embedded-std.txt"
diff "$work/source-std.txt" "$work/embedded-std.txt"

echo "== GOROOT may name the go command by another path"
mkdir -p "$work/link" "$work/copy"
ln -s "$work/go.com" "$work/link/go"
cp "$work/go.com" "$work/copy/go"
(cd "$work/hello" && GOROOT="$work/go.com" GOCACHE="$work/cache" /bin/sh "$work/link/go" list std | sort >"$work/linked-std.txt")
diff "$work/source-std.txt" "$work/linked-std.txt"
(cd "$work/hello" && GOROOT="$work/go.com" GOCACHE="$work/cache" /bin/sh "$work/copy/go" list std | sort >"$work/copied-std.txt")
diff "$work/source-std.txt" "$work/copied-std.txt"

echo "== a build with no GOROOT compiles only the program"
embedded build -x -trimpath -ldflags=-buildid= -o "$work/embedded/hello.com" . 2>"$work/build.log"
if grep -E "compile .* -p (fmt|runtime|os) " "$work/build.log"; then
	echo "a standard package was compiled from source" >&2
	exit 1
fi
# A link served out of the build cache writes no importcfg; a link that ran names the embedded packages.
if grep -qE "/link( |$)" "$work/build.log"; then
	grep -q "self:std/" "$work/build.log"
fi
/bin/sh "$work/embedded/hello.com" one two

echo "== byte for byte the source tree's build"
(cd "$work/hello" && GOOS=cosmo go build -trimpath -ldflags=-buildid= -o "$work/source/hello.com" .)
cmp "$work/embedded/hello.com" "$work/source/hello.com"

echo "== two links of the same inputs are one file"
embedded build -trimpath -ldflags=-buildid= -o "$work/again/hello.com" .
cmp "$work/embedded/hello.com" "$work/again/hello.com"

echo "== vet, test and tidy run through it"
embedded vet .
embedded test .
embedded mod tidy

echo "== a cgo program builds through it, byte for byte the source tree's build, and runs"
cp -r testdata/cgoprobe "$work/cgoprobe"
(cd "$work/cgoprobe" && env -u GOROOT GOCACHE="$work/cache" /bin/sh "$work/go.com" build -trimpath -ldflags=-buildid= -o "$work/embedded/cgoprobe.com" .)
(cd "$work/cgoprobe" && GOOS=cosmo go build -trimpath -ldflags=-buildid= -o "$work/source/cgoprobe.com" .)
cmp "$work/embedded/cgoprobe.com" "$work/source/cgoprobe.com"
/bin/sh "$work/embedded/cgoprobe.com" >"$work/cgoprobe.out"
grep -q "ok callback" "$work/cgoprobe.out"
(cd "$work/cgoprobe" && env -u GOROOT GOCACHE="$work/cache" /bin/sh "$work/go.com" vet .)

# wasm_build builds hello for GOOS $1 on wasm through the carried std, compiling no standard package, byte for byte the source tree's build.
wasm_build() {
	if ! GOOS="$1" GOARCH=wasm embedded build -x -trimpath -ldflags=-buildid= -o "$work/embedded/hello.$1.wasm" . 2>"$work/build-$1.log"; then
		cat "$work/build-$1.log" >&2
		exit 1
	fi
	if grep -E "compile .* -p (fmt|runtime|os) " "$work/build-$1.log"; then
		echo "a standard package was compiled from source for $1/wasm" >&2
		exit 1
	fi
	# A link served out of the build cache writes no importcfg, as above.
	if grep -qE "/link( |$)" "$work/build-$1.log" && ! grep -q "self:std/$1_wasm/" "$work/build-$1.log"; then
		echo "the $1/wasm link names no embedded package" >&2
		exit 1
	fi
	(cd "$work/hello" && GOOS="$1" GOARCH=wasm go build -trimpath -ldflags=-buildid= -o "$work/source/hello.$1.wasm" .)
	cmp "$work/embedded/hello.$1.wasm" "$work/source/hello.$1.wasm"
}

echo "== js/wasm and wasip1/wasm programs build through it from the carried std"
wasm_build js
wasm_build wasip1

echo "== a listing hands an outside reader a standard package's export data as a file"
export_file=$(embedded list -export -f '{{.Export}}' fmt)
test -s "$export_file"

echo "== std itself is refused"
if embedded test fmt 2>"$work/refuse.log"; then
	echo "testing an embedded std package was accepted" >&2
	exit 1
fi
grep -q "embedded in this go command" "$work/refuse.log"
rm -rf "$work"
