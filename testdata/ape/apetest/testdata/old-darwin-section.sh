  l=apeld-darwin-arm64
  o='@APE@'
  for c in "${APE_LOADER:-}" /usr/local/lib/ape/$l /usr/lib/ape/$l; do
    [ -x "$c" ] && { apereg "$c"; apepath; exec "$c" "$o" "$@"; }
  done
  for n in $l apeld ape; do
    c=$(command -v "$n" 2>/dev/null) || continue
    [ -n "$c" ] && { apereg "$c"; apepath; exec "$c" "$o" "$@"; }
  done
  for d in ${APE_LOADERDIR:-/dev/shm /tmp "${o%/*}"}; do
    [ -d "$d" ] && [ -w "$d" ] || continue
    u=$d/.ape-$l-00000000.$$
    dd if="@GZ@" bs=512 2>/dev/null | gzip -dc >"$u" 2>/dev/null || { rm -f "$u"; continue; }
    { [ -s "$u" ] && chmod 700 "$u" && [ -x "$u" ]; } || { rm -f "$u"; continue; }
    "$u" >/dev/null 2>&1; [ $? -eq 127 ] || { rm -f "$u"; continue; }
    if [ -z "${APE_NOBINFMT:-}" ] && apereg "$u"; then
      rm -f "$u"; APE_NOBINFMT=1; export APE_NOBINFMT
      apepath; exec "$o" "$@"
    fi
    apepath; exec "$u" "$o" "$@"
  done
  echo "APE: no $l on this host" >&2
  exit 121
