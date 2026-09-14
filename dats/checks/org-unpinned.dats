# A pin freezes this repository against one commit of another, so an org
# dependency carries no version and every org submodule tracks a branch.
# The guard reads the committed shape, before a build stamps a version in.
tests:
	- desc: no org dependency in the tree carries a frozen version
	  cmd: dats/checks/org-unpinned.sh .gitmodules src/cmd/go.mod src/cmd/go.sum src/cmd/vendor/modules.txt .github/workflows/*.yml .github/actions/*/action.yml
	  exit: 0

	- desc: the guard refuses an org submodule that tracks no branch
	  cmd: |
		printf '[submodule "dep"]\n\tpath = dep\n\turl = https://github.com/wow-look-at-my/dep.git\n' > "$TMPDIR/frozen.gitmodules"
		rc=0; dats/checks/org-unpinned.sh "$TMPDIR/frozen.gitmodules" || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard accepts an org submodule that declares branch = .
	  cmd: |
		printf '[submodule "dep"]\n\tpath = dep\n\turl = https://github.com/wow-look-at-my/dep.git\n\tbranch = .\n' > "$TMPDIR/tracked.gitmodules"
		dats/checks/org-unpinned.sh "$TMPDIR/tracked.gitmodules"
	  exit: 0

	- desc: a third-party submodule needs no branch
	  cmd: |
		printf '[submodule "lz4"]\n\tpath = lz4\n\turl = https://github.com/pierrec/lz4.git\n' > "$TMPDIR/third.gitmodules"
		dats/checks/org-unpinned.sh "$TMPDIR/third.gitmodules"
	  exit: 0

	- desc: the guard refuses a dated pseudo-version on an org module
	  cmd: |
		printf 'github.com/wow-look-at-my/dep v0.0.0-20260913211206-5bf638fdce71\n' > "$TMPDIR/pinned.mod"
		rc=0; dats/checks/org-unpinned.sh "$TMPDIR/pinned.mod" || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard refuses a release tag on an org module
	  cmd: |
		printf 'github.com/wow-look-at-my/dep v1.4.0\n' > "$TMPDIR/tagged.mod"
		rc=0; dats/checks/org-unpinned.sh "$TMPDIR/tagged.mod" || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard accepts the zero pseudo-version
	  cmd: |
		printf 'github.com/wow-look-at-my/dep v0.0.0-00010101000000-000000000000 // indirect\n' > "$TMPDIR/zero.mod"
		dats/checks/org-unpinned.sh "$TMPDIR/zero.mod"
	  exit: 0

	- desc: a third-party module keeps its version
	  cmd: |
		printf 'github.com/pierrec/lz4/v4 v4.1.27 // indirect\n' > "$TMPDIR/lz4.mod"
		dats/checks/org-unpinned.sh "$TMPDIR/lz4.mod"
	  exit: 0

	- desc: the guard refuses an org action pinned to a tag
	  cmd: |
		printf '      - uses: wow-look-at-my/dats@v1\n' > "$TMPDIR/tag.yml"
		rc=0; dats/checks/org-unpinned.sh "$TMPDIR/tag.yml" || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard refuses an org action pinned to a commit
	  cmd: |
		printf '      - uses: wow-look-at-my/dats@0123456789abcdef0123456789abcdef01234567\n' > "$TMPDIR/sha.yml"
		rc=0; dats/checks/org-unpinned.sh "$TMPDIR/sha.yml" || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard accepts an org action on a branch
	  cmd: |
		printf '      - uses: wow-look-at-my/dats@master\n      - uses: wow-look-at-my/actions@secret-server#latest\n' > "$TMPDIR/branch.yml"
		dats/checks/org-unpinned.sh "$TMPDIR/branch.yml"
	  exit: 0
