/* One PT_LOAD holding headers, code and strings. Nothing else in the file. */
ENTRY(_start)
PHDRS { all PT_LOAD FLAGS(5); }
SECTIONS {
	. = 0x400000 + SIZEOF_HEADERS;
	.text : { *(.text .text.*) *(.rodata .rodata.*) } :all
	/DISCARD/ : { *(.comment) *(.note.*) *(.eh_frame*) *(.data*) *(.bss*) }
}
