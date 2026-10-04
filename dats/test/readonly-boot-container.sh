#!/bin/sh
# Builds the shape `docker run --read-only` leaves, inside a mount namespace, and runs the APE in it.
set -u

ape=${1:?usage: readonly-boot-container.sh <ape> [args...]}
shift

mount -t tmpfs -o rw none /opt || exit 2
mkdir -p /opt/prog /opt/nowrite || exit 2
cp "$ape" /opt/prog/prog.com || exit 2
chmod 755 /opt/prog/prog.com || exit 2

# /tmp goes away, as --read-only leaves it, and /dev/shm is writable and noexec, which is how docker mounts it.
mount -t tmpfs -o ro none /tmp || exit 2
mount -t tmpfs -o rw,nosuid,nodev,noexec none /dev/shm || exit 2

# Prove both claims before the run counts for anything.
if (: > /tmp/canary) 2>/dev/null; then
	exit 3
fi
if ! (: > /dev/shm/canary) 2>/dev/null; then
	exit 4
fi

# RO_BOOT_SH picks the shell, as a command and its arguments.
shell_cmd=${RO_BOOT_SH:-/bin/sh}
shell_bin=$(command -v "${shell_cmd%% *}") || exit 2
shell_args=${shell_cmd#"${shell_cmd%% *}"}
out=$(PATH=/opt/nowrite APE_LOADER='' "$shell_bin" $shell_args /opt/prog/prog.com "$@" 2>&1) || exit 5
[ -z "$(find /opt/prog -name '.ape-*')" ] || exit 6
printf '%s' "$out"
