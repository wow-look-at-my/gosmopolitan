  l={{.Name}}
{{- if .Named}}
  apenamed() {
    s=$1; shift
    for d in {{.Dirs}}; do
      [ -d "$d" ] && [ -w "$d" ] || continue
      k=$d/.ape-$l-{{.Tag}}.$$; u=$k/${o##*/}
      mkdir -m 700 "$k" 2>/dev/null || continue
      { cp -c "$s" "$u" 2>/dev/null || cp "$s" "$u" 2>/dev/null; } && "$u" >/dev/null 2>&1
      [ $? -eq 127 ] && { apepath; exec "$u" -u "$o" "$@"; }
      rm -f "$u"; rmdir "$k"
    done
  }
{{- end}}
  for c in "${APE_LOADER:-}" /usr/local/lib/ape/$l /usr/lib/ape/$l; do
    [ -x "$c" ] && { apereg "$c"; {{if .Named}}apenamed "$c" "$@"; {{end}}apepath; exec "$c" "$o" "$@"; }
  done
  for n in $l apeld ape; do
    c=$(command -v "$n" 2>/dev/null) || continue
    [ -n "$c" ] && { apereg "$c"; {{if .Named}}[ "$n" = ape ] || apenamed "$c" "$@"; {{end}}apepath; exec "$c" "$o" "$@"; }
  done
