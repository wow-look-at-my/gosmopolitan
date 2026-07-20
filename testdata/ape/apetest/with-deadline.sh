#!/bin/sh
# with-deadline.sh SECONDS CMD [ARGS...]
#
# Runs CMD in its own process group; after SECONDS it SIGKILLs the whole
# group and exits 124 WITHOUT waiting for the corpse. CI uses this
# around `go test` because wedged steps on the macOS runners have been
# observed to survive both the runner's timeout-minutes cancellation
# and a process-group SIGKILL - i.e. a process stuck in an
# uninterruptible kernel state. Such a process cannot be waited for;
# the only way the step can end is to abandon it (launchd inherits the
# corpse). Callers must redirect CMD's output to a file rather than the
# step's stdout, so an abandoned descendant cannot keep the runner's
# log pipe open either.
#
# SIGKILL (not TERM/ALRM) because the Go runtime swallows most signals
# by default and a wedged cosmo binary has no signal handling at all;
# KILL is enforced by the kernel.
#
# On Windows (git-bash) it just execs the command: process-group kill
# semantics do not apply there and the wedge has only been seen on
# macOS.
set -u
secs="$1"
shift
case "$(uname -s 2>/dev/null)" in
MINGW* | MSYS* | CYGWIN* | Windows*)
	exec "$@"
	;;
esac
exec perl -e '
	my $secs = shift @ARGV;
	my $p = fork() // die "fork: $!";
	if (!$p) { setpgrp(0, 0); exec @ARGV or die "exec: $!" }
	local $SIG{ALRM} = sub {
		print STDERR "with-deadline: deadline of ${secs}s exceeded; killing process group and abandoning it\n";
		kill "KILL", -$p;
		sleep 2;
		exit 124;
	};
	alarm $secs;
	waitpid $p, 0;
	my $st = $?;
	exit(($st >> 8) | (($st & 0x7f) ? 1 : 0));
' "$secs" "$@"
