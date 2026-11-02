  l={{.Name}}
  [ -n "${APE_LOADER:-}" ] && [ -x "$APE_LOADER" ] && { apepath; exec "$APE_LOADER" "$o" "$@"; }
  n=${o##*/}; a=${UID:-$(id -u)}
  for d in {{.Dirs}}; do
    k=$d/.ape-$l-{{.Hash}}-$a
    [ -k "$d" ] && [ -O "$k" ] && [ ! -L "$k" ] && [ -O "$k/$n" ] && [ ! -L "$k/$n" ] && [ -x "$k/$n" ] && { apepath; exec "$k/$n" "$o" "$@"; }
  done
  for d in {{.Dirs}}; do
    [ -d "$d" ] && [ -k "$d" ] && [ -w "$d" ] || continue
    k=$d/.ape-$l-{{.Hash}}-$a; w=; u=$d/$n
    set -C; true 2>/dev/null >"$u"; c=$?; set +C
    if [ $c -ne 0 ] || [ -L "$u" ] || [ ! -O "$u" ]; then
      w=$k/.$$; u=$w/$n
      mkdir -m 700 "$k" "$w" 2>/dev/null
      [ -O "$k" ] && [ ! -L "$k" ] && [ -O "$w" ] && [ ! -L "$w" ] || continue
    fi
    { dd if="$o" bs={{.Block}} skip={{.Skip}} count={{.Count}} 2>/dev/null {{if .Gzip}}| gzip -dc {{end}}>"$u"; } 2>/dev/null && [ -s "$u" ] && chmod 700 "$u" && [ -x "$u" ] && { apepath; exec "$u" -i "$k" "$o" "$@"; }
    rm -f "$u"; [ -z "$w" ] || rmdir "$w" 2>/dev/null
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
