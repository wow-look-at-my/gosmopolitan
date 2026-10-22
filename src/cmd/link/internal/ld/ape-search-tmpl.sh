  l={{.Name}}
  for c in "${APE_LOADER:-}" /usr/local/lib/ape/$l /usr/lib/ape/$l; do
    [ -x "$c" ] && { apereg "$c"; apepath; exec "$c" "$o" "$@"; }
  done
  for n in $l apeld ape; do
    c=$(command -v "$n" 2>/dev/null) || continue
    [ -n "$c" ] && { apereg "$c"; apepath; exec "$c" "$o" "$@"; }
  done
