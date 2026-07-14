# flux-lang

> An event-driven language and virtual machine written in Go, designed to
> glue **Twitch chat events** to **bare-metal-style hardware operations**.

`flux-lang` is a small, deliberately-vertical project: its compiler and
runtime are both written in Go using only the standard library, so the
toolchain is reproducible, fast, and easy to audit.

The project is split into two deliverables:

| Path       | Purpose                                                                                  |
|------------|------------------------------------------------------------------------------------------|
| `compiler/`| The lexer, parser, and CLI for the `flux` language.                      |
| `vm/`      | A stack-based virtual machine that executes the compiler's bytecode.                     |

## Repository layout

```
flux-lang/
├── README.md
├── compiler/
│   ├── go.mod                ← module name: flux/compiler
│   ├── main.go               ← CLI entrypoint
│   └── lexer/
│       ├── lexer.go          ← zero-allocation lexer
│       └── lexer_test.go     ← tokenization test suite
└── vm/                       ← (next milestone)
```

## Quick start

```sh
# 1. Run the lexer test suite.
cd compiler
go test ./...

# 2. Tokenize a source file (or pipe via stdin).
go run . path/to/source.fx
```

## Design principles

1. **Zero-allocation lexer hot path.** Identifiers, numbers, and string
   literals are returned as sub-slices of the original source buffer — no
   `string` allocations, no `fmt.Sprintf` in `NextToken`.
2. **Strict, strongly-typed tokens.** Every keyword has a unique
   `TokenType` constant, so downstream stages never have to compare literal
   strings to decide what they received.
3. **Standard library only.** No third-party dependencies in the compiler
   keep the build fast and the surface area trivial to audit.
4. **ASCII by design.** The language keywords are ASCII, so the lexer
   operates on bytes and avoids `unicode`/`utf8` decode costs on the hot
   path.
 