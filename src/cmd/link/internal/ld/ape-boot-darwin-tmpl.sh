  l={{.Name}}
  [ -n "${APE_LOADER:-}" ] && [ -x "$APE_LOADER" ] && { apepath; exec "$APE_LOADER" "$o" "$@"; }
  n=${o##*/}; a=${UID:-$(id -u)}
  for d in {{.Dirs}}; do
    k=$d/.ape-$l-{{.Tag}}-$a
    [ -x "$k/$n" ] && [ -O "$k" ] && [ ! -L "$k" ] && { apepath; exec "$k/$n" "$o" "$@"; }
  done
  for d in {{.Dirs}}; do
    [ -d "$d" ] && [ -w "$d" ] || continue
    k=$d/.ape-$l-{{.Tag}}-$a
    { [ -d "$k" ] || mkdir -m 700 "$k" 2>/dev/null; } && [ -O "$k" ] && [ ! -L "$k" ] && [ -w "$k" ] || continue
    u=$k/.$n.$$
    { dd if="$o" bs={{.Block}} skip={{.Skip}} count={{.Count}} 2>/dev/null {{if .Gzip}}| gzip -dc {{end}}>"$u"; } 2>/dev/null && [ -s "$u" ] && chmod 700 "$u" && "$u" >/dev/null 2>&1
    [ $? -eq 127 ] && mv -f "$u" "$k/$n" && { apepath; exec "$k/$n" "$o" "$@"; }
    rm -f "$u"
  done
  for c in /usr/local/lib/ape/$l /usr/lib/ape/$l; do
    [ -x "$c" ] && { apepath; exec "$c" "$o" "$@"; }
  done
  for m in $l apeld ape; do
    c=$(command -v "$m" 2>/dev/null) || continue
    [ -n "$c" ] && { apepath; exec "$c" "$o" "$@"; }
  done
  echo "APE: no $l on this host, and nowhere to put the embedded one: install it on PATH, or point APE_LOADER at it" >&2
  exit 121
