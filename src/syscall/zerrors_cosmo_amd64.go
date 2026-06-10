// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Error constants for Cosmopolitan Libc on amd64.
// Cosmopolitan uses Linux error numbers.

//go:build cosmo && amd64

package syscall

const (
	AF_INET      = 0x2
	AF_INET6     = 0xa
	AF_UNIX      = 0x1
	AF_UNSPEC    = 0x0

	DT_BLK     = 0x6
	DT_CHR     = 0x2
	DT_DIR     = 0x4
	DT_FIFO    = 0x1
	DT_LNK     = 0xa
	DT_REG     = 0x8
	DT_SOCK    = 0xc
	DT_UNKNOWN = 0x0
	DT_WHT     = 0xe

	EPOLLERR      = 0x8
	EPOLLET       = -0x80000000
	EPOLLHUP      = 0x10
	EPOLLIN       = 0x1
	EPOLLMSG      = 0x400
	EPOLLONESHOT  = 0x40000000
	EPOLLOUT      = 0x4
	EPOLLPRI      = 0x2
	EPOLLRDBAND   = 0x80
	EPOLLRDHUP    = 0x2000
	EPOLLRDNORM   = 0x40
	EPOLLWRBAND   = 0x200
	EPOLLWRNORM   = 0x100
	EPOLL_CLOEXEC = 0x80000
	EPOLL_CTL_ADD = 0x1
	EPOLL_CTL_DEL = 0x2
	EPOLL_CTL_MOD = 0x3

	FD_CLOEXEC = 0x1
	FD_SETSIZE = 0x400

	F_DUPFD         = 0x0
	F_DUPFD_CLOEXEC = 0x406
	F_GETFD         = 0x1
	F_GETFL         = 0x3
	F_GETLK         = 0x5
	F_GETOWN        = 0x9
	F_OK            = 0x0
	F_RDLCK         = 0x0
	F_SETFD         = 0x2
	F_SETFL         = 0x4
	F_SETLK         = 0x6
	F_SETLKW        = 0x7
	F_SETOWN        = 0x8
	F_UNLCK         = 0x2
	F_WRLCK         = 0x1

	IPPROTO_IP   = 0x0
	IPPROTO_IPV6 = 0x29
	IPPROTO_ICMP = 0x1
	IPPROTO_TCP  = 0x6
	IPPROTO_UDP  = 0x11

	IPV6_V6ONLY = 0x1a

	IP_TOS = 0x1
	IP_TTL = 0x2

	O_ACCMODE   = 0x3
	O_APPEND    = 0x400
	O_ASYNC     = 0x2000
	O_CLOEXEC   = 0x80000
	O_CREAT     = 0x40
	O_DIRECTORY = 0x10000
	O_DSYNC     = 0x1000
	O_EXCL      = 0x80
	O_LARGEFILE = 0x0
	O_NDELAY    = 0x800
	O_NOCTTY    = 0x100
	O_NOFOLLOW  = 0x20000
	O_NONBLOCK  = 0x800
	O_RDONLY    = 0x0
	O_RDWR      = 0x2
	O_SYNC      = 0x101000
	O_TRUNC     = 0x200
	O_WRONLY    = 0x1

	RLIMIT_AS     = 0x9
	RLIMIT_CORE   = 0x4
	RLIMIT_CPU    = 0x0
	RLIMIT_DATA   = 0x2
	RLIMIT_FSIZE  = 0x1
	RLIMIT_NOFILE = 0x7
	RLIMIT_STACK  = 0x3
	RLIM_INFINITY = -0x1

	R_OK = 0x4

	SHUT_RD   = 0x0
	SHUT_RDWR = 0x2
	SHUT_WR   = 0x1

	SOCK_CLOEXEC   = 0x80000
	SOCK_DGRAM     = 0x2
	SOCK_NONBLOCK  = 0x800
	SOCK_RAW       = 0x3
	SOCK_SEQPACKET = 0x5
	SOCK_STREAM    = 0x1

	SOL_IP     = 0x0
	SOL_IPV6   = 0x29
	SOL_SOCKET = 0x1
	SOL_TCP    = 0x6

	SO_ACCEPTCONN = 0x1e
	SO_BROADCAST  = 0x6
	SO_DEBUG      = 0x1
	SO_DONTROUTE  = 0x5
	SO_ERROR      = 0x4
	SO_KEEPALIVE  = 0x9
	SO_LINGER     = 0xd
	SO_OOBINLINE  = 0xa
	SO_RCVBUF     = 0x8
	SO_RCVLOWAT   = 0x12
	SO_RCVTIMEO   = 0x14
	SO_REUSEADDR  = 0x2
	SO_REUSEPORT  = 0xf
	SO_SNDBUF     = 0x7
	SO_SNDLOWAT   = 0x13
	SO_SNDTIMEO   = 0x15
	SO_TYPE       = 0x3

	SOMAXCONN = 0x80

	SCM_RIGHTS = 0x1

	// Multicast constants
	IP_ADD_MEMBERSHIP  = 0x23
	IP_DROP_MEMBERSHIP = 0x24
	IP_MULTICAST_IF    = 0x20
	IP_MULTICAST_LOOP  = 0x22
	IP_MULTICAST_TTL   = 0x21

	IPV6_JOIN_GROUP     = 0x14
	IPV6_LEAVE_GROUP    = 0x15
	IPV6_MULTICAST_HOPS = 0x12
	IPV6_MULTICAST_IF   = 0x11
	IPV6_MULTICAST_LOOP = 0x13

	// TCP keepalive
	TCP_KEEPCNT   = 0x6
	TCP_KEEPIDLE  = 0x4
	TCP_KEEPINTVL = 0x5

	// Memory mapping flags
	PROT_EXEC  = 0x4
	PROT_NONE  = 0x0
	PROT_READ  = 0x1
	PROT_WRITE = 0x2

	MAP_ANON      = 0x20
	MAP_ANONYMOUS = 0x20
	MAP_FILE      = 0x0
	MAP_FIXED     = 0x10
	MAP_PRIVATE   = 0x2
	MAP_SHARED    = 0x1

	LOCK_EX = 0x2
	LOCK_NB = 0x4
	LOCK_SH = 0x1
	LOCK_UN = 0x8

	// Message flags
	MSG_CMSG_CLOEXEC = 0x40000000
	MSG_CTRUNC       = 0x8
	MSG_DONTROUTE    = 0x4
	MSG_DONTWAIT     = 0x40
	MSG_EOR          = 0x80
	MSG_NOSIGNAL     = 0x4000
	MSG_OOB          = 0x1
	MSG_PEEK         = 0x2
	MSG_TRUNC        = 0x20
	MSG_WAITALL      = 0x100

	S_IEXEC  = 0x40
	S_IFBLK  = 0x6000
	S_IFCHR  = 0x2000
	S_IFDIR  = 0x4000
	S_IFIFO  = 0x1000
	S_IFLNK  = 0xa000
	S_IFMT   = 0xf000
	S_IFREG  = 0x8000
	S_IFSOCK = 0xc000
	S_IREAD  = 0x100
	S_IRGRP  = 0x20
	S_IROTH  = 0x4
	S_IRUSR  = 0x100
	S_IRWXG  = 0x38
	S_IRWXO  = 0x7
	S_IRWXU  = 0x1c0
	S_ISGID  = 0x400
	S_ISUID  = 0x800
	S_ISVTX  = 0x200
	S_IWGRP  = 0x10
	S_IWOTH  = 0x2
	S_IWRITE = 0x80
	S_IWUSR  = 0x80
	S_IXGRP  = 0x8
	S_IXOTH  = 0x1
	S_IXUSR  = 0x40

	TCP_NODELAY = 0x1

	TIOCNOTTY = 0x5422
	TIOCSCTTY = 0x540e

	WNOHANG    = 0x1
	WUNTRACED  = 0x2
	WCONTINUED = 0x8
	WEXITED    = 0x4
	WNOWAIT    = 0x1000000

	W_OK = 0x2
	X_OK = 0x1
)

// Errors
const (
	E2BIG           = Errno(0x7)
	EACCES          = Errno(0xd)
	EADDRINUSE      = Errno(0x62)
	EADDRNOTAVAIL   = Errno(0x63)
	EADV            = Errno(0x44)
	EAFNOSUPPORT    = Errno(0x61)
	EAGAIN          = Errno(0xb)
	EALREADY        = Errno(0x72)
	EBADE           = Errno(0x34)
	EBADF           = Errno(0x9)
	EBADFD          = Errno(0x4d)
	EBADMSG         = Errno(0x4a)
	EBADR           = Errno(0x35)
	EBADRQC         = Errno(0x38)
	EBADSLT         = Errno(0x39)
	EBFONT          = Errno(0x3b)
	EBUSY           = Errno(0x10)
	ECANCELED       = Errno(0x7d)
	ECHILD          = Errno(0xa)
	ECHRNG          = Errno(0x2c)
	ECOMM           = Errno(0x46)
	ECONNABORTED    = Errno(0x67)
	ECONNREFUSED    = Errno(0x6f)
	ECONNRESET      = Errno(0x68)
	EDEADLK         = Errno(0x23)
	EDEADLOCK       = Errno(0x23)
	EDESTADDRREQ    = Errno(0x59)
	EDOM            = Errno(0x21)
	EDOTDOT         = Errno(0x49)
	EDQUOT          = Errno(0x7a)
	EEXIST          = Errno(0x11)
	EFAULT          = Errno(0xe)
	EFBIG           = Errno(0x1b)
	EHOSTDOWN       = Errno(0x70)
	EHOSTUNREACH    = Errno(0x71)
	EIDRM           = Errno(0x2b)
	EILSEQ          = Errno(0x54)
	EINPROGRESS     = Errno(0x73)
	EINTR           = Errno(0x4)
	EINVAL          = Errno(0x16)
	EIO             = Errno(0x5)
	EISCONN         = Errno(0x6a)
	EISDIR          = Errno(0x15)
	EISNAM          = Errno(0x78)
	EKEYEXPIRED     = Errno(0x7f)
	EKEYREJECTED    = Errno(0x81)
	EKEYREVOKED     = Errno(0x80)
	EL2HLT          = Errno(0x33)
	EL2NSYNC        = Errno(0x2d)
	EL3HLT          = Errno(0x2e)
	EL3RST          = Errno(0x2f)
	ELIBACC         = Errno(0x4f)
	ELIBBAD         = Errno(0x50)
	ELIBEXEC        = Errno(0x53)
	ELIBMAX         = Errno(0x52)
	ELIBSCN         = Errno(0x51)
	ELNRNG          = Errno(0x30)
	ELOOP           = Errno(0x28)
	EMEDIUMTYPE     = Errno(0x7c)
	EMFILE          = Errno(0x18)
	EMLINK          = Errno(0x1f)
	EMSGSIZE        = Errno(0x5a)
	EMULTIHOP       = Errno(0x48)
	ENAMETOOLONG    = Errno(0x24)
	ENAVAIL         = Errno(0x77)
	ENETDOWN        = Errno(0x64)
	ENETRESET       = Errno(0x66)
	ENETUNREACH     = Errno(0x65)
	ENFILE          = Errno(0x17)
	ENOANO          = Errno(0x37)
	ENOBUFS         = Errno(0x69)
	ENOCSI          = Errno(0x32)
	ENODATA         = Errno(0x3d)
	ENODEV          = Errno(0x13)
	ENOENT          = Errno(0x2)
	ENOEXEC         = Errno(0x8)
	ENOKEY          = Errno(0x7e)
	ENOLCK          = Errno(0x25)
	ENOLINK         = Errno(0x43)
	ENOMEDIUM       = Errno(0x7b)
	ENOMEM          = Errno(0xc)
	ENOMSG          = Errno(0x2a)
	ENONET          = Errno(0x40)
	ENOPKG          = Errno(0x41)
	ENOPROTOOPT     = Errno(0x5c)
	ENOSPC          = Errno(0x1c)
	ENOSR           = Errno(0x3f)
	ENOSTR          = Errno(0x3c)
	ENOSYS          = Errno(0x26)
	ENOTBLK         = Errno(0xf)
	ENOTCONN        = Errno(0x6b)
	ENOTDIR         = Errno(0x14)
	ENOTEMPTY       = Errno(0x27)
	ENOTNAM         = Errno(0x76)
	ENOTSOCK        = Errno(0x58)
	ENOTSUP         = Errno(0x5f)
	ENOTTY          = Errno(0x19)
	ENOTUNIQ        = Errno(0x4c)
	ENXIO           = Errno(0x6)
	EOPNOTSUPP      = Errno(0x5f)
	EOVERFLOW       = Errno(0x4b)
	EPERM           = Errno(0x1)
	EPFNOSUPPORT    = Errno(0x60)
	EPIPE           = Errno(0x20)
	EPROTO          = Errno(0x47)
	EPROTONOSUPPORT = Errno(0x5d)
	EPROTOTYPE      = Errno(0x5b)
	ERANGE          = Errno(0x22)
	EREMCHG         = Errno(0x4e)
	EREMOTE         = Errno(0x42)
	EREMOTEIO       = Errno(0x79)
	ERESTART        = Errno(0x55)
	EROFS           = Errno(0x1e)
	ESHUTDOWN       = Errno(0x6c)
	ESOCKTNOSUPPORT = Errno(0x5e)
	ESPIPE          = Errno(0x1d)
	ESRCH           = Errno(0x3)
	ESRMNT          = Errno(0x45)
	ESTALE          = Errno(0x74)
	ESTRPIPE        = Errno(0x56)
	ETIME           = Errno(0x3e)
	ETIMEDOUT       = Errno(0x6e)
	ETOOMANYREFS    = Errno(0x6d)
	ETXTBSY         = Errno(0x1a)
	EUCLEAN         = Errno(0x75)
	EUNATCH         = Errno(0x31)
	EUSERS          = Errno(0x57)
	EWOULDBLOCK     = Errno(0xb)
	EXDEV           = Errno(0x12)
	EXFULL          = Errno(0x36)
)

// Signals
const (
	SIGABRT   = Signal(0x6)
	SIGALRM   = Signal(0xe)
	SIGBUS    = Signal(0x7)
	SIGCHLD   = Signal(0x11)
	SIGCLD    = Signal(0x11)
	SIGCONT   = Signal(0x12)
	SIGFPE    = Signal(0x8)
	SIGHUP    = Signal(0x1)
	SIGILL    = Signal(0x4)
	SIGINT    = Signal(0x2)
	SIGIO     = Signal(0x1d)
	SIGIOT    = Signal(0x6)
	SIGKILL   = Signal(0x9)
	SIGPIPE   = Signal(0xd)
	SIGPOLL   = Signal(0x1d)
	SIGPROF   = Signal(0x1b)
	SIGPWR    = Signal(0x1e)
	SIGQUIT   = Signal(0x3)
	SIGSEGV   = Signal(0xb)
	SIGSTKFLT = Signal(0x10)
	SIGSTOP   = Signal(0x13)
	SIGSYS    = Signal(0x1f)
	SIGTERM   = Signal(0xf)
	SIGTRAP   = Signal(0x5)
	SIGTSTP   = Signal(0x14)
	SIGTTIN   = Signal(0x15)
	SIGTTOU   = Signal(0x16)
	SIGUNUSED = Signal(0x1f)
	SIGURG    = Signal(0x17)
	SIGUSR1   = Signal(0xa)
	SIGUSR2   = Signal(0xc)
	SIGVTALRM = Signal(0x1a)
	SIGWINCH  = Signal(0x1c)
	SIGXCPU   = Signal(0x18)
	SIGXFSZ   = Signal(0x19)
)

// Error table
var errors = [...]string{
	1:   "operation not permitted",
	2:   "no such file or directory",
	3:   "no such process",
	4:   "interrupted system call",
	5:   "input/output error",
	6:   "no such device or address",
	7:   "argument list too long",
	8:   "exec format error",
	9:   "bad file descriptor",
	10:  "no child processes",
	11:  "resource temporarily unavailable",
	12:  "cannot allocate memory",
	13:  "permission denied",
	14:  "bad address",
	15:  "block device required",
	16:  "device or resource busy",
	17:  "file exists",
	18:  "invalid cross-device link",
	19:  "no such device",
	20:  "not a directory",
	21:  "is a directory",
	22:  "invalid argument",
	23:  "too many open files in system",
	24:  "too many open files",
	25:  "inappropriate ioctl for device",
	26:  "text file busy",
	27:  "file too large",
	28:  "no space left on device",
	29:  "illegal seek",
	30:  "read-only file system",
	31:  "too many links",
	32:  "broken pipe",
	33:  "numerical argument out of domain",
	34:  "numerical result out of range",
	35:  "resource deadlock avoided",
	36:  "file name too long",
	37:  "no locks available",
	38:  "function not implemented",
	39:  "directory not empty",
	40:  "too many levels of symbolic links",
	42:  "no message of desired type",
	43:  "identifier removed",
	44:  "channel number out of range",
	45:  "level 2 not synchronized",
	46:  "level 3 halted",
	47:  "level 3 reset",
	48:  "link number out of range",
	49:  "protocol driver not attached",
	50:  "no CSI structure available",
	51:  "level 2 halted",
	52:  "invalid exchange",
	53:  "invalid request descriptor",
	54:  "exchange full",
	55:  "no anode",
	56:  "invalid request code",
	57:  "invalid slot",
	59:  "bad font file format",
	60:  "device not a stream",
	61:  "no data available",
	62:  "timer expired",
	63:  "out of streams resources",
	64:  "machine is not on the network",
	65:  "package not installed",
	66:  "object is remote",
	67:  "link has been severed",
	68:  "advertise error",
	69:  "srmount error",
	70:  "communication error on send",
	71:  "protocol error",
	72:  "multihop attempted",
	73:  "RFS specific error",
	74:  "bad message",
	75:  "value too large for defined data type",
	76:  "name not unique on network",
	77:  "file descriptor in bad state",
	78:  "remote address changed",
	79:  "can not access a needed shared library",
	80:  "accessing a corrupted shared library",
	81:  ".lib section in a.out corrupted",
	82:  "attempting to link in too many shared libraries",
	83:  "cannot exec a shared library directly",
	84:  "invalid or incomplete multibyte or wide character",
	85:  "interrupted system call should be restarted",
	86:  "streams pipe error",
	87:  "too many users",
	88:  "socket operation on non-socket",
	89:  "destination address required",
	90:  "message too long",
	91:  "protocol wrong type for socket",
	92:  "protocol not available",
	93:  "protocol not supported",
	94:  "socket type not supported",
	95:  "operation not supported",
	96:  "protocol family not supported",
	97:  "address family not supported by protocol",
	98:  "address already in use",
	99:  "cannot assign requested address",
	100: "network is down",
	101: "network is unreachable",
	102: "network dropped connection on reset",
	103: "software caused connection abort",
	104: "connection reset by peer",
	105: "no buffer space available",
	106: "transport endpoint is already connected",
	107: "transport endpoint is not connected",
	108: "cannot send after transport endpoint shutdown",
	109: "too many references: cannot splice",
	110: "connection timed out",
	111: "connection refused",
	112: "host is down",
	113: "no route to host",
	114: "operation already in progress",
	115: "operation now in progress",
	116: "stale file handle",
	117: "structure needs cleaning",
	118: "not a XENIX named type file",
	119: "no XENIX semaphores available",
	120: "is a named type file",
	121: "remote I/O error",
	122: "disk quota exceeded",
	123: "no medium found",
	124: "wrong medium type",
	125: "operation canceled",
	126: "required key not available",
	127: "key has expired",
	128: "key has been revoked",
	129: "key was rejected by service",
	130: "owner died",
	131: "state not recoverable",
}

// Signal table
var signals = [...]string{
	1:  "hangup",
	2:  "interrupt",
	3:  "quit",
	4:  "illegal instruction",
	5:  "trace/breakpoint trap",
	6:  "aborted",
	7:  "bus error",
	8:  "floating point exception",
	9:  "killed",
	10: "user defined signal 1",
	11: "segmentation fault",
	12: "user defined signal 2",
	13: "broken pipe",
	14: "alarm clock",
	15: "terminated",
	16: "stack fault",
	17: "child exited",
	18: "continued",
	19: "stopped (signal)",
	20: "stopped",
	21: "stopped (tty input)",
	22: "stopped (tty output)",
	23: "urgent I/O condition",
	24: "CPU time limit exceeded",
	25: "file size limit exceeded",
	26: "virtual timer expired",
	27: "profiling timer expired",
	28: "window changed",
	29: "I/O possible",
	30: "power failure",
	31: "bad system call",
}
