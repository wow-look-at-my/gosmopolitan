#!/bin/sh
# Boots an APE with every writable path made read-only, on linux and darwin.
# The loader reads the APE where it lies, so a read-only host is enough.
#
# One case per invocation:
#   resident  a loader is installed read-only, and the APE runs
#   ram       no loader at all, so the embedded one goes to tmpfs and runs
#   container the docker --read-only shape, where only the bind mount execs
#   refuse    nothing is writable and no loader exists, so it must exit 121
#
# The APE goes through /bin/sh on purpose. A binfmt_misc entry would otherwise
# take the exec, and this has to reach the boot script.
set -u

case=${1:?usage: readonly-boot.sh resident|ram|container|refuse}
ape=${2:?usage: readonly-boot.sh <case> <ape>}

# A mode bit means nothing to root, so root gets the same directories as
# read-only BIND MOUNTS instead, inside a mount namespace of its own. Either
# way the canary below proves the write actually fails before anything runs.
if [ "$(id -u)" = 0 ] && [ "${RO_BOOT_NS:-}" != 1 ]; then
	command -v unshare >/dev/null 2>&1 || {
		echo "run this as a normal user, or install unshare: root ignores a mode bit" >&2
		exit 1
	}
	exec unshare -m env RO_BOOT_NS=1 sh "$0" "$case" "$ape"
fi

work=${TMPDIR:-/tmp}/ro-boot-$case.$$

# A bind mount holds the directory, so it comes off before the delete.
cleanup() {
	if [ "${RO_BOOT_NS:-}" = 1 ]; then
		for d in "$work/nowrite" "$work/prog" "$work/lib"; do
			umount "$d" 2>/dev/null
		done
	fi
	chmod -R u+w "$work" 2>/dev/null
	rm -rf "$work"
}
trap cleanup EXIT

# Makes one directory read-only, by the means this user has.
ro_dir() {
	if [ "${RO_BOOT_NS:-}" = 1 ]; then
		mount -o bind "$1" "$1" && mount -o remount,ro,bind "$1"
	else
		chmod 555 "$1"
	fi
}

# The loader this host needs, taken from the toolchain that carries it. This
# is the copy docs/APE-BOOT.md tells an image to install, so the resident case
# covers the documented install as well as the read-only boot.
host_loader() {
	case $(uname -s) in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) echo "no loader for $(uname -s)" >&2; return 1 ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) echo "no loader for $(uname -m)" >&2; return 1 ;;
	esac
	src=src/cmd/link/internal/ld/apeld/bin/apeld-$os-$arch
	[ -f "$src" ] || { echo "$src is missing from the checkout" >&2; return 1; }
	# The name stays as it is. A darwin loader's ad-hoc signature names the
	# file it was signed as, and a rename breaks the signature.
	loader=$1/apeld-$os-$arch
	cp "$src" "$loader" || return 1
	chmod 755 "$loader"
}

mkdir -p "$work" || exit 1

# A read-only directory for the unpack, and a read-only copy of the APE's own
# directory. 0555 refuses a write for anybody but root, and CI runs as a user.
mkdir -p "$work/nowrite" "$work/prog" "$work/lib"
cp "$ape" "$work/prog/prog.com" || exit 1
chmod 755 "$work/prog/prog.com"

if [ "$case" = resident ]; then
	host_loader "$work/lib" || exit 1
fi

for d in "$work/nowrite" "$work/prog" "$work/lib"; do
	ro_dir "$d" || exit 1
done

# Prove the read-only claim before trusting the run: a write here must fail.
if (: > "$work/nowrite/canary") 2>/dev/null; then
	echo "the unpack directory is writable, so this case proves nothing" >&2
	exit 1
fi

set -- 10 5
if [ "$case" = resident ]; then
	out=$(APE_LOADERDIR="$work/nowrite" APE_LOADER="$loader" \
		/bin/sh "$work/prog/prog.com" "$@" 2>&1)
	code=$?
	if [ "$code" -ne 0 ] || [ "$out" != fizzbuzz ]; then
		echo "read-only boot through a resident loader failed: exit $code, output '$out'" >&2
		exit 1
	fi
	echo "read-only boot through a resident loader: fizzbuzz"
	exit 0
fi

if [ "$case" = container ]; then
	# The shape `docker run --read-only` leaves: /dev/shm is writable and
	# noexec, /tmp is gone, and the program arrives on a bind mount. The
	# program's own directory is the only candidate left.
	command -v unshare >/dev/null 2>&1 || { echo "unshare is needed to build the container shape" >&2; exit 1; }
	if ! { [ -d /opt ] && [ -d /dev/shm ]; }; then
		echo "/opt and /dev/shm have to exist to build the shape" >&2
		exit 1
	fi
	# -r maps this user to root inside, which is what buys the mounts with no
	# sudo. readonly-boot-container.sh builds the shape and runs the program.
	out=$(unshare -rm sh dats/test/readonly-boot-container.sh "$ape" "$@" 2>&1)
	code=$?
	case $code in
	2) echo "the container shape could not be built: '$out'" >&2; exit 1 ;;
	3) echo "/tmp stayed writable, so this case proves nothing" >&2; exit 1 ;;
	4) echo "/dev/shm is not writable, so the noexec claim proves nothing" >&2; exit 1 ;;
	6) echo "the run left a loader beside the program" >&2; exit 1 ;;
	esac
	if [ "$code" -ne 0 ] || [ "$out" != fizzbuzz ]; then
		echo "the container shape must still run: exit $code, output '$out'" >&2
		exit 1
	fi
	echo "read-only container, noexec /dev/shm: fizzbuzz, and nothing left"
	exit 0
fi

if [ "$case" = ram ]; then
	# Nothing resident, and the program's own directory read-only. The script
	# falls through to its own embedded loader, which goes to tmpfs. Nothing
	# reaches a disk, and nothing is left for anybody to find.
	before=$(find /dev/shm -maxdepth 1 -name '.ape-*' 2>/dev/null | wc -l)
	out=$(PATH="$work/nowrite" APE_LOADER='' \
		/bin/sh "$work/prog/prog.com" "$@" 2>&1)
	code=$?
	if [ "$code" -ne 0 ] || [ "$out" != fizzbuzz ]; then
		echo "the embedded loader must run from RAM: exit $code, output '$out'" >&2
		exit 1
	fi
	after=$(find /dev/shm -maxdepth 1 -name '.ape-*' 2>/dev/null | wc -l)
	if [ "$before" != "$after" ]; then
		echo "the run left a loader behind in /dev/shm: $before before, $after after" >&2
		exit 1
	fi
	echo "read-only program directory, loader from RAM: fizzbuzz, and nothing left"
	exit 0
fi

# refuse: nothing writable, no loader anywhere. PATH holds only the read-only
# directories, so the search finds nothing to exec.
out=$(PATH="$work/nowrite" APE_LOADERDIR="$work/nowrite" APE_LOADER='' \
	/bin/sh "$work/prog/prog.com" "$@" 2>&1)
code=$?
if [ "$code" -ne 121 ]; then
	echo "a read-only host with no loader must exit 121, got $code: '$out'" >&2
	exit 1
fi
case "$out" in
*"install it on PATH, or point APE_LOADER at it"*) ;;
*)
	echo "the refusal must name the fix, got '$out'" >&2
	exit 1
	;;
esac
echo "read-only boot with no loader refuses: exit 121, and names the fix"
