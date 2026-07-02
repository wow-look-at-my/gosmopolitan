#!/bin/sh
# with-deadline.sh SECONDS CMD [ARGS...]
#
# Runs CMD in its own process group and SIGKILLs the entire group after
# SECONDS. CI uses this around `go test` because runner-side step
# timeouts (timeout-minutes) were observed not to fire when a step
# wedged on the macOS runners - jobs sat 30+ minutes past the timeout
# and uploaded no logs. An in-step killer guarantees the step always
# terminates on its own, with the log of everything up to the wedge
# preserved, and the process-GROUP kill also reaps any orphaned APE
# grandchild that would otherwise keep the step's log pipes open.
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
		print STDERR "with-deadline: deadline of ${secs}s exceeded; killing process group\n";
		kill "KILL", -$p;
	};
	alarm $secs;
	waitpid $p, 0;
	my $st = $?;
	exit(($st >> 8) | (($st & 0x7f) ? 1 : 0));
' "$secs" "$@"
