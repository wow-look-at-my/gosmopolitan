/* One PT_LOAD holding headers, code and strings. Nothing else in the file. */
ENTRY(_start)
PHDRS { all PT_LOAD FLAGS(5); }
SECTIONS {
	/* Clear of every payload: a cgo image sits at 0x400000, a Go one at 4 GiB or 4 TiB.
	   A non-PIC amd64 build keeps its addresses below 4 GiB. */
	. = 0xC0000000 + SIZEOF_HEADERS;
	.text : { *(.text .text.*) *(.rodata .rodata.*) } :all
	/DISCARD/ : { *(.comment) *(.note.*) *(.eh_frame*) *(.data*) *(.bss*) }
}
