# Enum types

A proposal, derived from C++'s `enum class` and cut down to what Go can carry. Nothing here is implemented. The branch that carries this doc has a smaller, earlier shape in the compiler. The last section describes it.

## What Go has now

```go
type ConnState int

const (
	StateNew ConnState = iota
	StateActive
	StateIdle
	StateClosed
)

//go:generate stringer -type=ConnState
```

Each defect below is a reason `stringer` exists.

The type and its values are separate declarations that nothing ties together. A constant added to the block is a member. A constant of the same type declared in another file is also a member. Nothing states the set.

The names live in package scope. So every one of them is hand-prefixed with the type name. The prefix is the programmer paying for a scope the language did not give.

Printing needs a generator, a committed generated file, and a build step that regenerates it. That step is the reason this document exists.

A `switch` over the type is unchecked. A member added today falls through every switch written yesterday, in silence.

## What C++ has

```cpp
enum class ConnState : uint8_t {
    New = 0,
    Active,
    Idle,
    Closed,
};
```

Members are scoped to the type, so `ConnState::New` needs no prefix. The underlying type is explicit. The type and its members are one declaration. The set is therefore stated. C++ does not fix printing. Its exhaustiveness check is a warning under `-Wswitch` rather than a rule.

## Proposed syntax

```go
type ConnState enum uint8 {
	New
	Active
	Idle
	Closed
}
```

```
TypeDef    = identifier [ TypeParameters ] ( Type | EnumType ) .
EnumType   = "enum" Type "{" { EnumMember ";" } "}" .
EnumMember = identifier [ "=" Expression ] [ raw_string_lit ] .
```

Read it against C++ one piece at a time. `enum` moves to where a Go type declaration puts its type. `class` disappears, because Go has no unscoped variant to distinguish it from. `: uint8_t` becomes the ordinary type that follows. The member list is a Go block, so members are newline-terminated and the lexer inserts the semicolons.

A member carries no string in the common case. Its display text is its own identifier. Write a tag where the identifier cannot be the text:

```go
type Method enum uint8 {
	Get  `GET`
	Post `POST`
}

type Errno enum int32 {
	NotFound = 404 `not found`
	Conflict = 409 `conflict`
}
```

The tag is the part that retires `stringer`. It is backquoted, and a struct field tag is the reason. Go already spells "trailing string literal that is metadata rather than value" that way. A reader who knows `json:"name"` therefore reads this without being told. An interpreted string sits where C++ puts the value, so `New "new"` on a `uint8` enum reads as an assignment of the wrong type. The `=` takes the value. The tag takes the text. Neither can be read as the other.

## Semantics

**The underlying type is an integer type.** Anything else is a compile error.

**Members are scoped to the type.** Write `ConnState.New`, never a package-scope `StateNew`. A member and a method cannot share a name, so `ConnState.New` and a method expression stay distinguishable. The hand-written prefix on every enum member disappears.

**Values increase by themselves.** A member with no `=` is one more than the member before it. The first member is zero. An explicit value resets the count. So `iota` is not needed and not used.

**Every enum type has a `String() string` method**, declared implicitly. It returns the member's tag, or the member's identifier where the declaration carries no tag. For a value equal to no member it returns the type name and the value, `ConnState(7)`. Declaring `String` explicitly is an error, because both declarations name one method.

**A `switch` on an enum is exhaustive, or it carries a `default`.** A missing member is a compile error, not a warning. C++ declined to make this a rule. It is what makes a closed set worth writing down: adding a member turns every switch that ignores it red.

**An enum does not compare against untyped constants.** `state == 3` does not compile. `state == ConnState.Idle` does. A named integer type in Go accepts an untyped constant on either side. An enum that accepts one is a closed set in name only.

**Conversion from the underlying type is checked.** A constant conversion out of range, `ConnState(7)`, is a compile error. A runtime conversion answers with the comma-ok form:

```go
state, ok := ConnState(n)
```

Every value is otherwise one cast away from being garbage. The `ConnState(7)` that `String` prints then stops being a diagnostic and becomes a routine result.

## What this costs

The parser gains one production. types2 gains a scope per enum type and the walk that assigns values. It also gains an error for a non-integer underlying type, an error for a non-exhaustive switch, and an error for an untyped comparison. The back end gains one generated method body per enum type. That body is a switch over the members, with a call to `runtime.enumString` for every other value.

The exhaustiveness rule is the expensive one. It runs over every switch statement rather than every declaration. It also has to decide what covers means for a switch that falls through, and for one whose cases are not all constants.

## What is in the tree today

An earlier, smaller shape, which this proposal supersedes:

```go
type ConnState enum uint8

const (
	StateNew ConnState = iota "new"
	StateIdle                 "idle"
)
```

`enum` goes after the type name, with no member block. The members stay an ordinary `const` block with `iota`. Each constant optionally carries its text. It buys the `String` method and nothing else. Members stay in package scope. `iota` stays. Nothing states the set, and `switch` stays unchecked. It was the smallest change that retires `stringer`. It is not a good enum. The parser work it did is reusable rather than final.
