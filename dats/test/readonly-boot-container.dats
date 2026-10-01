# The shape `docker run --read-only` leaves, with the program on a writable
# bind mount. /dev/shm is writable and noexec there, because that is how docker
# mounts it, and --read-only takes /tmp away. So the program's own directory is
# the last candidate the boot script has, and this says it serves.
#
# Linux only. The shape is built from mount namespaces, which darwin has not
# got. readonly-boot.dats carries the claims both platforms share.
tests:
	- desc: a read-only container with a bind-mounted program runs it
	  cmd: dats/test/readonly-boot.sh container binaries/ape-binary-Linux/fizzbuzz.com
	  exit: 0
	  outputs:
		stdout:
			- "read-only container, noexec /dev/shm: fizzbuzz, and nothing left"
	# busybox answers [ -x ] from the mode bits for root, so a noexec /dev/shm
	# passes it. A docker build runs busybox as root, and that is where the
	# boot script's exec used to fail.
	- desc: busybox as root skips a noexec /dev/shm and runs the program
	  cmd: RO_BOOT_SH='busybox sh' dats/test/readonly-boot.sh container binaries/ape-binary-Linux/fizzbuzz.com
	  exit: 0
	  outputs:
		stdout:
			- "read-only container, noexec /dev/shm: fizzbuzz, and nothing left"
