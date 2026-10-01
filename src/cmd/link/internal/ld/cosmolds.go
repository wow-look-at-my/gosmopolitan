// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/sys"
	"fmt"
)

// The linker scripts of a cgo GOOS=cosmo link.

// cosmoBaseAMD64 is the amd64 link base of a cgo link. libcosmo uses 32-bit absolute relocations.
const cosmoBaseAMD64 = 0x400000

// cosmoBaseARM64 is the arm64 link base.
const cosmoBaseARM64 = 0x40000000000

// cosmoLinkerScript returns the linker script for one architecture.
func cosmoLinkerScript(arch sys.ArchFamily) string {
	if arch == sys.AMD64 {
		return fmt.Sprintf(cosmoLdsAMD64, cosmoBaseAMD64)
	}
	return fmt.Sprintf(cosmoLdsARM64, cosmoBaseARM64)
}

// cosmoLdsAMD64 makes PT_LOADs: RX text, R rodata, RW data. Each has vaddr -
// offset equal to the base, which the PE header needs (ape.go). Go text
// comes before _ereal. libcosmo rewrites TLS instructions between _ereal and
// __privileged_start at startup, and Go code must stay out of it.
const cosmoLdsAMD64 = `ENTRY(_start)
PHDRS {
  text PT_LOAD FILEHDR PHDRS FLAGS(5);
  rodata PT_LOAD FLAGS(4);
  data PT_LOAD FLAGS(6);
  tls PT_TLS FLAGS(4);
  stack PT_GNU_STACK FLAGS(6);
}
SECTIONS {
  __executable_start = %#x;
  . = __executable_start + SIZEOF_HEADERS;
  .note.go.buildid : { KEEP(*(.note.go.buildid)) } :text
  .text ALIGN(64) : {
    *go.o(.text .text.*)
    . = ALIGN(16);
    _ereal = .;
    *(.text.real)
    KEEP(*(SORT_BY_NAME(.sort.text.real.*)))
    *(.start)
    KEEP(*(.initprologue))
    KEEP(*(SORT_BY_NAME(.init.*)))
    KEEP(*(.init))
    KEEP(*(.initepilogue))
    *(.plt)
    *(.plt.got)
    *(.iplt)
    *(.text.startup .text.startup.*)
    *(.text.exit .text.exit.*)
    *(.text.unlikely .text.*_unlikely .text.unlikely.*)
    *(SORT_BY_ALIGNMENT(.text.antiquity))
    *(SORT_BY_ALIGNMENT(.text.antiquity.*))
    KEEP(*(.textwindowsprologue))
    *(.text.windows)
    KEEP(*(.textwindowsepilogue))
    *(SORT_BY_ALIGNMENT(.text.modernity))
    *(SORT_BY_ALIGNMENT(.text.modernity.*))
    *(SORT_BY_ALIGNMENT(.text.hot))
    *(SORT_BY_ALIGNMENT(.text.hot.*))
    KEEP(*(.keep.text))
    *(.text .stub .text.*)
    KEEP(*(SORT_BY_NAME(.sort.text.*)))
    *(.test.unlikely)
    *(.test .test.*)
    . = ALIGN(4096);
    __privileged_start = .;
    *(.privileged .privileged.*)
    __privileged_end = .;
    . = ALIGN(4096);
  } :text
  .rodata ALIGN(4096) : {
    *go.o(.rodata)
    KEEP(*(.rodata.pytab.0));
    KEEP(*(.rodata.pytab.1));
    KEEP(*(.rodata.pytab.2));
    *(.rodata .rodata.*)
    *(.ubsan.types)
    *(.ubsan.data)
    __eh_frame_hdr_start_actual = .;
    *(.eh_frame_hdr)
    __eh_frame_hdr_end_actual = .;
    __notices = .;
    KEEP(*(.notice))
    BYTE(0);
    BYTE(10);
    BYTE(10);
    KEEP(*(.idata.ro));
    KEEP(*(SORT_BY_NAME(.idata.ro.*)))
    KEEP(*(.initroprologue))
    KEEP(*(SORT_BY_NAME(.initro.*)))
    KEEP(*(.initroepilogue))
    KEEP(*(SORT_BY_NAME(.sort.rodata.*)))
  } :rodata
  .gopclntab : { KEEP(*(.gopclntab)) } :rodata
  .go.type : { KEEP(*(.go.type)) } :rodata
  .go.func : { KEEP(*(.go.func)) } :rodata
  .typelink : { KEEP(*(.typelink)) } :rodata
  .itablink : { KEEP(*(.itablink)) } :rodata
  .gosymtab : { KEEP(*(.gosymtab)) } :rodata
  .note.ape.ident : { KEEP(*(.note.ape.ident)) } :rodata
  .tdata : {
    _tdata_start = .;
    *(SORT_BY_ALIGNMENT(.tdata))
    *(SORT_BY_ALIGNMENT(.tdata.*))
    _tdata_end = .;
    . = ALIGN(4096);
    _etext = .;
    PROVIDE(etext = .);
  } :rodata :tls
  .tbss : {
    _tbss_start = .;
    *(SORT_BY_ALIGNMENT(.tbss))
    *(SORT_BY_ALIGNMENT(.tbss.*))
    KEEP(*(.fstls))
    _tbss_end = .;
  } :tls
  . = ALIGN(4096);
  .go.buildinfo : { KEEP(*(.go.buildinfo)) } :data
  .go.fipsinfo : { KEEP(*(.go.fipsinfo)) } :data
  .go.module : { KEEP(*(.go.module)) } :data
  .noptrdata : { KEEP(*(.noptrdata)) } :data
  .data : {
    KEEP(*(SORT_BY_NAME(.piro.data.sort.iat.*)))
    KEEP(*(.dataprologue))
    *(.data .data.*)
    *(.gnu_extab)
    *(.gcc_except_table .gcc_except_table.*)
    *(.exception_ranges*)
    *(.PyRuntime)
    *(.subrs)
    KEEP(*(SORT_BY_NAME(.sort.data.*)))
    . += . > 0 ? 1 : 0;
    . = ALIGN(8);
    __got_start = .;
    *(.got)
    __got_end = .;
    *(.got.plt)
    . = ALIGN(8);
    __init_array_start = .;
    KEEP(*(.preinit_array))
    KEEP(*(SORT_BY_INIT_PRIORITY(.init_array.*) SORT_BY_INIT_PRIORITY(.ctors.*)))
    KEEP(*(.init_array))
    KEEP(*(.ctors))
    __init_array_end = .;
    . = ALIGN(8);
    __fini_array_start = .;
    KEEP(*(SORT_BY_INIT_PRIORITY(.fini_array.*) SORT_BY_INIT_PRIORITY(.dtors.*)))
    KEEP(*(.fini_array))
    KEEP(*(.dtors))
    __fini_array_end = .;
    __eh_frame_start = .;
    KEEP(*(.eh_frame))
    *(.eh_frame.*)
    __eh_frame_end = .;
    . = ALIGN(8);
    KEEP(*(SORT_BY_NAME(.piro.relo.sort.*)))
    . = ALIGN(8);
    KEEP(*(SORT_BY_NAME(.piro.data.sort.*)))
    KEEP(*(.piro.pad.data))
    *(.igot.plt)
    KEEP(*(.dataepilogue))
    /* The PE header rounds .data up to 512 bytes, so the file must hold zeros there. */
    . = ALIGN(4096);
    _edata = .;
    PROVIDE(edata = .);
    _ezip = .;
  } :data
  .bss ALIGN(64) : {
    KEEP(*(.bssprologue))
    KEEP(*(SORT_BY_NAME(.piro.bss.init.*)))
    *(.piro.bss)
    KEEP(*(SORT_BY_NAME(.piro.bss.sort.*)))
    __piro_end = .;
    . += . > 0 ? 1 : 0;
    *(SORT_BY_ALIGNMENT(.bss))
    *(SORT_BY_ALIGNMENT(.bss.*))
    *(COMMON)
    KEEP(*(SORT_BY_NAME(.sort.bss.*)))
    KEEP(*(.bssepilogue))
  } :data
  .noptrbss : { *(.noptrbss) } :data
  . = ALIGN(4096);
  _end = .;
  PROVIDE(end = .);
  /DISCARD/ : {
    *(__patchable_function_entries)
    *(.note.gnu.property)
    *(__mcount_loc)
    *(.discard)
    *(.yoink)
    *(.head)
    *(.text.head)
    *(.elf.phdrs)
    *(.pe.header)
    *(.pe.sections)
    *(.macho)
    *(.ape.loader)
    *(.ape.pad.*)
    *(.note.openbsd.ident)
    *(.note.netbsd.ident)
    *(.note.GNU-stack)
  }
}
_tls_size = _tbss_end - _tdata_start;
_tdata_size = _tdata_end - _tdata_start;
_tbss_size = _tbss_end - _tbss_start;
_tbss_offset = _tbss_start - _tdata_start;
_tls_content = (_tdata_end - _tdata_start) + (_tbss_end - _tbss_start);
_tdata_align = ALIGNOF(.tdata);
_tbss_align = ALIGNOF(.tbss);
_tls_align = MAX(64, MAX(ALIGNOF(.tdata), ALIGNOF(.tbss)));
ape_stack_pf = DEFINED(ape_stack_pf) ? ape_stack_pf : 4 | 2;
ape_stack_prot = ((4 & (ape_stack_pf)) >> 2 | (2 & (ape_stack_pf)) | (1 & (ape_stack_pf)) << 2);
ape_stack_vaddr = DEFINED(ape_stack_vaddr) ? ape_stack_vaddr : 0x700000000000;
ape_stack_memsz = DEFINED(ape_stack_memsz) ? ape_stack_memsz : 4 * 1024 * 1024;
__eh_frame_hdr_start = __eh_frame_hdr_end_actual > __eh_frame_hdr_start_actual ? __eh_frame_hdr_start_actual : 0;
__eh_frame_hdr_end = __eh_frame_hdr_end_actual > __eh_frame_hdr_start_actual ? __eh_frame_hdr_end_actual : 0;
`

// cosmoLdsARM64 follows aarch64.lds, with Go's sections named and the base moved.
const cosmoLdsARM64 = `ENTRY(_start)
OUTPUT_ARCH(aarch64)
SECTIONS {
  __executable_start = %#x;
  . = __executable_start + SIZEOF_HEADERS;
  .note.go.buildid : { KEEP(*(.note.go.buildid)) }
  .init : {
    *(.start)
    KEEP(*(.initprologue))
    KEEP(*(SORT_NONE(.init)))
    KEEP(*(.initepilogue))
  } =0x1f2003d5
  .plt : ALIGN(16) {
    *(.plt)
    *(.iplt)
  }
  .text : {
    *go.o(.text .text.*)
    *(.text.unlikely .text.*_unlikely .text.unlikely.*)
    *(.text.antiquity .text.antiquity.*)
    *(.text.exit .text.exit.*)
    *(.text.startup .text.startup.*)
    *(.text.hot .text.hot.*)
    *(.text.modernity .text.modernity.*)
    *(.text .stub .text.* .gnu.linkonce.t.*)
    *(.gnu.warning)
  } =0x1f2003d5
  .fini : {
    KEEP(*(SORT_NONE(.fini)))
  } =0x1f2003d5
  . += CONSTANT(MAXPAGESIZE);
  .privileged : {
    __privileged_start = ABSOLUTE(.) & -CONSTANT(MAXPAGESIZE);
    *(.privileged*)
  } =0x1f2003d6
  .rodata : {
    *go.o(.rodata)
    KEEP(*(.rodata.pytab.0));
    KEEP(*(.rodata.pytab.1));
    KEEP(*(.rodata.pytab.2));
    KEEP(*(SORT_BY_NAME(.sort.rodata.*)))
    *(.rodata .rodata.* .gnu.linkonce.r.*)
    *(.ubsan.types)
    *(.ubsan.data)
  }
  .gopclntab : { KEEP(*(.gopclntab)) }
  .go.type : { KEEP(*(.go.type)) }
  .go.func : { KEEP(*(.go.func)) }
  .typelink : { KEEP(*(.typelink)) }
  .itablink : { KEEP(*(.itablink)) }
  .gosymtab : { KEEP(*(.gosymtab)) }
  .note.ape.ident : { KEEP(*(.note.ape.ident)) }
  .notice : {
    __notices = .;
    KEEP(*(.notice))
    BYTE(0);
    BYTE(10);
    BYTE(10);
  }
  .eh_frame_hdr : {
    *(.eh_frame_hdr)
    *(.eh_frame_entry .eh_frame_entry.*)
  }
  __eh_frame_hdr_start = SIZEOF(.eh_frame_hdr) > 0 ? ADDR(.eh_frame_hdr) : 0;
  __eh_frame_hdr_end = SIZEOF(.eh_frame_hdr) > 0 ? . : 0;
  .gcc_except_table : ONLY_IF_RO {
    *(.gcc_except_table .gcc_except_table.*)
  }
  .gnu_extab : ONLY_IF_RO {
    *(.gnu_extab*)
  }
  .exception_ranges : ONLY_IF_RO {
    *(.exception_ranges*)
  }
  __etext = .;
  _etext = .;
  PROVIDE(etext = .);
  . += CONSTANT(MAXPAGESIZE);
  . = DATA_SEGMENT_ALIGN(CONSTANT(MAXPAGESIZE), CONSTANT(COMMONPAGESIZE));
  .gnu_extab : ONLY_IF_RW {
    *(.gnu_extab)
  }
  .gcc_except_table : ONLY_IF_RW {
    *(.gcc_except_table .gcc_except_table.*)
  }
  .exception_ranges : ONLY_IF_RW {
    *(.exception_ranges*)
  }
  .tdata : {
    _tdata_start = .;
    __tdata_start = .;
    *(.tdata .tdata.* .gnu.linkonce.td.*)
    _tdata_end = .;
  }
  .tbss : {
    _tbss_start = .;
    *(.tbss .tbss.* .gnu.linkonce.tb.*)
    *(.tcommon)
    _tbss_end = .;
  }
  .init_array : {
    __init_array_start = .;
    KEEP(*(.preinit_array))
    KEEP(*(SORT_BY_INIT_PRIORITY(.init_array.*) SORT_BY_INIT_PRIORITY(.ctors.*)))
    KEEP(*(.init_array))
    KEEP(*(.ctors))
    __init_array_end = .;
  }
  .fini_array : {
    __fini_array_start = .;
    KEEP(*(SORT_BY_INIT_PRIORITY(.fini_array.*) SORT_BY_INIT_PRIORITY(.dtors.*)))
    KEEP(*(.fini_array EXCLUDE_FILE(*crtbegin.o *crtbegin?.o *crtend.o *crtend?.o ) .dtors))
    __fini_array_end = .;
  }
  .data.rel.ro : {
    KEEP(*(SORT_BY_NAME(.piro.relo.sort.*)))
    *(.data.rel.ro.local* .gnu.linkonce.d.rel.ro.local.*)
    *(.data.rel.ro .data.rel.ro.* .gnu.linkonce.d.rel.ro.*)
  }
  .got : {
    *(.got)
    *(.igot)
  }
  . = DATA_SEGMENT_RELRO_END(24, .);
  .got.plt : {
    *(.got.plt)
    *(.igot.plt)
  }
  .go.buildinfo : { KEEP(*(.go.buildinfo)) }
  .go.fipsinfo : { KEEP(*(.go.fipsinfo)) }
  .go.module : { KEEP(*(.go.module)) }
  .noptrdata : { KEEP(*(.noptrdata)) }
  .data : {
    __data_start = .;
    KEEP(*(SORT_BY_NAME(.piro.data.sort.*)))
    *(.data .data.* .gnu.linkonce.d.*)
    KEEP(*(SORT_BY_NAME(.sort.data.*)))
    SORT(CONSTRUCTORS)
  }
  _edata = .;
  PROVIDE(edata = .);
  . = .;
  __bss_start = .;
  __bss_start__ = .;
  .bss : {
    *(.dynbss)
    *(.bss .bss.* .gnu.linkonce.b.*)
    KEEP(*(SORT_BY_NAME(.piro.bss.sort.*)))
    *(COMMON)
  }
  .noptrbss : { *(.noptrbss) }
  . = ALIGN(CONSTANT(COMMONPAGESIZE));
  _bss_end__ = .;
  __bss_end__ = .;
  . = ALIGN(64 / 8);
  __end__ = .;
  _end = .;
  PROVIDE(end = .);
  . = DATA_SEGMENT_END(.);
  /DISCARD/ : {
    *(__patchable_function_entries)
    *(.GCC.command.line)
    *(.note.GNU-stack)
    *(.gnu_debuglink)
    *(.text.windows)
    *(.gnu.lto_*)
    *(.eh_frame)
    *(.idata.*)
    *(.yoink)
    *(.head)
  }
}
ape_stack_vaddr = DEFINED(ape_stack_vaddr) ? ape_stack_vaddr : 0x700000000000;
ape_stack_memsz = DEFINED(ape_stack_memsz) ? ape_stack_memsz : 8 * 1024 * 1024;
ape_stack_prot = 1 | 2;
_tls_size = _tbss_end - _tdata_start;
_tdata_size = _tdata_end - _tdata_start;
_tbss_size = _tbss_end - _tbss_start;
_tbss_offset = _tbss_start - _tdata_start;
_tls_content = (_tdata_end - _tdata_start) + (_tbss_end - _tbss_start);
_tdata_align = ALIGNOF(.tdata);
_tbss_align = ALIGNOF(.tbss);
_tls_align = MAX(64, MAX(ALIGNOF(.tdata), ALIGNOF(.tbss)));
`
