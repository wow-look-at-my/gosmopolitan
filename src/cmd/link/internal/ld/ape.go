// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

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

// APE ( Portable Executable), per ape/specification.md.

const (
	// APE header must be page-aligned for ELF loading Using 64KB for Windows allocation granularity compatibility.
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

// convertToAPE converts an ELF binary to Portable Executable format.
type apePayload struct {
	elf    []byte // complete ELF image; p_offset values are payload-relative
	arch   sys.ArchFamily
	offset uint64 // file offset of this image inside the APE; set by layoutAPE

	// pe carries the symbol RVAs the real amd64 PE header needs.
	pe *apePEInfo

	// head is the 64K APE head of the input file this payload was extracted from, set only on the -apefat merge path.
	head []byte
}

// apePEInfo holds the image RVAs, resolved from the live link's symbol table,
// that writePECosmoAMD64 places in the PE header.
type apePEInfo struct {
	entryRVA    uint32 // the PE AddressOfEntryPoint
	importsRVA  uint32 // the import directory table
	importsSize uint32 // the import directory table, its zero descriptor included
	iatRVA      uint32
	iatSize     uint32
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
	// Validate the program header table up front: shiftPOffsets, makeEmbeddedElfHeader, makeMachoHeader, payloadExtent, stripPayload.
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

	// Read the ELF file we created
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

// writePrintfBlob escapes blob into script as the body of a shell printf
// '...' statement: printable ASCII stays literal.
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

// apeLoaderDirs is where a host that carries no native loader puts the one the APE embeds, in order. /dev/shm is tmpfs.
const apeLoaderDirs = `${APE_LOADERDIR:-/dev/shm /tmp "${o%/*}"}`

// writeLoaderBoot emits the shell that hands the APE at "$o" to a native
// loader. The loader reads the file and boots the payload from memory, so
// the APE is never copied and never modified, and a read-only filesystem
// stops being a reason the binary cannot start.
func writeLoaderBoot(script *bytes.Buffer, l *apeLoader) {
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
// directly. F opens the interpreter AT REGISTRATION and keeps the descriptor,
// so a read-only image with no loader file on it still starts one.
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
// that machine skips this path. APE_NOBINFMT stops another pass retrying.
var apeLoaderTmpl = template.Must(template.New("apeloader").Parse(
	`  for d in {{.Dirs}}; do
    [ -d "$d" ] && [ -w "$d" ] || continue
    u=$d/.ape-$l-{{.Tag}}.$$
    dd if="$o" bs=1 skip={{.Offset}} count={{.Length}} 2>/dev/null {{if .Gzip}}| gzip -dc {{end}}>"$u" 2>/dev/null || { rm -f "$u"; continue; }
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
`))

// makeAPEHeaderForPayloads creates the 64K APE polyglot header that boots
// the given payloads (at most one per architecture family). With both an
// amd64 and an arm64 payload the result is a fat APE: the bootstrap script
// and the embedded boot headers dispatch on the host architecture, and the
// macOS ARM64 APE loader finds the aarch64 image by decoding every printf
// statement in the first many bytes.
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

	// Embedded (printf-encoded) boot ELF headers.
	var amdBoot, armBoot []byte
	if linuxAMD {
		amdBoot = makeEmbeddedElfHeader(amd.elf, amd.offset, sys.AMD64)
	}
	if linuxARM || darwinARM {
		armBoot = makeEmbeddedElfHeader(arm.elf, arm.offset, sys.ARM64)
	}

	// The native loaders the selected platforms boot through.
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

	// The header is one file that is both a DOS/PE image, whose e_lfanew at 0x3C points at the PE header at 0x80.

	copy(header[0:8], ape.Magic)
	header[8] = '\n'

	// Fill bytes 0x09-0x2B with spaces (inside the single-quoted string)
	for i := 0x09; i < 0x2C; i++ {
		header[i] = ' '
	}

	// Close the quoted string at 0x2C
	header[0x2C] = '\''

	// Heredoc opener at 0x2D-0x3B (several bytes: "\n: <<'__APE__'\n") The trailing newline ends the heredoc opener line.
	heredocOpener := []byte("\n: <<'__APE__'\n")
	copy(header[0x2D:], heredocOpener)

	// Now 0x3C+ is heredoc body - null bytes are safe here! e_lfanew at 0x3C-0x3F - must point to PE header at 0x80
	binary.LittleEndian.PutUint32(header[0x3C:], 0x80)

	// Fill bytes 0x40-0x7F with safe content (heredoc body)
	// Use printable characters to avoid any shell parsing issues
	for i := 0x40; i < 0x80; i++ {
		header[i] = '#'
	}

	// The script starts after the transplanted PE headers.
	var script bytes.Buffer
	// apeSelfPath resolves $0 to an absolute path in o, which is the only thing the loader and the staged copy can open.
	const apeSelfPath = `  o=$0; case $o in */*) ;; *) c=$(command -v "$o" 2>/dev/null); [ -n "$c" ] && o=$c || o=./$o ;; esac; [ -f "$o" ] || o=$(pwd)/${0##*/}; case $o in /*) ;; *) o=$(pwd)/${o#./} ;; esac` + "\n"

	// Here-doc terminator
	script.WriteString("__APE__\n")

	// The standard directories go on PATH before anything reads it.
	script.WriteString("apeP=${PATH-}; apeS=${PATH+1}\n")
	script.WriteString("PATH=\"${PATH:+$PATH:}/usr/bin:/bin:/usr/sbin:/sbin\"; export PATH\n")
	// The program gets the PATH its caller gave it.
	script.WriteString("apepath() { if [ -n \"$apeS\" ]; then PATH=$apeP; export PATH; else unset PATH; fi; }\n")
	script.WriteString(apeRegisterFn)

	// Architecture dispatch
	script.WriteString("m=$(uname -m 2>/dev/null) || m=x86_64\n")

	// Each arch branch splits on the host OS first, then hands the file to that
	// platform's native loader.
	unsupported := func(indent string) {
		fmt.Fprintf(&script, "%s%s; exit 1\n", indent, apeUnsupportedEcho(plat))
	}

	// --- x86-hosts ---
	script.WriteString("if [ \"$m\" = x86_64 ] || [ \"$m\" = amd64 ]; then\n")
	switch {
	case linuxAMD:
		script.WriteString(apeSelfPath)
		// Intel macs are out of support, so this arch means Linux.
		fmt.Fprintf(&script, "  [ -d /Applications ] && { %s; exit 1; }\n", apeUnsupportedEcho(plat))
		writeLoaderBoot(&script, loaderFor(cosmoape.LinuxAMD64))
	case amd == nil:
		script.WriteString("  echo 'APE: x86_64 cannot run ARM64 binary' >&2\n")
		script.WriteString("  exit 1\n")
	default:
		unsupported("  ")
	}
	script.WriteString("fi\n")

	// --- ARM64 hosts ---
	script.WriteString("if [ \"$m\" = aarch64 ] || [ \"$m\" = arm64 ]; then\n")
	switch {
	case linuxARM || darwinARM:
		script.WriteString(apeSelfPath)
		script.WriteString("  if [ -d /Applications ]; then\n")
		if darwinARM {
			writeLoaderBoot(&script, loaderFor(cosmoape.DarwinARM64))
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

	// Boot ELF headers, for a loader the host already has rather than for this
	// script.
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
	// The cosmo ape loader scans only the first many bytes for printf
	// statements; every boot header must decode from within that window.
	if scriptOffset+len(scriptBytes) > 8192 {
		Exitf("APE shell script ends at %#x, beyond the loader's 8192-byte scan window", scriptOffset+len(scriptBytes))
	}
	copy(header[scriptOffset:], scriptBytes)

	placeApeLoaders(header, loaders)

	// === PE Header at offset 0x80 === The polyglot's MZ magic and e_lfanew
	// presume a PE image header here. For windows/amd64 the header maps the
	// embedded cosmo image and enters the runtime's NT boot stub: computed from
	// the live link's symbols on the thin path, transplanted verbatim from the
	// amd64 input's head on the fat path (same payload offset, same bytes, so
	// the thin header is valid as-is). Otherwise the do-nothing stub keeps the
	// file parseable as a PE.
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

	// Ensure there's a newline before the script (required for heredoc terminator) The __APE__ at the start of the script must be at the beginning.
	if scriptOffset > 0 {
		header[scriptOffset-1] = '\n'
	}

	// Pad remainder with newlines (safe for shell parsing) Start after the script ends, but skip embedded data regions
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
	// Create a minimal ELF header (many bytes for ELF64)
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

	// Section header fields normally stay zero: execution never reads them.
	if shoff := binary.LittleEndian.Uint64(origElf[40:48]); shoff >= uint64(len(origElf)) {
		binary.LittleEndian.PutUint64(hdr[40:], shoff)
		copy(hdr[60:64], origElf[60:64]) // e_shnum, e_shstrndx
	}

	return hdr
}

// Real PE header parameters for the cosmo amd64 image (writePECosmoAMD64).
const (
	// peCosmoImageBase is the cosmo/amd64 internal link base (amd64/obj.go sets FlagTextAddr = 0x100000000 + HEADR).
	peCosmoImageBase = 0x100000000
	peCosmoSectAlign = 0x1000
	peCosmoFileAlign = 0x200
	// peCosmoHeadersSize covers the real header chain (ends at 0x208) rounded to FileAlignment.
	peCosmoHeadersSize = 0x400
	peCosmoImportsSize = 0x28
	// peCosmoSections is the section count of the real header (.text, .rodata, .data).
	peCosmoSections = 3
)

// Fixed layout of the runtime.ntidata import blob. Must match the DATA
// directives and layout comment in runtime/rt0_cosmo_nt_amd64.s.
const (
	ntidataSize        = 0x70
	ntidataILT         = 0x28 // import lookup table (entries + terminator)
	ntidataHintGetProc = 0x40 // hint/name entry for GetProcAddress
	ntidataHintLoadLib = 0x52 // hint/name entry for LoadLibraryA
	ntidataDLLName     = 0x62 // "kernel32.dll\0"
	ntiatSize          = 24   // import address table (slots + terminator)
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

// apeImageBase returns vaddr - p_offset of the first PT_LOAD of a payload,
// which is the PE ImageBase of an amd64 image.
func apeImageBase(elf []byte) uint64 {
	loads := apePayloadLoads(elf)
	if len(loads) == 0 {
		return 0
	}
	return loads[0].vaddr - loads[0].off
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
// patches those RVA fields of the runtime.ntidata import blob in the
// payload bytes, and attaches the header RVAs to the payload for
// writePECosmoAMD64. Runs on the thin amd64 path only (convertToAPE),
// where ctxt.loader is still alive.
func apePrepareNTBoot(ctxt *Link, p *apePayload) {
	ldr := ctxt.loader
	base := apeImageBase(p.elf)
	sym := func(name string, wantSize int64) uint64 {
		s := ldr.Lookup(name, 0)
		if s == 0 {
			Exitf("APE NT boot: symbol %s not found; it should be a deadcode root for cosmo/amd64", name)
		}
		if wantSize >= 0 && ldr.SymSize(s) != wantSize {
			Exitf("APE NT boot: %s is %d bytes, want %d (layout contract with rt0_cosmo_nt_amd64.s)", name, ldr.SymSize(s), wantSize)
		}
		v := uint64(ldr.SymValue(s))
		if v < base || v-base >= 1<<32 {
			Exitf("APE NT boot: %s at %#x is outside the PE image (base %#x)", name, v, base)
		}
		return v
	}
	if ctxt.LinkMode == LinkExternal {
		p.pe = cosmoNTBoot(p.elf, base)
		return
	}
	entry := sym("_rt0_cosmo_nt", -1)
	idata := sym("runtime.ntidata", ntidataSize)
	iat := sym("runtime.ntiat", ntiatSize)

	loads := apePayloadLoads(p.elf)
	idataOff := apeVaddrFileOff(loads, idata, ntidataSize, "runtime.ntidata")
	// The IAT must be file-backed too: the NT loader resolves imports by overwriting bytes that exist in the file image.
	apeVaddrFileOff(loads, iat, ntiatSize, "runtime.ntiat")

	// Cross-check the blob's fixed layout against the strings the asm placed.
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

	idataRVA := uint32(idata - base)
	iatRVA := uint32(iat - base)
	// Patch those RVA fields (layout comment in rt0_cosmo_nt_amd64.s).
	binary.LittleEndian.PutUint32(blob[0x00:], idataRVA+ntidataILT)
	binary.LittleEndian.PutUint32(blob[0x0C:], idataRVA+ntidataDLLName)
	binary.LittleEndian.PutUint32(blob[0x10:], iatRVA)
	binary.LittleEndian.PutUint64(blob[ntidataILT:], uint64(idataRVA)+ntidataHintGetProc)
	binary.LittleEndian.PutUint64(blob[ntidataILT+8:], uint64(idataRVA)+ntidataHintLoadLib)

	p.pe = &apePEInfo{
		entryRVA:    uint32(entry - base),
		importsRVA:  idataRVA,
		importsSize: peCosmoImportsSize,
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
// PE32+ image at base peCosmoImageBase whose sections map the payload's
// PT_LOADs (skipping the payload's ELF-header page, which the PE
// headers region occupies virtually), whose entry point is the
// runtime's _rt0_cosmo_nt stub, and whose import directory points at
// the runtime.ntidata blob patched by apePrepareNTBoot.
func writePECosmoAMD64(header []byte, amd *apePayload) {
	info := amd.pe
	loads := apePayloadLoads(amd.elf)
	imageBase := apeImageBase(amd.elf)
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
		if l.vaddr-l.off != imageBase {
			Exitf("APE PE: PT_LOAD %d has vaddr %#x - offset %#x != image base %#x; RVAs would not equal payload offsets", i, l.vaddr, l.off, imageBase)
		}
		if imageBase%0x10000 != 0 {
			Exitf("APE PE: image base %#x is not a multiple of 64K", imageBase)
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

	// .data's SizeOfRawData is p_filesz rounded up to FileAlignment.
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
	within := func(rva, size uint32) bool {
		for _, sect := range sects[1:] {
			if rva >= sect.rva && rva+size <= sect.rva+sect.vsz {
				return true
			}
		}
		return false
	}
	if !within(info.importsRVA, info.importsSize) {
		Exitf("APE PE: import directory [%#x, %#x) is outside .rodata and .data", info.importsRVA, info.importsRVA+info.importsSize)
	}
	if d := sects[2]; info.iatSize != 0 && (info.iatRVA < d.rva || info.iatRVA+info.iatSize > d.rva+d.vsz) {
		Exitf("APE PE: import address table [%#x, %#x) is outside .data", info.iatRVA, info.iatRVA+info.iatSize)
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
	// RELOCS_STRIPPED | EXECUTABLE_IMAGE | LARGE_ADDRESS_AWARE | DEBUG_STRIPPED, matching real Cosmopolitan APEs.
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
	binary.LittleEndian.PutUint64(header[optStart+24:], imageBase)          // ImageBase
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
	// NX_COMPAT | TERMINAL_SERVER_AWARE.
	binary.LittleEndian.PutUint16(header[optStart+70:], 0x8100) // DllCharacteristics
	binary.LittleEndian.PutUint64(header[optStart+72:], 0x800000)
	// rt0_go carves g0's stack as [entry SP - 64K, entry SP].
	binary.LittleEndian.PutUint64(header[optStart+80:], 0x10000)  // SizeOfStackCommit
	binary.LittleEndian.PutUint64(header[optStart+88:], 0x100000) // SizeOfHeapReserve
	binary.LittleEndian.PutUint64(header[optStart+96:], 0x1000)   // SizeOfHeapCommit
	binary.LittleEndian.PutUint32(header[optStart+104:], 0)       // LoaderFlags
	binary.LittleEndian.PutUint32(header[optStart+108:], 16)      // NumberOfRvaAndSizes
	dirStart := optStart + 112
	binary.LittleEndian.PutUint32(header[dirStart+8:], info.importsRVA)
	binary.LittleEndian.PutUint32(header[dirStart+12:], info.importsSize)
	binary.LittleEndian.PutUint32(header[dirStart+96:], info.iatRVA)
	binary.LittleEndian.PutUint32(header[dirStart+100:], info.iatSize)

	// Section table.
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

// It remains for outputs that cannot carry the real header: arm64-only APEs
// (no NT support) and synthetic payloads without a live link or an input head
// (ld tests).
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
			0x00, 0x00, 0x80, 0x52,
			0xC0, 0x03, 0x5F, 0xD6, // ret
		})
	default:
		header[0x200] = 0x31 // xor eax, eax
		header[0x201] = 0xC0
		header[0x202] = 0xC3 // ret
	}
}
