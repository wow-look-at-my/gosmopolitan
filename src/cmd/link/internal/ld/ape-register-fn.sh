apereg() { [ -e /proc/sys/fs/binfmt_misc/APE ] && return 1
  [ -w /proc/sys/fs/binfmt_misc/register ] || return 1
  { printf ":APE:M::MZqFpD=\047::$1:F" > /proc/sys/fs/binfmt_misc/register; } 2>/dev/null
  [ -e /proc/sys/fs/binfmt_misc/APE ]; }
