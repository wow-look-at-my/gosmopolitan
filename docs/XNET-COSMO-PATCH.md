# The x/net fork cosmo needs

`GOOS=cosmo` matches the `linux` build tag (`src/go/build/build.go`, `matchTag`), the way `android` does. That is what lets most of std compile for cosmo unchanged. An upstream file guarded `//go:build linux` therefore compiles for cosmo too. `golang.org/x/net/quic` holds files of that shape which reach socket constants the cosmo `syscall` package leaves undefined: `IP_RECVTOS`, `IPV6_RECVPKTINFO`, `IPV6_PKTINFO`, `IPV6_RECVTCLASS`, `IP_PKTINFO` and `IPV6_TCLASS`.

quic is not optional here. `net/http` imports it through `x/net/http3`.

The fork answers by sending cosmo to `udp_other.go`, the portable path that reaches none of those constants. These build tags carry that:

| file | upstream | cosmo |
|---|---|---|
| `quic/udp_linux.go` | `//go:build linux` | `//go:build linux && !cosmo` |
| `quic/udp_msg.go` | `//go:build !quicbasicnet && (darwin \|\| linux)` | `//go:build !quicbasicnet && (darwin \|\| (linux && !cosmo))` |
| `quic/udp_other.go` | `//go:build quicbasicnet \|\| !(darwin \|\| linux)` | `//go:build quicbasicnet \|\| cosmo \|\| !(darwin \|\| linux)` |

## Why the patch needs a fork of the repository

A submodule carries upstream's commit. It cannot carry a local edit. The org already answers this for `x/tools` with `wow-look-at-my/gosmopolitan_tools`, which `docs/GOPLS.md` describes. `x/net` needs the same treatment: `wow-look-at-my/gosmopolitan_net`, carrying these tags, with `src/vendor/golang.org/x/net` pointed at it.

## The alternative, and why it is not taken

The cosmo `syscall` package can define the missing constants instead. Upstream's Linux path then compiles. Cosmopolitan Libc does translate a Linux socket option to its host equivalent. So the idea is not absurd. It is untested here. There is no macOS or Windows runner for a socket option this fork has never exercised on either host. A `setsockopt` that quietly fails costs quic its ECN and packet-info handling, and reports nothing. Take this route only with a measurement from each host.
