  for d in {{.Dirs}}; do
    [ -d "$d" ] && [ -w "$d" ] || continue
{{- if .Named}}
    k=$d/.ape-$l-{{.Tag}}.$$; u=$k/${o##*/}
    mkdir -m 700 "$k" 2>/dev/null || continue
{{- else}}
    u=$d/.ape-$l-{{.Tag}}.$$
{{- end}}
    dd if="$o" bs={{.Block}} skip={{.Skip}} count={{.Count}} 2>/dev/null {{if .Gzip}}| gzip -dc {{end}}>"$u" 2>/dev/null || { rm -f "$u"{{if .Named}}; rmdir "$k"{{end}}; continue; }
    { [ -s "$u" ] && chmod 700 "$u" && [ -x "$u" ]; } || { rm -f "$u"{{if .Named}}; rmdir "$k"{{end}}; continue; }
    "$u" >/dev/null 2>&1; [ $? -eq 127 ] || { rm -f "$u"{{if .Named}}; rmdir "$k"{{end}}; continue; }
    if [ -z "${APE_NOBINFMT:-}" ] && apereg "$u"; then
      rm -f "$u"{{if .Named}}; rmdir "$k"{{end}}; APE_NOBINFMT=1; export APE_NOBINFMT
      apepath; exec "$o" "$@"
    fi
    apepath; exec "$u" -u "$o" "$@"
  done
  echo "APE: no $l on this host, and nowhere to put the embedded one: install it on PATH, or point APE_LOADER at it" >&2
  exit 121
