# The embedded standard library: go tool embedstd writes the blob, a cosmo
# go command links it in, and with no GOROOT that command builds a program
# byte for byte as the source tree does. The toolchain must already be
# built.
tests:
	- desc: a go command carrying its standard library builds without a GOROOT what the source tree builds
	  cmd: dats/checks/embedded-std.sh
	  timeout: 25m
	  exit: 0
