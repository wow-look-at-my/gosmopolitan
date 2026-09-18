#!/bin/sh
# Builds the shape `docker run --read-only` leaves, inside a mount namespace,
# and runs the APE in it. readonly-boot.sh calls this through `unshare -rm`.
#
# Everything here is a FRESH tmpfs. A nested user namespace refuses a bind of a
# mount it inherited, so /opt stands in for the writable bind docker gives the
# program, and /tmp becomes a read-only tmpfs.
#
# Exit codes tell readonly-boot.sh which claim broke:
#   2 the shape could not be built   3 /tmp stayed writable
#   4 /dev/shm is not writable       5 the program did not run
#   6 the run left a loader behind
set -u

ape=${1:?usage: readonly-boot-container.sh <ape> [args...]}
shift

mount -t tmpfs -o rw none /opt || exit 2
mkdir -p /opt/prog /opt/nowrite || exit 2
cp "$ape" /opt/prog/prog.com || exit 2
chmod 755 /opt/prog/prog.com || exit 2

# /tmp goes away, as --read-only leaves it, and /dev/shm is writable and
# noexec, which is how docker mounts it.
mount -t tmpfs -o ro none /tmp || exit 2
mount -t tmpfs -o rw,nosuid,nodev,noexec none /dev/shm || exit 2

# Prove both claims before the run counts for anything.
if (: > /tmp/canary) 2>/dev/null; then
	exit 3
fi
if ! (: > /dev/shm/canary) 2>/dev/null; then
	exit 4
fi

out=$(PATH=/opt/nowrite APE_LOADER='' /bin/sh /opt/prog/prog.com "$@" 2>&1) || exit 5
[ -z "$(find /opt/prog -name '.ape-*')" ] || exit 6
printf '%s' "$out"
