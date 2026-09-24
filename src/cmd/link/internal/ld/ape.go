// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"cmd/internal/cosmoape"
	"cmd/internal/objabi"
	"cmd/internal/sys"
	"encoding/binary"
	"fmt"
	"internal/ape"
	"os"
	"strings"
	"text/template"
)

// APE (Actually Portable Executable), per ape/specification.md. One
// polyglot boots on several hosts:
// - Linux: an embedded ELF header, written by printf in octal.
// - macOS x86-64: dd copies the Mach-O header backward.
// - macOS ARM64: the embedded loader source, compiled by cc on first run.
// - Windows: a real PE header maps the amd64 image and enters the NT boot
//   stub (rt0_cosmo_nt_amd64.s). An arm64-only APE keeps a do-nothing
//   stub header, which stays parseable as a PE.
// - Windows shell (MSYS/Cygwin): cmd.exe runs the PE.

const (
	// APE header must be page-aligned for ELF loading
	// Using 64KB for Windows allocation granularity compatibility.
	// internal/ape states the same size to every reader of the output.
	apeHeaderSize   = ape.HeaderSize
	apeScriptOffset = 0x800

	// ELF constants
	elfMagic        = "\x7fELF"
	elfClass64      = 2
	elfDataLSB      = 1
	elfOSABIFreeBSD = 9 // Use FreeBSD ABI per spec
	elfTypeExec     = 2
	elfMachineAMD64 = 0x3E
	elfMachineARM64 = 0xB7
)

// convertToAPE converts an ELF binary to Actually Portable Executable format.
// apePayload describes one architecture's ELF image embedded in an APE file.
type apePayload struct {
	elf    []byte // complete ELF image; p_offset values are payload-relative
	arch   sys.ArchFamily
	offset uint64 // file offset of this image inside the APE; set by layoutAPE

	// pe carries the symbol RVAs the real amd64 PE header needs. It is
	// set only on the thin-link path (convertToAPE), where the loader is
	// alive to resolve them; nil means no real PE header can be computed
	// for this payload.
	pe *apePEInfo

	// head is the 64K APE head of the input file this payload was
	// extracted from, set only on the -apefat merge path. The amd64
	// input's head already contains the real PE header computed by its
	// thin link, valid verbatim in the fat file (the amd64 image lands
	// at the same file offset with identical bytes), so the fat header
	// transplants it instead of recomputing.
	head []byte
}

// apePEInfo holds the image RVAs, resolved from the live link's symbol
// table, that writePECosmoAMD64 places in the PE header. RVAs are
// relative to peCosmoImageBase, and for this layout equal payload-
// relative file offsets (every PT_LOAD has vaddr - p_offset ==
// peCosmoImageBase; see apePayloadLoads).
type apePEInfo struct {
	entryRVA   uint32 // _rt0_cosmo_nt, the PE AddressOfEntryPoint
	importsRVA uint32 // runtime.ntidata, the import directory table
}

// payloadFromELF validates elf and wraps it as an APE payload.
func payloadFromELF(elf []byte) (*apePayload, error) {
	if len(elf) < 64 || string(elf[0:4]) != elfMagic {
		return nil, fmt.Errorf("not a valid ELF binary")
	}
	var arch sys.ArchFamily
	switch m := binary.LittleEndian.Uint16(elf[18:20]); m {
	case elfMachineAMD64:
		arch = sys.AMD64
	case elfMachineARM64:
		arch = sys.ARM64
	default:
		return nil, fmt.Errorf("unsupported ELF machine type %#x", m)
	}
	// Validate the program header table up front: shiftPOffsets,
	// makeEmbeddedElfHeader, makeMachoHeader, payloadExtent,
	// stripPayload, and apePayloadLoads all index it without further
	// checks, so a truncated or corrupt input would panic there.
	phoff := binary.LittleEndian.Uint64(elf[32:40])
	phentsize := binary.LittleEndian.Uint16(elf[54:56])
	phnum := binary.LittleEndian.Uint16(elf[56:58])
	if phentsize != 56 {
		return nil, fmt.Errorf("corrupt ELF: e_phentsize is %d, want 56", phentsize)
	}
	if phnum == 0 {
		return nil, fmt.Errorf("corrupt ELF: no program headers")
	}
	if phoff > uint64(len(elf)) || uint64(phnum)*56 > uint64(len(elf))-phoff {
		return nil, fmt.Errorf("corrupt ELF: program header table (e_phoff %#x, e_phnum %d) extends past end of file (%d bytes)", phoff, phnum, len(elf))
	}
	return &apePayload{elf: elf, arch: arch}, nil
}

func (p *apePayload) entry() uint64 { return binary.LittleEndian.Uint64(p.elf[24:32]) }

func (ctxt *Link) convertToAPE() {
	if ctxt.HeadType != objabi.Hcosmo {
		return
	}

	outfile := *flagOutfile
	if outfile == "" {
		return
	}

	// Read the ELF file we just created
	elfData, err := os.ReadFile(outfile)
	if err != nil {
		Exitf("cannot read output file for APE conversion: %v", err)
	}
	p, err := payloadFromELF(elfData)
	if err != nil {
		Exitf("APE conversion: %v", err)
	}
	if p.arch != ctxt.Arch.Family {
		Exitf("APE conversion: ELF machine type does not match link architecture")
	}
	if p.arch == sys.AMD64 {
		apePrepareNTBoot(ctxt, p)
	}
	writeAPEFile(outfile, []*apePayload{p})
}

// apePayloadAlign is the alignment of payload images within the APE file.
// The APE loader requires p_vaddr to be congruent to p_offset modulo 16384
// for every program header; placing payloads on 64K boundaries (the largest
// page size in play) preserves whatever congruence each image already has.
const apePayloadAlign = 0x10000

// layoutAPE assigns file offsets to the payloads: the first begins right
// after the APE header, each subsequent payload at the next aligned boundary.
func layoutAPE(payloads []*apePayload) {
	off := uint64(apeHeaderSize)
	for _, p := range payloads {
		p.offset = off
		off += uint64(len(p.elf))
		off = (off + apePayloadAlign - 1) &^ uint64(apePayloadAlign-1)
	}
}

// writeAPEFile writes an APE polyglot containing the given payloads.
// Payload p_offset values are rewritten to absolute file offsets.
func writeAPEFile(outfile string, payloads []*apePayload) {
	layoutAPE(payloads)
	header := makeAPEHeaderForPayloads(payloads)

	apeFile, err := os.Create(outfile)
	if err != nil {
		Exitf("cannot create APE output: %v", err)
	}
	defer apeFile.Close()

	if _, err := apeFile.Write(header); err != nil {
		Exitf("cannot write APE header: %v", err)
	}
	cur := uint64(apeHeaderSize)
	for _, p := range payloads {
		if p.offset > cur {
			if _, err := apeFile.Write(make([]byte, p.offset-cur)); err != nil {
				Exitf("cannot write APE padding: %v", err)
			}
			cur = p.offset
		}
		if _, err := apeFile.Write(shiftPOffsets(p.elf, p.offset)); err != nil {
			Exitf("cannot write APE payload: %v", err)
		}
		cur += uint64(len(p.elf))
	}
	if end := apePEFileEnd(payloads); end > cur {
		if _, err := apeFile.Write(make([]byte, end-cur)); err != nil {
			Exitf("cannot write APE PE padding: %v", err)
		}
	}

	if err := os.Chmod(outfile, 0755); err != nil {
		Exitf("cannot chmod APE output: %v", err)
	}
}

// apePEFileEnd returns the file offset the amd64 payload's PE sections
// reach, or 0 when there is no amd64 payload. .data's SizeOfRawData is
// p_filesz rounded up to FileAlignment (writePECosmoAMD64), so the PE image
// extends past the payload's loadable span by up to FileAlignment-1 bytes
// of zero padding.
//
// The file must cover that tail. A STRIPPED amd64 payload with nothing
// after it ends exactly at its loadable span, and a PE header that then
// references bytes past EOF makes the NT loader reject the whole image
// ("%1 is not a valid Win32 application").
func apePEFileEnd(payloads []*apePayload) uint64 {
	for _, p := range payloads {
		if p.arch != sys.AMD64 {
			continue
		}
		loads := apePayloadLoads(p.elf)
		if len(loads) == 0 {
			return 0
		}
		data := loads[len(loads)-1]
		rounded := (data.filesz + peCosmoFileAlign - 1) &^ uint64(peCosmoFileAlign-1)
		return p.offset + data.off + rounded
	}
	return 0
}

// shiftPOffsets returns a copy of elf whose program header p_offset values
// are increased by delta, making them absolute within the APE file.
func shiftPOffsets(elf []byte, delta uint64) []byte {
	out := make([]byte, len(elf))
	copy(out, elf)
	phoff := binary.LittleEndian.Uint64(out[32:40])
	phentsize := binary.LittleEndian.Uint16(out[54:56])
	phnum := binary.LittleEndian.Uint16(out[56:58])
	for i := uint16(0); i < phnum; i++ {
		ph := phoff + uint64(i)*uint64(phentsize)
		pOffset := binary.LittleEndian.Uint64(out[ph+8:])
		binary.LittleEndian.PutUint64(out[ph+8:], pOffset+delta)
	}
	return out
}

// writePrintfBlob escapes blob into script as the body of a shell
// printf '...' statement: printable ASCII stays literal, everything else
// becomes an octal escape. Single quotes must be octal too -- not the shell
// backslash-quote idiom -- because the APE loader's printf decoder stops at the first
// raw quote byte when it scans the header for embedded boot ELF headers.
// Percent signs must be octal as well: printf would treat a bare '%' in its
// format string as a conversion directive, corrupting the header
// write whenever a variable header byte (e_entry, e_phoff, ...) is 0x25.
func writePrintfBlob(script *bytes.Buffer, blob []byte) {
	script.WriteString(printfBlob(blob))
}

// printfBlob is writePrintfBlob's escaping, for a caller that renders the
// statement from a template instead of writing it piece by piece.
func printfBlob(blob []byte) string {
	var b strings.Builder
	for _, c := range blob {
		if c >= 0x20 && c < 0x7f && c != '\\' && c != '\'' && c != '%' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "\\%03o", c)
		}
	}
	return b.String()
}

// apeLoaderDirs is where a host that carries no native loader puts the one the
// APE embeds, in order. /dev/shm is tmpfs, so those bytes stay in RAM. /tmp
// follows, for a host without one, which is every darwin host. "${o%/*}" is
// the APE's own directory, last: docker mounts /dev/shm noexec and --read-only
// closes /tmp, so a bind-mounted program may be all that takes an executable.
//
<<<<<<< HEAD
// Fixed at /tmp, and no environment variable is read to find it. TMPDIR
// and HOME are caller-supplied: either can be unset, unwritable, or not
// per-user at all, and a container run by numeric UID gets HOME="/" from
// the runtime itself. /tmp is world-writable on virtually every host.
//
// apeUIDSuffix stands in for the per-user isolation a real HOME would
// give, keeping one user's staged copies out of another user's path.
var apeRunDir = "/tmp/.ape-run-1" + apeUIDSuffix

// apeUIDSuffix is a shell command substitution for the running user's
// numeric uid, read with `id -u` -- a syscall, not an environment
// variable, so it holds even when the caller has configured nothing at
// all. "shared" is the fallback for the one host where even `id` itself
// fails; every real run keeps its own subdirectory as usual, and this one
// case degrades to the pre-uid-scoping behavior rather than failing to
// stage at all.
const apeUIDSuffix = `-$(id -u 2>/dev/null || echo shared)`

// writeStagedCopy emits the shell that gives the host a runnable copy of
// the APE at "$p". It never touches the APE itself: the kernel refuses
// the DOS/shell magic, and writing the real header into the running file
// needs it writable and breaks its checksum, so the COPY is corrected.
// The copy is keyed by the source's device, inode, size and mtime to the
// NANOSECOND, or a checksum where stat is missing: a rebuild within one
// second, in place and at one size, would run the previous binary's copy.
// Staging also registers the magic with binfmt_misc and records whether
// the host can bind-mount; both fail silently. With that mark, and only
// as root, the run binds the copy over the APE's own path in a PRIVATE
// mount namespace, so argv[0] and /proc/self/exe stay put. Without it
// argv[0] is the copy, under the original's basename.
func writeStagedCopy(script *bytes.Buffer, boot []byte, machoOffset, machoSize int) {
	const ddBlockSize = 8
=======
// None of them is a sidecar. -u unlinks the file before the program starts.
//
// Unquoted on purpose: the shell splits it, so APE_LOADERDIR may name several
// directories, and it replaces the list rather than adding to it.
const apeLoaderDirs = `${APE_LOADERDIR:-/dev/shm /tmp "${o%/*}"}`

// writeLoaderBoot emits the shell that hands the APE at "$o" to a native
// loader. The loader reads the file and boots the payload from memory, so
// the APE is never copied and never modified, and a read-only filesystem
// stops being a reason the binary cannot start.
//
// A loader already on the host runs as it stands and writes nothing. A
// host that carries none unpacks the one the APE embeds, once: it is a few
// hundred bytes, it is the same for every APE of that architecture, and
// the tag in its name is its own content hash, so a rebuilt loader always
// unpacks to a path of its own.
func writeLoaderBoot(script *bytes.Buffer, l *apeLoader) {
>>>>>>> origin/master
	data := struct {
		Name   string
		Dirs   string
		Tag    string
		Offset int
		Length int
		Gzip   bool
	}{
		Name:   l.name,
		Dirs:   apeLoaderDirs,
		Tag:    l.tag,
		Offset: l.offset,
		Length: len(l.blob),
		Gzip:   l.gzip,
	}
	writeLoaderSearch(script, l.name)
	if err := apeLoaderTmpl.Execute(script, data); err != nil {
		Exitf("APE: rendering the loader boot script: %v", err)
	}
}

// writeLoaderSearch emits the search for a loader the host already has. It
// reads candidates and execs one. It writes nothing, which is what makes a
// read-only filesystem enough to start the program.
//
<<<<<<< HEAD
// The binfmt_misc line quotes its magic with DOUBLE
// quotes on purpose: the macOS ARM64 loader decodes every `printf '` in
// the first 8K as a boot header, and TestFatBootHeaders holds that count
// at two. The shell leaves \047 alone inside double quotes.
var apeStageTmpl = template.Must(template.New("apestage").Parse(
	`  k=$(stat -c %d.%i.%.9Y.%s "$o" 2>/dev/null || stat -f %d.%i.%Fm.%z "$o" 2>/dev/null || cksum <"$o" | tr -d ' ')
  c="{{.RunDir}}/$k"
  p="$c/${0##*/}"
  if [ ! -x "$p" ]; then
    (umask 077; mkdir -p "$c") || { echo "APE: cannot create $c" >&2; exit 121; }
    cp "$o" "$p.$$" || { echo "APE: cannot stage $p" >&2; exit 121; }
{{- if .Boot}}
    exec 7<> "$p.$$" || { echo "APE: cannot stage $p" >&2; exit 121; }
    printf '{{.Boot}}' >&7
    exec 7<&-
{{- end}}
{{- if .Macho}}
    if [ -d /Applications ]; then
      dd if="$p.$$" of="$p.$$" bs={{.BlockSize}} skip={{.Skip}} count={{.Count}} conv=notrunc 2>/dev/null || { echo 'APE: Mach-O relocation failed' >&2; exit 121; }
    fi
{{- end}}
    chmod 755 "$p.$$" && mv -f "$p.$$" "$p" || { rm -f "$p.$$"; echo "APE: cannot stage $p" >&2; exit 121; }
    if [ "$(id -u 2>/dev/null)" = 0 ]; then
      [ -w /proc/sys/fs/binfmt_misc/register ] || mount -t binfmt_misc none /proc/sys/fs/binfmt_misc 2>/dev/null
      [ -e /proc/sys/fs/binfmt_misc/APE ] || { printf ":APE:M::MZqFpD=\047::/bin/sh:" > /proc/sys/fs/binfmt_misc/register; } 2>/dev/null
      unshare -m true 2>/dev/null && : > "$c/.bind"
    fi
  fi
  if [ -f "$c/.bind" ]; then
    u=$(command -v unshare 2>/dev/null); m=$(command -v mount 2>/dev/null); s=$(command -v sh 2>/dev/null)
    if [ -n "$u" ] && [ -n "$m" ] && [ -n "$s" ]; then
      # Every tool here is resolved BEFORE the caller's PATH comes back,
      # because that PATH may name none of them.
      apepath
      exec "$u" -m "$s" -c 'b="$0"; a="$1"; n="$2"; shift 2; "$n" --bind "$b" "$a" 2>/dev/null && exec "$a" "$@"; exec "$b" "$@"' "$p" "$o" "$m" "$@"
    fi
  fi
  apepath; exec "$p" "$@"
=======
// The absolute candidates come first, because each `command -v` costs a
// PATH walk. APE_LOADER names one outright. Nothing looks beside the
// binary: an APE is one file, and a loader shipped next to it would be a
// second thing to carry. `ape` is last: the cosmo loader of that name
// boots the file too, and a host with cosmopolitan installed has it.
func writeLoaderSearch(script *bytes.Buffer, name string) {
	if err := apeSearchTmpl.Execute(script, struct{ Name string }{name}); err != nil {
		Exitf("APE: rendering the loader search: %v", err)
	}
}

var apeSearchTmpl = template.Must(template.New("apesearch").Parse(
	`  l={{.Name}}
  for c in "${APE_LOADER:-}" /usr/local/lib/ape/$l /usr/lib/ape/$l; do
    [ -x "$c" ] && { apereg "$c"; apepath; exec "$c" "$o" "$@"; }
  done
  for n in $l apeld ape; do
    c=$(command -v "$n" 2>/dev/null) || continue
    [ -n "$c" ] && { apereg "$c"; apepath; exec "$c" "$o" "$@"; }
  done
`))

// apeRegisterFn hands the loader to the kernel, so execve starts an APE
// directly. F opens the interpreter AT REGISTRATION and keeps the
// descriptor, so a read-only image with no loader file on it still starts
// one. Both guards are a stat, so an unprivileged run falls through to the
// search above for free. It succeeds only when THIS run registered the
// entry, which a caller reads as permission to delete that file.
//
// The magic is DOUBLE-quoted, because the cosmo ape loader decodes every
// `printf '` in the first 8192 bytes as a boot header and this is not one.
// The redirect sits inside a group: a shell reports one it cannot open on
// its own stderr.
const apeRegisterFn = `apereg() { [ -e /proc/sys/fs/binfmt_misc/APE ] && return 1
  [ -w /proc/sys/fs/binfmt_misc/register ] || return 1
  { printf ":APE:M::MZqFpD=\047::$1:F" > /proc/sys/fs/binfmt_misc/register; } 2>/dev/null
  [ -e /proc/sys/fs/binfmt_misc/APE ]; }
`

// apeLoaderTmpl puts the loader the APE embeds somewhere the kernel can exec
// it, for a host the search found nothing on. It leaves nothing behind.
//
// -u has the loader unlink its own file first, because a script cannot delete
// anything after it execs. Unlinking first and exec'ing through /dev/fd would
// be shorter, and XNU answers that with EACCES. A tmpfs directory also keeps
// the bytes in RAM, so on linux no disk is touched.
//
// Root hands the loader to binfmt_misc on the way past, so every later run on
// that machine skips this path. APE_NOBINFMT stops a second pass retrying.
var apeLoaderTmpl = template.Must(template.New("apeloader").Parse(
	`  for d in {{.Dirs}}; do
    [ -d "$d" ] && [ -w "$d" ] || continue
    u=$d/.ape-$l-{{.Tag}}.$$
    dd if="$o" bs=1 skip={{.Offset}} count={{.Length}} 2>/dev/null {{if .Gzip}}| gzip -dc {{end}}>"$u" 2>/dev/null || { rm -f "$u"; continue; }
    { [ -s "$u" ] && chmod 700 "$u" && [ -x "$u" ]; } || { rm -f "$u"; continue; }
    if [ -z "${APE_NOBINFMT:-}" ] && apereg "$u"; then
      rm -f "$u"; APE_NOBINFMT=1; export APE_NOBINFMT
      apepath; exec "$o" "$@"
    fi
    apepath; exec "$u" -u "$o" "$@"
  done
  echo "APE: no $l on this host, and nowhere to put the embedded one: install it on PATH, or point APE_LOADER at it" >&2
  exit 121
>>>>>>> origin/master
`))

// makeAPEHeaderForPayloads creates the 64K APE polyglot header that boots
// the given payloads (at most one per architecture family). With both an
// amd64 and an arm64 payload the result is a fat APE: the bootstrap script
// and the embedded boot headers dispatch on the host architecture, and the
// macOS ARM64 APE loader finds the aarch64 image by decoding every printf
// statement in the first 8192 bytes.
//
// apePlatforms decides which boot mechanisms the header carries, and a
// host outside the selection gets a message naming what it was built
// for. The header is a fixed 64K either way, so deselecting a platform
// without dropping its architecture changes the claim, not the size.
func makeAPEHeaderForPayloads(payloads []*apePayload) []byte {
	var amd, arm *apePayload
	for _, p := range payloads {
		switch p.arch {
		case sys.AMD64:
			if amd != nil {
				Exitf("APE: more than one amd64 payload")
			}
			amd = p
		case sys.ARM64:
			if arm != nil {
				Exitf("APE: more than one arm64 payload")
			}
			arm = p
		default:
			Exitf("APE: unsupported payload architecture")
		}
	}
	if amd == nil && arm == nil {
		Exitf("APE: no payloads")
	}

	plat := apePlatforms(payloads)
	linuxAMD := plat.Has(cosmoape.LinuxAMD64)
	windowsAMD := plat.Has(cosmoape.WindowsAMD64)
	linuxARM := plat.Has(cosmoape.LinuxARM64)
	darwinARM := plat.Has(cosmoape.DarwinARM64)

	header := make([]byte, apeHeaderSize)

	// Embedded (printf-encoded) boot ELF headers. They serve two purposes:
	// self-assimilation on Linux, and discovery by the macOS ARM64 APE
	// loader, which octal-decodes every printf in the first 8192 bytes and
	// uses the first one with an aarch64 machine type.
	var amdBoot, armBoot []byte
	if linuxAMD {
		amdBoot = makeEmbeddedElfHeader(amd.elf, amd.offset, sys.AMD64)
	}
	if linuxARM || darwinARM {
		armBoot = makeEmbeddedElfHeader(arm.elf, arm.offset, sys.ARM64)
	}

	// The native loaders the selected platforms boot through, and an index
	// from platform to loader for the branches that emit the shell.
	loaders := apeLoadersFor(plat)
	loaderFor := func(p cosmoape.Platform) *apeLoader {
		for _, l := range loaders {
			if l.name == "apeld-"+p.OS+"-"+p.Arch {
				return l
			}
		}
		Exitf("APE: %s is selected and has no loader", p)
		return nil
	}

	// The header is one file that is both a DOS/PE image, whose e_lfanew
	// at 0x3C points at the PE header at 0x80, and a shell script. The
	// e_lfanew field holds null bytes, which bash refuses to parse, so the
	// heredoc opens BEFORE 0x3C and puts them in its body:
	// - 0x00-0x07: "MZqFpD='", the DOS magic and a shell assignment
	// - 0x08-0x2C: a newline, 35 spaces and the closing quote
	// - 0x2D-0x3B: "\n: <<'__APE__'\n", the heredoc opener
	// - 0x3C+: the heredoc body, e_lfanew and the PE header at 0x80 in it
	// - apeScriptOffset: "__APE__\n" closes the heredoc, then the script

	// Write the APE magic at offset 0
	copy(header[0:8], ape.Magic)
	header[8] = '\n'

	// Fill bytes 0x09-0x2B with spaces (inside the single-quoted string)
	for i := 0x09; i < 0x2C; i++ {
		header[i] = ' '
	}

	// Close the quoted string at 0x2C
	header[0x2C] = '\''

	// Heredoc opener at 0x2D-0x3B (15 bytes: "\n: <<'__APE__'\n")
	// The trailing newline ends the heredoc opener line.
	// Heredoc body starts at 0x3C.
	heredocOpener := []byte("\n: <<'__APE__'\n")
	copy(header[0x2D:], heredocOpener)

	// Now 0x3C+ is heredoc body - null bytes are safe here!
	// e_lfanew at 0x3C-0x3F - must point to PE header at 0x80
	binary.LittleEndian.PutUint32(header[0x3C:], 0x80)

	// Fill bytes 0x40-0x7F with safe content (heredoc body)
	// Use printable characters to avoid any shell parsing issues
	for i := 0x40; i < 0x80; i++ {
		header[i] = '#'
	}

	// The script starts after the transplanted PE headers.
	var script bytes.Buffer
	// apeSelfPath resolves $0 to an absolute path in o, which is the only
	// thing the loader and the staged copy can open. A caller that execs a
	// bare name relies on execvp's rules, so a name with no slash is looked
	// up on PATH and otherwise taken as ./name - the darwin ENOEXEC retry
	// hands the shell exactly that, and net/http/cgi is where it shows up.
	const apeSelfPath = `  o=$0; case $o in */*) ;; *) c=$(command -v "$o" 2>/dev/null); [ -n "$c" ] && o=$c || o=./$o ;; esac; [ -f "$o" ] || o=$(pwd)/${0##*/}; case $o in /*) ;; *) o=$(pwd)/${o#./} ;; esac` + "\n"

	// Here-doc terminator
	script.WriteString("__APE__\n")

	// The standard directories go on PATH before anything reads it. This
	// script runs uname, stat, cksum, tr, mkdir, cp, chmod and mv, and a
	// program started with a scrubbed environment finds none of them. A
	// missing uname is the worst of the set: the arch dispatch then falls
	// back to x86_64 and an arm64 machine is told it cannot run its own
	// binary. The caller's own PATH stays in front.
	script.WriteString("apeP=${PATH-}; apeS=${PATH+1}\n")
	script.WriteString("PATH=\"${PATH:+$PATH:}/usr/bin:/bin:/usr/sbin:/sbin\"; export PATH\n")
	// The program gets the PATH its caller gave it, not the one this script
	// runs its own tools under. net/http/cgi hands a child PATH=/wibble and
	// reads it back, so an appended /usr/bin is a wrong answer.
	script.WriteString("apepath() { if [ -n \"$apeS\" ]; then PATH=$apeP; export PATH; else unset PATH; fi; }\n")
<<<<<<< HEAD
=======
	script.WriteString(apeRegisterFn)
>>>>>>> origin/master

	// Architecture dispatch
	script.WriteString("m=$(uname -m 2>/dev/null) || m=x86_64\n")

	// Each arch branch splits on the host OS first, then hands the file to
	// that platform's native loader. A /Applications directory is what
	// tells macOS from Linux.
	unsupported := func(indent string) {
		fmt.Fprintf(&script, "%s%s; exit 1\n", indent, apeUnsupportedEcho(plat))
	}

	// --- x86-64 hosts ---
	script.WriteString("if [ \"$m\" = x86_64 ] || [ \"$m\" = amd64 ]; then\n")
	switch {
<<<<<<< HEAD
	case linuxAMD || darwinAMD:
		script.WriteString(apeSelfPath)
		if !linuxAMD {
			// Without a boot ELF header there is nothing to assimilate
			// into, and re-execing would spin on this script forever.
			fmt.Fprintf(&script, "  [ -d /Applications ] || { %s; exit 1; }\n", apeUnsupportedEcho(plat))
		}
		if !darwinAMD {
			// Refuse macOS before staging a copy: the printf writes an ELF
			// header, and with no Mach-O header to put over it the copy
			// would not run there anyway.
			fmt.Fprintf(&script, "  [ -d /Applications ] && { %s; exit 1; }\n", apeUnsupportedEcho(plat))
		}
		writeStagedCopy(&script, amdBoot, machoOffset, machoSize)
=======
	case linuxAMD:
		script.WriteString(apeSelfPath)
		// Intel macs are out of support, so this arch means Linux.
		fmt.Fprintf(&script, "  [ -d /Applications ] && { %s; exit 1; }\n", apeUnsupportedEcho(plat))
		writeLoaderBoot(&script, loaderFor(cosmoape.LinuxAMD64))
>>>>>>> origin/master
	case amd == nil:
		script.WriteString("  echo 'APE: x86_64 cannot run ARM64 binary' >&2\n")
		script.WriteString("  exit 1\n")
	default:
		unsupported("  ")
	}
	script.WriteString("fi\n")

	// --- ARM64 hosts ---
	script.WriteString("if [ \"$m\" = aarch64 ] || [ \"$m\" = arm64 ]; then\n")
<<<<<<< HEAD
	if arm != nil {
		script.WriteString(apeSelfPath)
		script.WriteString("  t=\"/tmp/.ape-1.10" + apeUIDSuffix + "\"\n")
		if darwinARM {
			script.WriteString(`  if [ -d /Applications ]; then
    # macOS ARM64: use compiled Mach-O loader or compile from source
    # Don't use existing loader if it might be ELF (from Linux)
    if [ -x "$t" ] && file "$t" 2>/dev/null | grep -q "Mach-O"; then
      apepath; exec "$t" "$o" "$@"
    fi
    # Compile APE loader from embedded source
    if ! type cc >/dev/null 2>&1; then
      echo "$0: please run: xcode-select --install" >&2
      exit 1
    fi
    mkdir -p "${t%/*}" || exit
    dd if="$o" bs=1 skip=APE_LOADER_OFFSET count=APE_LOADER_SIZE 2>/dev/null | gzip -dc >"$t.c.$$" || exit
    mv -f "$t.c.$$" "$t.c" || exit
    cc -w -O -o "$t.$$" "$t.c" || exit
    mv -f "$t.$$" "$t" || exit
    apepath; exec "$t" "$o" "$@"
  fi
`)
		}
		if linuxARM && !darwinARM {
			// Same trap as the amd64 branch: an ELF header on a macOS host
			// leaves something that runs nowhere.
			fmt.Fprintf(&script, "  [ -d /Applications ] && { %s; exit 1; }\n", apeUnsupportedEcho(plat))
		}
		if linuxARM {
			script.WriteString(`  # Linux ARM64: an installed loader runs the file as it stands
  a=$(command -v ape 2>/dev/null); [ -n "$a" ] && { apepath; exec "$a" "$o" "$@"; }
  [ -x "$t" ] && { apepath; exec "$t" "$o" "$@"; }
`)
			writeStagedCopy(&script, armBoot, 0, 0)
=======
	switch {
	case linuxARM || darwinARM:
		script.WriteString(apeSelfPath)
		script.WriteString("  if [ -d /Applications ]; then\n")
		if darwinARM {
			writeLoaderBoot(&script, loaderFor(cosmoape.DarwinARM64))
>>>>>>> origin/master
		} else {
			unsupported("    ")
		}
		script.WriteString("  else\n")
		if linuxARM {
			writeLoaderBoot(&script, loaderFor(cosmoape.LinuxARM64))
		} else {
			unsupported("    ")
		}
		script.WriteString("  fi\n")
	case arm == nil:
		// An amd64 payload cannot run natively here, and Rosetta does not
		// close the gap: the assimilated Mach-O fails codesign's strict
		// validation, and Apple Silicon SIGKILLs an unsigned executable.
		// An arm64 payload is the only answer.
		script.WriteString(`  if [ -d /Applications ]; then
    echo 'APE: this amd64-only binary cannot run natively on ARM64 macOS.' >&2
    echo 'APE: rebuild with ARM64 (fat APE) support to run on Apple Silicon.' >&2
    exit 1
  fi
  echo 'APE: ARM64 Linux cannot run x86_64 binary' >&2
  exit 1
`)
	default:
		unsupported("  ")
	}
	script.WriteString("fi\n")

	if windowsAMD {
		script.WriteString(`# Windows shells (MSYS/Cygwin): delegate to cmd.exe for PE execution
case "$(uname -s 2>/dev/null)" in
CYGWIN*|MINGW*|MSYS*) exec cmd //c "$0" "$@" ;;
esac
`)
	}
	script.WriteString(`echo 'APE: unsupported platform' >&2
exit 1
`)

	// Boot ELF headers, for a loader the host already has rather than for
	// this script. The cosmo loader installed as `ape` locates the payload
	// by octal-decoding every printf in the file's first 8192 bytes, and
	// the search above is happy to exec it. These lines sit after the exit
	// because nothing in this script runs them.
	if len(amdBoot) > 0 {
		script.WriteString("printf '")
		writePrintfBlob(&script, amdBoot)
		script.WriteString("' >&7\n")
	}
	if len(armBoot) > 0 {
		script.WriteString("printf '")
		writePrintfBlob(&script, armBoot)
		script.WriteString("' >&7\n")
	}

	scriptBytes := script.Bytes()

	// Place script after the PE headers.
	scriptOffset := apeScriptOffset
	if len(scriptBytes) > apeHeaderSize-scriptOffset {
		Exitf("APE shell script too large: %d bytes", len(scriptBytes))
	}
	// The loaders are copied over the header after the script; if the
	// script has grown into their regions they would silently clobber its
	// tail, leaving a binary that parses as a broken shell script.
	for _, l := range loaders {
		if scriptOffset+len(scriptBytes) > l.offset {
			Exitf("APE shell script (%d bytes at %#x) overlaps the %s loader at %#x", len(scriptBytes), scriptOffset, l.name, l.offset)
		}
	}
	// The cosmo ape loader scans only the first 8192 bytes for printf
	// statements; every boot header must decode from within that window.
	if scriptOffset+len(scriptBytes) > 8192 {
		Exitf("APE shell script ends at %#x, beyond the loader's 8192-byte scan window", scriptOffset+len(scriptBytes))
	}
	copy(header[scriptOffset:], scriptBytes)

	placeApeLoaders(header, loaders)

	// === PE Header at offset 0x80 ===
	// The polyglot's MZ magic and e_lfanew presume a PE image header
	// here. For windows/amd64 the header really maps the embedded cosmo
	// image and enters the runtime's NT boot stub: computed from the live
	// link's symbols on the thin path, transplanted verbatim from the
	// amd64 input's head on the fat path (same payload offset, same
	// bytes, so the thin header is valid as-is). Otherwise the legacy
	// do-nothing stub keeps the file parseable as a PE.
	switch {
	case !windowsAMD:
		stubArch := sys.ARM64
		if amd != nil {
			stubArch = sys.AMD64
		}
		writePEHeader(header, stubArch)
	case amd.pe != nil:
		writePECosmoAMD64(header, amd)
	case amd.head != nil:
		if *flagApePlatforms != "" {
			checkNTBootHead(amd)
		}
		transplantPEHeader(header, amd)
	case *flagApePlatforms != "":
		Exitf("-apeplatforms selects %s, but the amd64 input carries no NT boot header: pass the thin APE this linker produced, not a raw ELF", cosmoape.WindowsAMD64)
	default:
		writePEHeader(header, sys.AMD64)
	}

	// Ensure there's a newline before the script (required for heredoc terminator)
	// The __APE__ at the start of the script must be at the beginning of a line
	if scriptOffset > 0 {
		header[scriptOffset-1] = '\n'
	}

	// Pad remainder with newlines (safe for shell parsing)
	// Start after the script ends, but skip embedded data regions
	scriptEnd := scriptOffset + len(scriptBytes)
	for i := scriptEnd; i < apeHeaderSize; i++ {
		// Don't overwrite an embedded loader with newlines
		if inApeLoader(loaders, i) {
			continue
		}
		if header[i] == 0 {
			header[i] = '\n'
		}
	}

	return header
}

// makeEmbeddedElfHeader creates an ELF header for embedding in the APE printf statement.
// This header points to the actual ELF segments in the APE file.
func makeEmbeddedElfHeader(origElf []byte, elfOffset uint64, arch sys.ArchFamily) []byte {
	// Create a minimal ELF header (64 bytes for ELF64)
	hdr := make([]byte, 64)

	// ELF magic
	copy(hdr[0:4], elfMagic)
	hdr[4] = elfClass64      // 64-bit
	hdr[5] = elfDataLSB      // Little endian
	hdr[6] = 1               // ELF version
	hdr[7] = elfOSABIFreeBSD // FreeBSD ABI per spec

	// Object file type
	binary.LittleEndian.PutUint16(hdr[16:], elfTypeExec)

	// Machine type
	switch arch {
	case sys.ARM64:
		binary.LittleEndian.PutUint16(hdr[18:], elfMachineARM64)
	default:
		binary.LittleEndian.PutUint16(hdr[18:], elfMachineAMD64)
	}

	// ELF version
	binary.LittleEndian.PutUint32(hdr[20:], 1)

	// Entry point - copy from original
	copy(hdr[24:32], origElf[24:32])

	// Program header offset - adjusted for APE header
	phoff := binary.LittleEndian.Uint64(origElf[32:40])
	binary.LittleEndian.PutUint64(hdr[32:], phoff+elfOffset)

	// Section header offset (set to 0, not used for execution)
	binary.LittleEndian.PutUint64(hdr[40:], 0)

	// Flags
	binary.LittleEndian.PutUint32(hdr[48:], 0)

	// ELF header size
	binary.LittleEndian.PutUint16(hdr[52:], 64)

	// Program header entry size and count - copy from original
	copy(hdr[54:56], origElf[54:56]) // e_phentsize
	copy(hdr[56:58], origElf[56:58]) // e_phnum

	// Section header entry size and count (not used)
	binary.LittleEndian.PutUint16(hdr[58:], 64)
	binary.LittleEndian.PutUint16(hdr[60:], 0)
	binary.LittleEndian.PutUint16(hdr[62:], 0)

	// Section header fields normally stay zero: execution never reads
	// them, and a pristine payload's own table sits at a payload-relative
	// offset that would be wrong in the assimilated file. The one producer
	// of an exception is the -apefat compact debug mode (apedebug.go),
	// whose payload ehdrs reference a section-header view appended past
	// the payload image at an ABSOLUTE APE file offset - recognizable
	// here as an offset at or beyond the payload image's end. That offset
	// stays correct after self-assimilation rewrites the file's first 64
	// bytes with this header, so propagating it is exactly what lets
	// debuggers find the appended debug info in the assimilated binary.
	// Every other payload shape (thin links, stripped or full fat merges)
	// keeps today's zeroed fields, bit for bit.
	if shoff := binary.LittleEndian.Uint64(origElf[40:48]); shoff >= uint64(len(origElf)) {
		binary.LittleEndian.PutUint64(hdr[40:], shoff)
		copy(hdr[60:64], origElf[60:64]) // e_shnum, e_shstrndx
	}

	return hdr
}

<<<<<<< HEAD
// machoSegment describes one LC_SEGMENT_64 load command.
type machoSegment struct {
	name     string
	vmaddr   uint64
	vmsize   uint64
	fileoff  uint64 // absolute offset in the APE file
	filesize uint64
	prot     uint32 // used for both initprot and maxprot
}

// machoSegmentsFromELF derives the Mach-O segment table from the payload's
// PT_LOAD program headers. elfData's p_offset values are still
// payload-relative when this runs (shiftPOffsets rewrites them to absolute
// file offsets at write-out), so file offsets are computed as
// elfOffset + p_offset, matching the bytes of the final APE file.
//
// The first (executable) load is extended downward to file offset 0 so that
// it also maps the APE polyglot header - and, after the dd transform, the
// Mach-O header itself. XNU's parse_machfile rejects (LOAD_BADMACHO) any
// executable in which no R+X segment maps the start of the file
// (found_header_segment); extending the text segment mirrors what
// Cosmopolitan's ape.S Mach-O header does.
func machoSegmentsFromELF(elfData []byte, elfOffset uint64) []machoSegment {
	elfPhoff := binary.LittleEndian.Uint64(elfData[32:40])
	elfPhentsize := binary.LittleEndian.Uint16(elfData[54:56])
	elfPhnum := binary.LittleEndian.Uint16(elfData[56:58])

	var segs []machoSegment
	for i := uint16(0); i < elfPhnum; i++ {
		phdr := elfData[elfPhoff+uint64(i)*uint64(elfPhentsize):]
		if binary.LittleEndian.Uint32(phdr[0:4]) != 1 { // PT_LOAD
			continue
		}
		flags := binary.LittleEndian.Uint32(phdr[4:8])
		off := binary.LittleEndian.Uint64(phdr[8:16])
		vaddr := binary.LittleEndian.Uint64(phdr[16:24])
		filesz := binary.LittleEndian.Uint64(phdr[32:40])
		memsz := binary.LittleEndian.Uint64(phdr[40:48])
		if memsz < filesz {
			Exitf("APE Mach-O: PT_LOAD %d has p_memsz %#x < p_filesz %#x", i, memsz, filesz)
		}
		var prot uint32
		if flags&0x4 != 0 { // PF_R
			prot |= machoProtRead
		}
		if flags&0x2 != 0 { // PF_W
			prot |= machoProtWrite
		}
		if flags&0x1 != 0 { // PF_X
			prot |= machoProtExec
		}
		// Segment names are advisory; pick conventional ones per class.
		name := "__RODATA"
		switch {
		case prot&machoProtExec != 0:
			name = "__TEXT"
		case prot&machoProtWrite != 0:
			name = "__DATA"
		}
		segs = append(segs, machoSegment{
			name:   name,
			vmaddr: vaddr,
			// Rounding p_memsz up to whole pages preserves the BSS:
			// XNU zero-fills [filesize, vmsize) of a segment, so a
			// data segment with p_memsz > p_filesz gets its zero
			// pages from the kernel, like ELF.
			vmsize:   (memsz + machoPageSize - 1) &^ (machoPageSize - 1),
			fileoff:  elfOffset + off,
			filesize: filesz,
			prot:     prot,
		})
	}
	if len(segs) == 0 {
		Exitf("APE Mach-O: ELF payload has no PT_LOAD segments")
	}

	// Extend the first load down to file offset 0 (see function comment).
	first := &segs[0]
	if first.prot&(machoProtRead|machoProtExec) != machoProtRead|machoProtExec {
		Exitf("APE Mach-O: first PT_LOAD is not readable+executable (prot %#x); cannot map the file header", first.prot)
	}
	ext := first.fileoff
	if first.vmaddr <= ext {
		Exitf("APE Mach-O: first PT_LOAD vmaddr %#x is too low to extend the segment to file offset 0", first.vmaddr)
	}
	first.fileoff = 0
	first.filesize += ext
	first.vmaddr -= ext
	first.vmsize += ext

	// XNU's load_segment refuses to map anything whose file offset or vm
	// address is not page-aligned, and overlapping vm ranges would fail
	// vm_map_enter at load time. Go's ELF layout guarantees both (loads
	// are page-rounded and laid out consecutively), so a violation means
	// the layout changed and this code needs revisiting.
	for i := range segs {
		s := &segs[i]
		if s.fileoff%machoPageSize != 0 || s.vmaddr%machoPageSize != 0 {
			Exitf("APE Mach-O: segment %s (fileoff %#x, vmaddr %#x) is not page-aligned", s.name, s.fileoff, s.vmaddr)
		}
		if s.filesize > s.vmsize {
			Exitf("APE Mach-O: segment %s filesize %#x exceeds vmsize %#x", s.name, s.filesize, s.vmsize)
		}
		if i > 0 {
			prev := &segs[i-1]
			if s.vmaddr < prev.vmaddr+prev.vmsize {
				Exitf("APE Mach-O: segment %s (vmaddr %#x) overlaps %s (ends %#x) after page rounding", s.name, s.vmaddr, prev.name, prev.vmaddr+prev.vmsize)
			}
		}
	}
	return segs
}

// makeMachoHeader creates the Mach-O executable header for macOS x86-64.
//
// The bootstrap script dd-copies this header, which sits at 0x2000 in the
// APE header, over the start of a COPY of the file. Its load commands
// point at the embedded amd64 ELF image, and XNU loads it with no dyld
// (LC_UNIXTHREAD, not LC_MAIN), so they must pass parse_machfile and
// load_segment on their own: __PAGEZERO covers [0, lowest mapped address)
// with no access; one LC_SEGMENT_64 per PT_LOAD carries initprot and
// maxprot from p_flags and a vmsize covering p_memsz, at the ELF's own
// addresses; the text segment reaches down to file offset 0, so an R+X
// segment maps the header, which the kernel demands; and LC_UNIXTHREAD
// holds the ELF entry in rip and the XNU host indicator in rcx.
func makeMachoHeader(elfData []byte, elfOffset uint64, elfEntry uint64) []byte {
	segs := machoSegmentsFromELF(elfData, elfOffset)

	// XNU only accepts an entry point that falls inside a segment mapped
	// readable+executable (parse_machfile's validentry check).
	validEntry := false
	for _, s := range segs {
		if elfEntry >= s.vmaddr && elfEntry < s.vmaddr+s.vmsize &&
			s.prot&(machoProtRead|machoProtExec) == machoProtRead|machoProtExec {
			validEntry = true
		}
	}
	if !validEntry {
		Exitf("APE Mach-O: entry point %#x is not inside a readable+executable segment", elfEntry)
	}

	ncmds := 1 + len(segs) + 1 // __PAGEZERO + loads + LC_UNIXTHREAD
	sizeofcmds := machoSegmentCmdSize*(1+len(segs)) + machoUnixThreadCmdSize

	var buf bytes.Buffer

	// Mach-O header (32 bytes)
	binary.Write(&buf, binary.LittleEndian, uint32(machoMagic64))       // magic
	binary.Write(&buf, binary.LittleEndian, uint32(machoCPUTypeX64))    // cputype
	binary.Write(&buf, binary.LittleEndian, uint32(machoCPUSubtypeX64)) // cpusubtype
	binary.Write(&buf, binary.LittleEndian, uint32(machoFileTypeExec))  // filetype
	binary.Write(&buf, binary.LittleEndian, uint32(ncmds))              // ncmds
	binary.Write(&buf, binary.LittleEndian, uint32(sizeofcmds))         // sizeofcmds
	binary.Write(&buf, binary.LittleEndian, uint32(machoFlagNoUndefs))  // flags
	binary.Write(&buf, binary.LittleEndian, uint32(0))                  // reserved

	// __PAGEZERO: MH_EXECUTE images must not map anything below the page
	// zero region; XNU raises the vm map's minimum offset to its end.
	// vmaddr 0, filesize 0, prot 0/0 is the shape load_segment treats as
	// page zero.
	writeMachoSegment(&buf, machoSegment{name: "__PAGEZERO", vmsize: segs[0].vmaddr})

	for _, s := range segs {
		writeMachoSegment(&buf, s)
	}

	writeMachoUnixThread(&buf, elfEntry)

	if buf.Len() != 32+sizeofcmds {
		Exitf("APE Mach-O: internal error: wrote %d header bytes, want %d", buf.Len(), 32+sizeofcmds)
	}
	// The bootstrap script dd-copies the header in 8-byte blocks and
	// derives the block count from this buffer's length; pad to a block
	// boundary so the copy covers exactly the emitted header.
	for buf.Len()%8 != 0 {
		buf.WriteByte(0)
	}

	return buf.Bytes()
}

// writeMachoSegment emits one LC_SEGMENT_64 load command (no sections).
func writeMachoSegment(buf *bytes.Buffer, s machoSegment) {
	binary.Write(buf, binary.LittleEndian, uint32(machoLCSegment64))    // cmd
	binary.Write(buf, binary.LittleEndian, uint32(machoSegmentCmdSize)) // cmdsize
	var name [16]byte
	copy(name[:], s.name)
	buf.Write(name[:]) // segname
	binary.Write(buf, binary.LittleEndian, s.vmaddr)
	binary.Write(buf, binary.LittleEndian, s.vmsize)
	binary.Write(buf, binary.LittleEndian, s.fileoff)
	binary.Write(buf, binary.LittleEndian, s.filesize)
	binary.Write(buf, binary.LittleEndian, s.prot)    // maxprot
	binary.Write(buf, binary.LittleEndian, s.prot)    // initprot
	binary.Write(buf, binary.LittleEndian, uint32(0)) // nsects
	binary.Write(buf, binary.LittleEndian, uint32(0)) // flags
}

// writeMachoUnixThread emits the LC_UNIXTHREAD load command that starts the
// kernel-loaded Mach-O at entry. The register file is exactly the 21
// quadwords of x86_THREAD_STATE64 (count is expressed in 32-bit words, so
// 42), matching the declared cmdsize byte for byte. rsp is left zero, which
// makes XNU allocate a default stack; rcx carries the host-OS indicator so
// rt0_cosmo_amd64.s (which reads CL) knows it is running on XNU.
func writeMachoUnixThread(buf *bytes.Buffer, entry uint64) {
	binary.Write(buf, binary.LittleEndian, uint32(machoLCUnixThread))      // cmd
	binary.Write(buf, binary.LittleEndian, uint32(machoUnixThreadCmdSize)) // cmdsize
	binary.Write(buf, binary.LittleEndian, uint32(machoThreadStateFlavor)) // flavor (x86_THREAD_STATE64)
	binary.Write(buf, binary.LittleEndian, uint32(machoThreadStateRegs*2)) // count (32-bit words)

	var regs [machoThreadStateRegs]uint64
	regs[2] = machoHostXNU // rcx: host OS for rt0 (CL = 8 means XNU)
	regs[16] = entry       // rip
	for _, r := range regs {
		binary.Write(buf, binary.LittleEndian, r)
	}
}

=======
>>>>>>> origin/master
// Real PE header parameters for the cosmo amd64 image (writePECosmoAMD64).
const (
	// peCosmoImageBase is the cosmo/amd64 link base (amd64/obj.go sets
	// FlagTextAddr = 0x100000000 + HEADR), already the multiple of 64K
	// the Windows loader demands of ImageBase. Every PT_LOAD of the
	// image satisfies vaddr - p_offset == peCosmoImageBase (the layout
	// invariant Vaddr == Fileoff mod FlagRound plus lockstep address/
	// offset assignment), so RVA == payload-relative file offset
	// throughout, and PointerToRawData == apeHeaderSize + RVA once the
	// payload sits at apeHeaderSize.
	peCosmoImageBase = 0x100000000
	peCosmoSectAlign = 0x1000
	peCosmoFileAlign = 0x200
	// peCosmoHeadersSize covers the real header chain (ends at 0x208)
	// rounded to FileAlignment; it must stay at or below the first
	// section RVA (0x1000) and at or below AddressOfEntryPoint.
	peCosmoHeadersSize = 0x400
	// peCosmoImportsSize is DataDirectory[1].Size: one import
	// descriptor plus the all-zero terminator entry.
	peCosmoImportsSize = 0x28
	// peCosmoSections is the section count of the real header (.text,
	// .rodata, .data), which also tells it apart from the 1-section stub.
	peCosmoSections = 3
)

// Fixed layout of the runtime.ntidata import blob. Must match the DATA
// directives and layout comment in runtime/rt0_cosmo_nt_amd64.s.
const (
	ntidataSize        = 0x70
	ntidataILT         = 0x28 // import lookup table (2 entries + terminator)
	ntidataHintGetProc = 0x40 // hint/name entry for GetProcAddress
	ntidataHintLoadLib = 0x52 // hint/name entry for LoadLibraryA
	ntidataDLLName     = 0x62 // "kernel32.dll\0"
	ntiatSize          = 24   // import address table (2 slots + terminator)
)

// apePhdr is one PT_LOAD program header of a payload image, with the
// payload-relative file offset.
type apePhdr struct {
	flags  uint32
	off    uint64
	vaddr  uint64
	filesz uint64
	memsz  uint64
}

// apePayloadLoads returns the PT_LOAD program headers of a payload whose
// table payloadFromELF has already validated.
func apePayloadLoads(elf []byte) []apePhdr {
	phoff := binary.LittleEndian.Uint64(elf[32:40])
	phentsize := binary.LittleEndian.Uint16(elf[54:56])
	phnum := binary.LittleEndian.Uint16(elf[56:58])
	var loads []apePhdr
	for i := uint16(0); i < phnum; i++ {
		ph := elf[phoff+uint64(i)*uint64(phentsize):]
		if binary.LittleEndian.Uint32(ph[0:4]) != 1 { // PT_LOAD
			continue
		}
		loads = append(loads, apePhdr{
			flags:  binary.LittleEndian.Uint32(ph[4:8]),
			off:    binary.LittleEndian.Uint64(ph[8:16]),
			vaddr:  binary.LittleEndian.Uint64(ph[16:24]),
			filesz: binary.LittleEndian.Uint64(ph[32:40]),
			memsz:  binary.LittleEndian.Uint64(ph[40:48]),
		})
	}
	return loads
}

// apeVaddrFileOff translates the virtual address range [vaddr,
// vaddr+size) to its payload-relative file offset, requiring the whole
// range to be file-backed (within p_filesz) by a single PT_LOAD.
func apeVaddrFileOff(loads []apePhdr, vaddr, size uint64, what string) uint64 {
	for _, l := range loads {
		if vaddr >= l.vaddr && vaddr+size <= l.vaddr+l.filesz {
			return l.off + (vaddr - l.vaddr)
		}
	}
	Exitf("APE NT boot: %s (vaddr %#x, %d bytes) is not file-backed by any PT_LOAD; it must be initialized data, not BSS", what, vaddr, size)
	return 0
}

// apePrepareNTBoot resolves the NT boot symbols from the live link,
// patches the five RVA fields of the runtime.ntidata import blob in the
// payload bytes, and attaches the header RVAs to the payload for
// writePECosmoAMD64. Runs on the thin amd64 path only (convertToAPE),
// where ctxt.loader is still alive.
func apePrepareNTBoot(ctxt *Link, p *apePayload) {
	ldr := ctxt.loader
	sym := func(name string, wantSize int64) uint64 {
		s := ldr.Lookup(name, 0)
		if s == 0 {
			Exitf("APE NT boot: symbol %s not found; it should be a deadcode root for cosmo/amd64", name)
		}
		if wantSize >= 0 && ldr.SymSize(s) != wantSize {
			Exitf("APE NT boot: %s is %d bytes, want %d (layout contract with rt0_cosmo_nt_amd64.s)", name, ldr.SymSize(s), wantSize)
		}
		v := uint64(ldr.SymValue(s))
		if v < peCosmoImageBase || v-peCosmoImageBase >= 1<<32 {
			Exitf("APE NT boot: %s at %#x is outside the PE image (base %#x)", name, v, uint64(peCosmoImageBase))
		}
		return v
	}
	entry := sym("_rt0_cosmo_nt", -1)
	idata := sym("runtime.ntidata", ntidataSize)
	iat := sym("runtime.ntiat", ntiatSize)

	loads := apePayloadLoads(p.elf)
	idataOff := apeVaddrFileOff(loads, idata, ntidataSize, "runtime.ntidata")
	// The IAT must be file-backed too: the NT loader resolves imports by
	// overwriting bytes that exist in the file image.
	apeVaddrFileOff(loads, iat, ntiatSize, "runtime.ntiat")

	// Cross-check the blob's fixed layout against the strings the asm
	// placed, so a drifted rt0_cosmo_nt_amd64.s fails the link loudly
	// instead of producing an unloadable import table.
	blob := p.elf[idataOff : idataOff+ntidataSize]
	for _, want := range []struct {
		off int
		s   string
	}{
		{ntidataHintGetProc + 2, "GetProcAddress\x00"},
		{ntidataHintLoadLib + 2, "LoadLibraryA\x00"},
		{ntidataDLLName, "kernel32.dll\x00"},
	} {
		if got := string(blob[want.off : want.off+len(want.s)]); got != want.s {
			Exitf("APE NT boot: runtime.ntidata+%#x holds %q, want %q; blob layout out of sync with rt0_cosmo_nt_amd64.s", want.off, got, want.s)
		}
	}

	idataRVA := uint32(idata - peCosmoImageBase)
	iatRVA := uint32(iat - peCosmoImageBase)
	// Patch the five RVA fields (layout comment in rt0_cosmo_nt_amd64.s).
	binary.LittleEndian.PutUint32(blob[0x00:], idataRVA+ntidataILT)                         // IDT[0].OriginalFirstThunk
	binary.LittleEndian.PutUint32(blob[0x0C:], idataRVA+ntidataDLLName)                     // IDT[0].Name
	binary.LittleEndian.PutUint32(blob[0x10:], iatRVA)                                      // IDT[0].FirstThunk
	binary.LittleEndian.PutUint64(blob[ntidataILT:], uint64(idataRVA)+ntidataHintGetProc)   // ILT[0]
	binary.LittleEndian.PutUint64(blob[ntidataILT+8:], uint64(idataRVA)+ntidataHintLoadLib) // ILT[1]

	p.pe = &apePEInfo{
		entryRVA:   uint32(entry - peCosmoImageBase),
		importsRVA: idataRVA,
	}
}

// peCosmoSection is one section header of the real amd64 PE header.
type peCosmoSection struct {
	name  string
	rva   uint32 // VirtualAddress
	vsz   uint32 // VirtualSize (BSS beyond rawsz is zero-filled)
	raw   uint32 // PointerToRawData, an absolute APE file offset
	rawsz uint32 // SizeOfRawData
	chars uint32
}

// writePECosmoAMD64 writes the real PE header for an amd64 payload: a
// PE32+ image at base peCosmoImageBase whose three sections map the
// payload's PT_LOADs (skipping the payload's ELF-header page, which the
// PE headers region occupies virtually), whose entry point is the
// runtime's _rt0_cosmo_nt stub, and whose import directory points at
// the runtime.ntidata blob patched by apePrepareNTBoot.
func writePECosmoAMD64(header []byte, amd *apePayload) {
	info := amd.pe
	loads := apePayloadLoads(amd.elf)
	if len(loads) != 3 {
		Exitf("APE PE: amd64 payload has %d PT_LOADs, want 3 (RX text, R rodata, RW data)", len(loads))
	}
	const (
		elfPFExec  = 1
		elfPFWrite = 2
		elfPFRead  = 4
	)
	wantFlags := [3]uint32{elfPFRead | elfPFExec, elfPFRead, elfPFRead | elfPFWrite}
	for i, l := range loads {
		if l.flags != wantFlags[i] {
			Exitf("APE PE: PT_LOAD %d has flags %#x, want %#x", i, l.flags, wantFlags[i])
		}
		if l.vaddr-l.off != peCosmoImageBase {
			Exitf("APE PE: PT_LOAD %d has vaddr %#x - offset %#x != image base %#x; RVAs would not equal payload offsets", i, l.vaddr, l.off, uint64(peCosmoImageBase))
		}
		if l.off%peCosmoSectAlign != 0 {
			Exitf("APE PE: PT_LOAD %d file offset %#x is not %#x-aligned", i, l.off, peCosmoSectAlign)
		}
		if l.memsz < l.filesz {
			Exitf("APE PE: PT_LOAD %d has memsz %#x < filesz %#x", i, l.memsz, l.filesz)
		}
	}
	text, ro, data := loads[0], loads[1], loads[2]
	if text.off != 0 || text.filesz <= peCosmoSectAlign {
		Exitf("APE PE: text load must start at payload offset 0 and extend past the ELF header page (off %#x, filesz %#x)", text.off, text.filesz)
	}
	if text.memsz != text.filesz {
		Exitf("APE PE: text load has memsz %#x != filesz %#x", text.memsz, text.filesz)
	}
	end := data.off + data.memsz
	if end >= 1<<32 {
		Exitf("APE PE: image end %#x does not fit the 32-bit RVA space", end)
	}

	// .data's SizeOfRawData is p_filesz rounded up to FileAlignment; the
	// rounding tail is loaded into memory ahead of the zero-filled BSS,
	// so it must be zero bytes in the file (the linker's next file area
	// starts at a page-rounded offset, leaving zero padding here).
	dataRawSize := (data.filesz + peCosmoFileAlign - 1) &^ uint64(peCosmoFileAlign-1)
	for i := data.off + data.filesz; i < data.off+dataRawSize; i++ {
		if i >= uint64(len(amd.elf)) || amd.elf[i] != 0 {
			Exitf("APE PE: byte %#x of the payload is not zero padding; cannot round .data raw size %#x up to FileAlignment", i, data.filesz)
		}
	}

	sects := [3]peCosmoSection{
		{".text", uint32(text.off + peCosmoSectAlign), uint32(text.memsz - peCosmoSectAlign),
			uint32(amd.offset+text.off) + peCosmoSectAlign, uint32(text.filesz - peCosmoSectAlign),
			0x60000020}, // CODE | EXECUTE | READ
		{".rodata", uint32(ro.off), uint32(ro.memsz),
			uint32(amd.offset + ro.off), uint32(ro.filesz),
			0x40000040}, // INITIALIZED_DATA | READ
		{".data", uint32(data.off), uint32(data.memsz),
			uint32(amd.offset + data.off), uint32(dataRawSize),
			0xC0000040}, // INITIALIZED_DATA | READ | WRITE
	}
	sizeOfImage := (uint32(end) + peCosmoSectAlign - 1) &^ uint32(peCosmoSectAlign-1)

	if t := sects[0]; info.entryRVA < t.rva || info.entryRVA >= t.rva+t.vsz {
		Exitf("APE PE: entry RVA %#x is outside .text [%#x, %#x)", info.entryRVA, t.rva, t.rva+t.vsz)
	}
	if d := sects[2]; info.importsRVA < d.rva || info.importsRVA+peCosmoImportsSize > d.rva+d.vsz {
		Exitf("APE PE: import directory RVA %#x is outside .data [%#x, %#x)", info.importsRVA, d.rva, d.rva+d.vsz)
	}

	peStart := 0x80
	copy(header[peStart:], []byte{'P', 'E', 0, 0})

	// COFF header.
	coffStart := peStart + 4
	binary.LittleEndian.PutUint16(header[coffStart+0:], 0x8664)          // Machine: amd64
	binary.LittleEndian.PutUint16(header[coffStart+2:], peCosmoSections) // NumberOfSections
	binary.LittleEndian.PutUint32(header[coffStart+4:], 0)               // TimeDateStamp
	binary.LittleEndian.PutUint32(header[coffStart+8:], 0)               // PointerToSymbolTable
	binary.LittleEndian.PutUint32(header[coffStart+12:], 0)              // NumberOfSymbols
	binary.LittleEndian.PutUint16(header[coffStart+16:], 240)            // SizeOfOptionalHeader
	// RELOCS_STRIPPED | EXECUTABLE_IMAGE | LARGE_ADDRESS_AWARE |
	// DEBUG_STRIPPED, matching real Cosmopolitan APEs. RELOCS_STRIPPED
	// is honest: cosmo code is position-dependent and there is no
	// .reloc section, so the image must load at ImageBase or not at all.
	binary.LittleEndian.PutUint16(header[coffStart+18:], 0x0223) // Characteristics

	// Optional header (PE32+).
	optStart := coffStart + 20
	binary.LittleEndian.PutUint16(header[optStart+0:], 0x20B)               // Magic: PE32+
	header[optStart+2] = 1                                                  // MajorLinkerVersion
	header[optStart+3] = 0                                                  // MinorLinkerVersion
	binary.LittleEndian.PutUint32(header[optStart+4:], 0)                   // SizeOfCode (unused by loaders)
	binary.LittleEndian.PutUint32(header[optStart+8:], 0)                   // SizeOfInitializedData
	binary.LittleEndian.PutUint32(header[optStart+12:], 0)                  // SizeOfUninitializedData
	binary.LittleEndian.PutUint32(header[optStart+16:], info.entryRVA)      // AddressOfEntryPoint
	binary.LittleEndian.PutUint32(header[optStart+20:], sects[0].rva)       // BaseOfCode
	binary.LittleEndian.PutUint64(header[optStart+24:], peCosmoImageBase)   // ImageBase
	binary.LittleEndian.PutUint32(header[optStart+32:], peCosmoSectAlign)   // SectionAlignment
	binary.LittleEndian.PutUint32(header[optStart+36:], peCosmoFileAlign)   // FileAlignment
	binary.LittleEndian.PutUint16(header[optStart+40:], 6)                  // MajorOSVersion
	binary.LittleEndian.PutUint16(header[optStart+42:], 0)                  // MinorOSVersion
	binary.LittleEndian.PutUint16(header[optStart+44:], 0)                  // MajorImageVersion
	binary.LittleEndian.PutUint16(header[optStart+46:], 0)                  // MinorImageVersion
	binary.LittleEndian.PutUint16(header[optStart+48:], 6)                  // MajorSubsystemVersion
	binary.LittleEndian.PutUint16(header[optStart+50:], 0)                  // MinorSubsystemVersion
	binary.LittleEndian.PutUint32(header[optStart+52:], 0)                  // Win32VersionValue
	binary.LittleEndian.PutUint32(header[optStart+56:], sizeOfImage)        // SizeOfImage
	binary.LittleEndian.PutUint32(header[optStart+60:], peCosmoHeadersSize) // SizeOfHeaders
	binary.LittleEndian.PutUint32(header[optStart+64:], 0)                  // CheckSum
	binary.LittleEndian.PutUint16(header[optStart+68:], 3)                  // Subsystem: CONSOLE
	// NX_COMPAT | TERMINAL_SERVER_AWARE. Deliberately no DYNAMIC_BASE or
	// HIGH_ENTROPY_VA: with relocations stripped, ASLR must not be
	// invited to move the image off its link base.
	binary.LittleEndian.PutUint16(header[optStart+70:], 0x8100)   // DllCharacteristics
	binary.LittleEndian.PutUint64(header[optStart+72:], 0x800000) // SizeOfStackReserve (8 MiB)
	// rt0_go carves g0's stack as [entry SP - 64K, entry SP], so the
	// commit must hand the entry thread at least 64K up front.
	binary.LittleEndian.PutUint64(header[optStart+80:], 0x10000)  // SizeOfStackCommit
	binary.LittleEndian.PutUint64(header[optStart+88:], 0x100000) // SizeOfHeapReserve
	binary.LittleEndian.PutUint64(header[optStart+96:], 0x1000)   // SizeOfHeapCommit
	binary.LittleEndian.PutUint32(header[optStart+104:], 0)       // LoaderFlags
	binary.LittleEndian.PutUint32(header[optStart+108:], 16)      // NumberOfRvaAndSizes
	// Data directories: only [1] (imports) is populated.
	dirStart := optStart + 112
	binary.LittleEndian.PutUint32(header[dirStart+8:], info.importsRVA)
	binary.LittleEndian.PutUint32(header[dirStart+12:], peCosmoImportsSize)

	// Section table (ends at 0x208, within the [0x80, 0x7FF) budget the
	// shell script at apeScriptOffset leaves for the PE header chain).
	sectStart := optStart + 240
	for i, s := range sects {
		sh := header[sectStart+40*i:]
		copy(sh[0:8], s.name)
		binary.LittleEndian.PutUint32(sh[8:], s.vsz)
		binary.LittleEndian.PutUint32(sh[12:], s.rva)
		binary.LittleEndian.PutUint32(sh[16:], s.rawsz)
		binary.LittleEndian.PutUint32(sh[20:], s.raw)
		binary.LittleEndian.PutUint32(sh[24:], 0) // PointerToRelocations
		binary.LittleEndian.PutUint32(sh[28:], 0) // PointerToLinenumbers
		binary.LittleEndian.PutUint16(sh[32:], 0) // NumberOfRelocations
		binary.LittleEndian.PutUint16(sh[34:], 0) // NumberOfLinenumbers
		binary.LittleEndian.PutUint32(sh[36:], s.chars)
	}
}

// transplantPEHeader copies the amd64 input's PE header region verbatim
// into a fat APE's head. The thin link computed a header whose RVAs and
// absolute raw data pointers are equally valid in the fat file: the
// amd64 image lands at the same file offset (apeHeaderSize) with
// byte-identical content, imports blob included.
func transplantPEHeader(header []byte, amd *apePayload) {
	if len(amd.head) < apeScriptOffset {
		Exitf("APE PE transplant: amd64 input head is %d bytes, want at least %#x", len(amd.head), apeScriptOffset)
	}
	if string(amd.head[0x80:0x84]) != "PE\x00\x00" {
		Exitf("APE PE transplant: amd64 input has no PE signature at 0x80")
	}
	if amd.offset != apeHeaderSize {
		Exitf("APE PE transplant: amd64 payload at %#x, want %#x; the transplanted header's raw data pointers assume the thin layout", amd.offset, uint64(apeHeaderSize))
	}
	copy(header[0x80:apeScriptOffset], amd.head[0x80:apeScriptOffset])
}

// writePEHeader writes the legacy stub PE header: a parseable console
// PE32+ whose entry immediately returns 0, mapping nothing of the
// payload. It remains for outputs that cannot carry the real header:
// arm64-only APEs (no NT support) and synthetic payloads without a live
// link or an input head (ld tests).
func writePEHeader(header []byte, arch sys.ArchFamily) {
	peStart := 0x80

	// PE Signature
	copy(header[peStart:], []byte{'P', 'E', 0, 0})

	// COFF Header
	coffStart := peStart + 4
	var machineType uint16
	switch arch {
	case sys.ARM64:
		machineType = 0xAA64
	default:
		machineType = 0x8664
	}
	binary.LittleEndian.PutUint16(header[coffStart+0:], machineType)
	binary.LittleEndian.PutUint16(header[coffStart+2:], 1)     // NumberOfSections
	binary.LittleEndian.PutUint32(header[coffStart+4:], 0)     // TimeDateStamp
	binary.LittleEndian.PutUint32(header[coffStart+8:], 0)     // PointerToSymbolTable
	binary.LittleEndian.PutUint32(header[coffStart+12:], 0)    // NumberOfSymbols
	binary.LittleEndian.PutUint16(header[coffStart+16:], 240)  // SizeOfOptionalHeader
	binary.LittleEndian.PutUint16(header[coffStart+18:], 0x22) // Characteristics

	// Optional Header (PE32+)
	optStart := coffStart + 20
	binary.LittleEndian.PutUint16(header[optStart+0:], 0x20B)        // Magic: PE32+
	header[optStart+2] = 1                                           // MajorLinkerVersion
	header[optStart+3] = 0                                           // MinorLinkerVersion
	binary.LittleEndian.PutUint32(header[optStart+4:], 0x200)        // SizeOfCode
	binary.LittleEndian.PutUint32(header[optStart+8:], 0)            // SizeOfInitializedData
	binary.LittleEndian.PutUint32(header[optStart+12:], 0)           // SizeOfUninitializedData
	binary.LittleEndian.PutUint32(header[optStart+16:], 0x1000)      // AddressOfEntryPoint
	binary.LittleEndian.PutUint32(header[optStart+20:], 0x1000)      // BaseOfCode
	binary.LittleEndian.PutUint64(header[optStart+24:], 0x140000000) // ImageBase
	binary.LittleEndian.PutUint32(header[optStart+32:], 0x1000)      // SectionAlignment
	binary.LittleEndian.PutUint32(header[optStart+36:], 0x200)       // FileAlignment
	binary.LittleEndian.PutUint16(header[optStart+40:], 6)           // MajorOSVersion
	binary.LittleEndian.PutUint16(header[optStart+42:], 0)           // MinorOSVersion
	binary.LittleEndian.PutUint16(header[optStart+44:], 0)           // MajorImageVersion
	binary.LittleEndian.PutUint16(header[optStart+46:], 0)           // MinorImageVersion
	binary.LittleEndian.PutUint16(header[optStart+48:], 6)           // MajorSubsystemVersion
	binary.LittleEndian.PutUint16(header[optStart+50:], 0)           // MinorSubsystemVersion
	binary.LittleEndian.PutUint32(header[optStart+52:], 0)           // Win32VersionValue
	binary.LittleEndian.PutUint32(header[optStart+56:], 0x2000)      // SizeOfImage
	binary.LittleEndian.PutUint32(header[optStart+60:], 0x200)       // SizeOfHeaders
	binary.LittleEndian.PutUint32(header[optStart+64:], 0)           // CheckSum
	binary.LittleEndian.PutUint16(header[optStart+68:], 3)           // Subsystem: CONSOLE
	binary.LittleEndian.PutUint16(header[optStart+70:], 0x8160)      // DllCharacteristics
	binary.LittleEndian.PutUint64(header[optStart+72:], 0x100000)    // SizeOfStackReserve
	binary.LittleEndian.PutUint64(header[optStart+80:], 0x1000)      // SizeOfStackCommit
	binary.LittleEndian.PutUint64(header[optStart+88:], 0x100000)    // SizeOfHeapReserve
	binary.LittleEndian.PutUint64(header[optStart+96:], 0x1000)      // SizeOfHeapCommit
	binary.LittleEndian.PutUint32(header[optStart+104:], 0)          // LoaderFlags
	binary.LittleEndian.PutUint32(header[optStart+108:], 16)         // NumberOfRvaAndSizes

	// Section Header
	sectStart := optStart + 240
	copy(header[sectStart:], []byte(".text\x00\x00\x00"))
	binary.LittleEndian.PutUint32(header[sectStart+8:], 0x1000)
	binary.LittleEndian.PutUint32(header[sectStart+12:], 0x1000)
	binary.LittleEndian.PutUint32(header[sectStart+16:], 0x200)
	binary.LittleEndian.PutUint32(header[sectStart+20:], 0x200)
	binary.LittleEndian.PutUint32(header[sectStart+24:], 0)
	binary.LittleEndian.PutUint32(header[sectStart+28:], 0)
	binary.LittleEndian.PutUint16(header[sectStart+32:], 0)
	binary.LittleEndian.PutUint16(header[sectStart+34:], 0)
	binary.LittleEndian.PutUint32(header[sectStart+36:], 0x60000020)

	// Minimal entry stub at 0x200, matching the COFF machine type.
	switch machineType {
	case 0xAA64:
		copy(header[0x200:], []byte{
			0x00, 0x00, 0x80, 0x52, // mov w0, #0
			0xC0, 0x03, 0x5F, 0xD6, // ret
		})
	default:
		header[0x200] = 0x31 // xor eax, eax
		header[0x201] = 0xC0
		header[0x202] = 0xC3 // ret
	}
}
