  for d in {{.Dirs}}; do
    [ -d "$d" ] && [ -w "$d" ] || continue
    u=$d/.ape-$l-{{.Tag}}.$$
    dd if="$o" bs={{.Block}} skip={{.Skip}} count={{.Count}} 2>/dev/null {{if .Gzip}}| gzip -dc {{end}}>"$u" 2>/dev/null || { rm -f "$u"; continue; }
    { [ -s "$u" ] && chmod 700 "$u" && [ -x "$u" ]; } || { rm -f "$u"; continue; }
    "$u" >/dev/null 2>&1; [ $? -eq 127 ] || { rm -f "$u"; continue; }
    if [ -z "${APE_NOBINFMT:-}" ] && apereg "$u"; then
      rm -f "$u"; APE_NOBINFMT=1; export APE_NOBINFMT
      apepath; exec "$o" "$@"
    fi
    apepath; exec "$u" -u "$o" "$@"
  done
  echo "APE: no $l on this host, and nowhere to put the embedded one: install it on PATH, or point APE_LOADER at it" >&2
  exit 121
