# A continue-on-error hides a red leg, so each one carries an expiry date
# and stops being accepted once that date passes.
tests:
	- desc: every continue-on-error in a workflow is dated and current
	  cmd: dats/checks/waiver-expiry.sh .github/workflows/*.yml
	  exit: 0

	- desc: the guard refuses a waiver whose date has passed
	  cmd: |
		printf '  # waiver-expires: 2000-01-01\n  continue-on-error: true\n' > "$TMPDIR/w.yml"
		rc=0; dats/checks/waiver-expiry.sh "$TMPDIR/w.yml" || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard refuses a waiver with no date at all
	  cmd: |
		printf '  continue-on-error: true\n' > "$TMPDIR/u.yml"
		rc=0; dats/checks/waiver-expiry.sh "$TMPDIR/u.yml" || rc=$?
		test "$rc" -eq 2
	  exit: 0

	- desc: the guard accepts a waiver dated in the future
	  cmd: |
		printf '  continue-on-error: true # waiver-expires: 2999-01-01\n' > "$TMPDIR/f.yml"
		dats/checks/waiver-expiry.sh "$TMPDIR/f.yml"
	  exit: 0
