# Enum types

An enum type is a named integer type that states its own members. The type checker knows the whole set. That is what lets it print a value, refuse a switch that misses a member, and refuse a value from outside the set.

## Declaration

```go
type Phase enum uint8 {
	Queued
	Building
	Testing
	Publishing
	Done
	Failed
}
```

```
TypeDef    = identifier [ TypeParameters ] ( Type | EnumType ) .
EnumType   = "enum" Type "{" { EnumMember ";" } "}" .
EnumMember = identifier [ "=" Expression ] [ raw_string_lit ] .
```

`enum` sits where a type declaration puts its type. The type that follows is the underlying type. It is an integer type, and anything else is a compile error. The member list is a Go block, so members are newline-terminated and the lexer inserts the semicolons.

A member declares its value with `=` and its display text with a backquoted tag. Both are optional. A member carrying neither, as every member above does, takes the value after the member before it and prints its own identifier.

## Values

A member with no `=` is one more than the member before it. The first member of a block is zero. An explicit value sets that member, and the count continues from it.

Write values where an outside authority pins them. The kernel ABI pins an errno. A wire protocol pins an opcode. A file format pins a tag byte. Inserting a member into such a block must not renumber the members under it, so each one names its own value:

```go
type Errno enum int32 {
	EPERM   = 1  `operation not permitted`
	ENOENT  = 2  `no such file or directory`
	ESRCH   = 3  `no such process`
	EINTR   = 4  `interrupted system call`
	EIO     = 5  `input/output error`
	ENXIO   = 6  `no such device or address`
	E2BIG   = 7  `argument list too long`
	ENOEXEC = 8  `exec format error`
	EBADF   = 9  `bad file descriptor`
	ECHILD  = 10 `no child processes`
	EAGAIN  = 11 `resource temporarily unavailable`
	ENOMEM  = 12 `out of memory`
	EACCES  = 13 `permission denied`
}
```

Omit values where nothing outside the program reads them. `Phase` above names no value, because which integer stands for `Testing` is the compiler's business. A member inserted into that block renumbers the members under it and changes nothing a program can observe.

## Tags

A tag is the text `String` returns for that member. It is a raw string literal, backquoted, which is how Go spells a trailing string literal that is metadata rather than value. A reader who knows `json:"name"` reads this the same way. An interpreted string sits where the value goes, so `Idle "idle"` on an integer enum reads as an assignment of the wrong type.

Write a tag where the identifier cannot be the text. `ENXIO` needs one. POSIX pins that identifier and keeps it terse on purpose. Nothing derives `no such device or address` from it.

Omit the tag where the identifier is already the word a person wants to read. `Queued` needs none.

## Members are scoped to the type

A member is reached through its type, as `Phase.Queued`. No member is declared in package scope, so no member carries the type name as a hand-written prefix.

A member and a method cannot share a name. So `Phase.Queued` and a method expression stay distinguishable.

## The String method

Every enum type has a `String() string` method, declared implicitly. It returns the member's tag, or the member's identifier where the member carries no tag. For a value equal to no member it returns the type name and the value, `Phase(7)`.

Where members share a value, the member declared first supplies the result.

Declaring `String` on an enum type explicitly is a compile error, because both declarations name one method.

## Switches are exhaustive

A `switch` on an enum value names every member of that type, or it carries a `default`. A missing member is a compile error.

This is what a stated set buys. Adding a member turns red every switch that ignores it.

## Untyped constants do not compare

`state == 3` does not compile. `state == Phase.Idle` does.

A named integer type in Go accepts an untyped constant on either side. An enum accepting one is a stated set in name only.

## Conversion is checked

A constant conversion names a member, or it is a compile error. `Phase(7)` does not compile.

A conversion of a runtime value answers with the comma-ok form:

```go
phase, ok := Phase(n)
```

A value reaching an enum type has therefore passed through a check. The `Phase(7)` that `String` prints is a diagnostic for memory that went wrong, rather than an ordinary result.
