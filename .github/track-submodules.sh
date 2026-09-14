#!/bin/sh
# Move every org submodule onto the head of the branch it follows, so a build
# uses current code rather than whatever commit the gitlink froze.
#
# The branch is the branch this run is on when the submodule has one, else that
# submodule's default branch. src/submodulebranch.bash does that, and make.bash
# calls it during a build; this wrapper exists because actions/checkout leaves a
# detached HEAD, so the branch has to come from the ref the run is on rather
# than from git.
#
# A stale gitlink is not a version. It is a fix that never shipped: the
# cacheclient index lifetime sat unreached behind one for a whole day.
set -eu

exec bash ./src/submodulebranch.bash "$@"
