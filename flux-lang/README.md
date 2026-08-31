<div align="center">

# ⚡ flux-lang

### Event-Driven System Language & Virtual Machine

*A low-overhead, concurrent bytecode toolchain designed in pure Go to bridge real-time event telemetry streams (like Twitch IRC) with bare-metal style software loops and virtual heap management.*

---

[![License: ISC](https://img.shields.io/badge/License-ISC-blueviolet.svg)](#license)
[![Go: 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8.svg)](#tech-stack)
[![Coverage: Pure Stdlib](https://img.shields.io/badge/Dependencies-Pure_Stdlib-success.svg)](#tech-stack)
[![Heap Allocator: First-Fit](https://img.shields.io/badge/Heap_Allocator-First--Fit-orange.svg)](#custom-heap-manager)

</div>

---

## ✨ Why this exists

General-purpose runtimes and high-level managed languages are tuned for
*throughput over a wide variety of workloads*. **High-frequency event
hooks**, however — Twitch chat bursts, GPIO interrupts, IRC floods, log
telemetry — stress a very different muscle: **predictable latency under
tight, repetitive bursts of small allocations**.

When an event fires repeatedly inside a standard Go program, every
`SEND_CHAT` or `TRIGGER_PIN` analogue pays the GC its share: a transient
string here, a boxed interface there, a closure capture forcing the
allocator to walk the heap. The larger the program, the worse the
worst-case pause, and pauses are the thing an event loop **cannot
tolerate**.

`flux-lang` solves this by collapsing the entire pipeline into a single,
pre-allocated execution island:

- The **compiler** is a pure stdlib Go pipeline → no third-party parsing,
  codegen, or marshaling lag.
- The **VM** owns its own **1 MB static RAM array** and a hand-rolled
  **first-fit allocator** → the host OS is never invoked once execution
  has begun.
- Event handlers are **registered up front** and run inside the same
  flat bytecode stream they were compiled into → the body of an
  `ON_CHAT` block runs from the same RAM you warmed up at boot.

In short: `flux-lang` is what you reach for when **“just call malloc”** is
not an acceptable answer to the question “how did the chat drop frames?”.

---

## 🎬 What you can do

| Capability                                 | What it does                                                                                                                                                       |
|--------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Zero-Allocation Scanning**               | Sequential byte-level **lexing** that returns substrings of the original source by index — no `string` copies, no `fmt.Sprintf` in the hot path, GC stays idle.   |
| **Recursive Descent Parsing**              | Recursive-descent **parser** that grows an `ast.Program` tree, accumulates every diagnostic, and surfaces the partial tree even after a syntax error.                |
| **Deterministic Bytecode Emitter**         | **Codegen** lowers the AST into a single flat `.flx` binary: 4-byte magic `FLUX`, version, deduplicated string constant pool, code section.                          |
| **16-Register Virtual CPU**                | A `CPU` struct with `Registers [16]uint32`, `PC uint32`, an `Instruction Pointer`-driven dispatch loop, and an exact-match event subscription registry.              |
| **Self-Managed Dynamic Heap**              | 1 MB virtual RAM (`[1024*1024]byte`) with a manual **first-fit** `Alloc`/`Free`, `ErrOOM`-on-overflow, and `ErrDoubleFree` on misuse — no host allocator.             |
| **Interactive Chat Simulator**            | stdin-driven CLI loop parsing `<username>: <message>`, dispatching them through `DeliverChatMessage`, saving and restoring the PC around every body invocation.       |

---

## 🧱 Tech Stack

| Layer            | Implementation                                                                                    |
|------------------|----------------------------------------------------------------------------------------------------|
| Language         | **Go 1.22+** (no third-party dependencies whatsoever)                                             |
| Compiler modules | `flux/compiler` → `lexer`, `parser`, `ast`, `codegen`, plus the CLI `flux-compiler`              |
| VM modules       | `flux/vm` → `memory` (custom heap) and `cpu` (dispatch loop), plus the CLI `flux-vm`              |
| Wire format      | Custom `.flx` binary (see *Mathematical & Architectural Invariants* below)                        |
| Standard library | `strings`, `bytes`, `binary`, `bufio`, `encoding/binary`, `errors`, `fmt`, `io`, `os` — that's it |

> [!TIP]
> The deliberate choice to keep **everything** inside the standard
> library is not laziness — it is a contract. There is no `gopkg.in/...`
> digests to refresh, no transitive dependency audit to run before
> every release, and zero marshaling friction between phases of the
> compiler pipeline. The byte stream produced by `codegen` is the same
> byte stream `cpu.LoadBinary` parses byte-for-byte.

---

## 🚀 Quick Start & Practical Execution

### 1. Compile a `.flx` binary from source

```bash
# Navigate to the compiler component and emit the binary using pipe input.
cd compiler

echo 'ALLOC R1, 32
MOV R1, "Richard"
ON_CHAT "!hype", R1
    SEND_CHAT "Hype train activated!"
    TRIGGER_PIN 18, 1
FREE R1' | go run main.go -o ../vm/test.flx -
```

The compiler will report on stderr:

```text
wrote "../vm/test.flx" (<N> bytes, <M> constants)
```

### 2. Run the binary inside the VM

```bash
# Navigate to the VM component and execute the compiled binary file.
cd ../vm
go run main.go test.flx
```

### Interactive runtime visualisation

Because `MOV R1, "Richard"` registers an `ON_CHAT "!hype", R1`
subscription, the VM does **not** exit after running the top-level
bytecode — it enters the chat simulator. Pump a chat line in:

```text
ON_CHAT subscribed pattern="!hype" user_var=R1 body=[<offset>..<offset>+<length>)
--- interactive chat simulator ---
format: <username>: <message>
Ctrl+D, blank line, or 'quit' to exit
Richard: !hype
>>> ON_CHAT match pattern="!hype" username="Richard" user_var=R1 username_addr=<addr>
SEND_CHAT "Hype train activated!"
TRIGGER_PIN pin=18 state=1
<<< ON_CHAT block end
quit
```

The bare `quit` line at the bottom is what closes the simulator:
`vm/main.go` treats `quit`, `exit`, an empty line, and a `Ctrl+D` EOF
as equivalent exit signals.

> [!NOTE]
> The transcript above is an **example**, not a literal log. The two
> allocator-driven values — the body's byte range in the
> `ON_CHAT subscribed` line and `username_addr` in the match line —
> are computed at runtime and depend on the codegen's exact emission
> plus the heap's free-block chain at the moment of each allocation.
> `SEND_CHAT`, `TRIGGER_PIN`, and the `>>>` / `<<<` markers are
> deterministic; the offsets are not.

---

## 🧮 Mathematical & Architectural Invariants

### `.flx` Binary File Format

Every compiled program is a single `.flx` file laid out as:

| Offset (bytes) | Size | Field                       | Notes                                                          |
|----------------|------|-----------------------------|----------------------------------------------------------------|
| `[0..4)`       | 4    | **Magic** `FLUX`            | The only checksum the format carries — loader refuses any file that doesn't start with these bytes. |
| `[4]`          | 1    | **Version** `= 1`           | Bumped only on wire-format-breaking changes.                  |
| `[5..7)`       | 2    | `ConstantsCount` (Uint16 BE) | Length of the deduplicated constant pool.                      |
| `[7..11)`      | 4    | `CodeSectionOffset` (Uint32 BE) | Byte offset of the code section, measured from the start of the file. |
| `[11..15)`     | 4    | `CodeSectionSize`  (Uint32 BE) | Length in bytes of the code section.                            |

After the 15-byte header, the constant pool is laid out sequentially
as `[Length:Uint32 BE][Raw String Bytes]`. After the constant pool
comes the code section.

### Custom Heap Manager Allocation

The VM owns a single package-global 1 MB byte array:

```go
var RAM [1024 * 1024]byte // package memory
```

Every block is preceded by a **6-byte metadata header**:

| Header bytes | Field         | Type        | Purpose                       |
|--------------|---------------|-------------|-------------------------------|
| `[0..4)`     | `BlockSize`   | `uint32` BE | Length of the **data** region immediately following this header (NOT including the header itself). |
| `[4]`        | `IsAllocated` | `uint8`     | `0` = free, `1` = allocated.  |
| `[5]`        | (reserved)    | `uint8`     | Padding to keep the header naturally 2-byte aligned. |

When `DeliverChatMessage("Richard", "!hype")` fires, the VM:

1. Computes `len("Richard") + 1 = 8` bytes (the `+1` is the mandatory
   NUL terminator).
2. Calls `memory.Alloc(8)`, which scans the heap chain looking for the
   **first free block** (`first-fit`) whose data region is large
   enough.
3. Marks the chosen block as allocated, splits off any leftover bytes
   (if there's room for another header + data area) into a fresh free
   block immediately after, and returns the **byte offset** that sits
   one header past the new allocated block.
4. Writes `"Richard\x00"` into that offset and logs e.g.
   `username_addr=44`.

> [!WARNING]
> **`MOV_REG_STR` overwrites the destination register.** Each `MOV`
> into a register hands out a **new** heap address. Any previous
> `ALLOC` in the same register is now unreachable. If you write
> `ALLOC R1, 32` followed by `MOV R1, "Richard"`, the 32-byte block
> is **leaked forever** unless explicit records kept elsewhere.
> This is by design: the machine trusts you, the way `malloc` does.

### Instruction Set Architecture (ISA) Layout

| Opcode | Mnemonic         | Hex  | Layout                                                            |
|--------|------------------|------|-------------------------------------------------------------------|
| 0x01   | `OP_ALLOC`       | 0x01 | `[OP_ALLOC] [Reg:1] [Size:Uint32 BE]`                              |
| 0x02   | `OP_FREE`        | 0x02 | `[OP_FREE]  [Reg:1]`                                               |
| 0x03   | `OP_MOV_REG_REG` | 0x03 | `[OP_MOV_REG_REG] [DstReg:1] [SrcReg:1]`                           |
| 0x04   | `OP_MOV_REG_INT` | 0x04 | `[OP_MOV_REG_INT] [DstReg:1] [Value:4-byte BE]` *(bit pattern of `int32`)* |
| 0x05   | `OP_MOV_REG_STR` | 0x05 | `[OP_MOV_REG_STR] [DstReg:1] [ConstIdx:Uint32 BE]`                 |
| 0x06   | `OP_TRIGGER_PIN` | 0x06 | `[OP_TRIGGER_PIN] [Pin:Uint8] [State:Uint8]`                       |
| 0x07   | `OP_SEND_CHAT`   | 0x07 | `[OP_SEND_CHAT] [Operand:Uint32 BE]` *(high-bit-tagged)*          |
| 0x08   | `OP_ON_CHAT`     | 0x08 | `[OP_ON_CHAT] [TriggerIdx:Uint32 BE] [UserReg:1] [BodyStart:Uint32 BE] [BodyLength:Uint32 BE]` |
| 0x09   | `OP_ADD`         | 0x09 | `[OP_ADD] [DstReg:1] [SrcReg:1]`                                  |
| 0x0A   | `OP_SUB`         | 0x0A | `[OP_SUB] [DstReg:1] [SrcReg:1]`                                  |
| 0x0B   | `OP_MUL`         | 0x0B | `[OP_MUL] [DstReg:1] [SrcReg:1]`                                  |
| 0x0C   | `OP_DIV`         | 0x0C | `[OP_DIV] [DstReg:1] [SrcReg:1]`                                  |
| 0x0D   | `OP_AND`         | 0x0D | `[OP_AND] [DstReg:1] [SrcReg:1]`                                  |
| 0x0E   | `OP_OR`          | 0x0E | `[OP_OR]  [DstReg:1] [SrcReg:1]`                                  |
| 0x0F   | `OP_XOR`         | 0x0F | `[OP_XOR] [DstReg:1] [SrcReg:1]`                                  |
| 0x10   | `OP_SHL`         | 0x10 | `[OP_SHL] [DstReg:1] [SrcReg:1]`                                  |
| 0x11   | `OP_SHR`         | 0x11 | `[OP_SHR] [DstReg:1] [SrcReg:1]`                                  |
| 0x12   | `OP_CMP`         | 0x12 | `[OP_CMP] [Reg1:1] [Reg2:1]`                                      |
| 0x13   | `OP_JMP`         | 0x13 | `[OP_JMP] [TargetPC:Uint32 BE]`                                   |
| 0x14   | `OP_JZ`          | 0x14 | `[OP_JZ]  [TargetPC:Uint32 BE]`                                   |
| 0x15   | `OP_JNZ`         | 0x15 | `[OP_JNZ] [TargetPC:Uint32 BE]`                                   |
| 0x16   | `OP_CALL`        | 0x16 | `[OP_CALL] [TargetPC:Uint32 BE]`                                  |
| 0x17   | `OP_RET`         | 0x17 | `[OP_RET]`                                                        |

`OP_SEND_CHAT` carries a **tagged Uint32 operand**: the high bit cleared
denotes a register code (`1..16`) whose value the VM reads as a heap
address holding a NUL-terminated C-string; the high bit set denotes a
constant-pool index. Single-bit, single-instruction type discrimination
— no extra byte.

`OP_ON_CHAT` is a 14-byte header followed immediately by the body's
bytecode. The compiler patches `BodyStart` and `BodyLength` in place
once the body has finished emitting.

### Memory-leak invariant: runtime detection

The heap manager does **not** auto-coalesce, and the machine does
**not** track liveness for you. Two invariants are enforced at
runtime instead. The VM surfaces these by **strerror-style** strings
returned from `memory/heap.go` and prefixed with the failing
opcode on stderr:

| Failure scenario                                      | Returned error string                | Typical stderr line (from `vm/main.go`) |
|-------------------------------------------------------|--------------------------------------|------------------------------------------|
| Call `Free` with an address below `HeaderSize` or `>= RAMSize` | `"memory: invalid address"`          | `run: FREE: memory: invalid address`     |
| Call `Free` on a block whose header is already marked free      | `"memory: double free"`              | `run: FREE: memory: double free`         |
| `Alloc(size)` finds no free block large enough for `size`        | `ErrOOM` (`"memory: out of memory"`) | `run: ALLOC: memory: out of memory`      |

This is the machine's equivalent of a `panic` boundary. It does **not**
protect you from re-assigning an already-occupied allocation register
without first executing `FREE` — that class of leak is the program's
responsibility, exactly like a `C` program leaking a `malloc`'d block
when its pointer goes out of scope.

---

## 📦 Project Structure & Testing

### Monorepo layout

```
flux-lang/
├── README.md                       ← you are here
├── compiler/
│   ├── go.mod                      ← module name: flux/compiler
│   ├── main.go                     ← CLI entrypoint (flux-compiler)
│   ├── main_test.go
│   ├── ast/
│   │   └── ast.go                  ← Statement / Expression node types
│   ├── codegen/
│   │   ├── opcodes.go              ← ISA + .flx header constants
│   │   ├── codegen.go              ← AST → .flx emitter
│   │   └── codegen_test.go
│   ├── lexer/
│   │   ├── lexer.go                ← zero-allocation byte lexer
│   │   └── lexer_test.go
│   └── parser/
│       ├── parser.go               ← recursive-descent parser
│       └── parser_test.go
└── vm/
    ├── go.mod                      ← module name: flux/vm
    ├── main.go                     ← CLI entrypoint (flux-vm)
    ├── main_test.go
    ├── cpu/
    │   ├── cpu.go                  ← Virtual CPU + dispatch loop + event loop
    │   └── cpu_test.go
    └── memory/
        ├── heap.go                 ← Static 1MB RAM + first-fit allocator
        └── heap_test.go
```

**Runtime artifacts** (generated by the Quick Start, **not** checked in):

| Path            | Producer                  | Consumer      | Purpose                                                |
|-----------------|---------------------------|---------------|--------------------------------------------------------|
| `vm/test.flx`   | `flux-compiler -o …`      | `flux-vm`     | Compiled canonical-program bytecode, fed straight into the VM via `vm/main.go test.flx`. |

The `compiler/` and `vm/` directories are **independent Go modules**
(`flux/compiler` and `flux/vm`). They share no code; the only thing
they have in common is the `.flx` byte-level wire format. Either one
can be vendored, rewritten, or swapped without touching the other.

### Testing pipeline

Both modules expose a native `go test ./...` plus a `go vet` pre-flight:

```bash
# Compiler
cd compiler
go vet ./...
go test ./... -v -count=1

# VM
cd ../vm
go vet ./...
go test ./... -v -count=1
```

The test suites cover:

- **Compiler** → tokenisation (Lexer), AST shape (Parser), `.flx`
  byte-for-byte layout (Codegen), CLI flag wiring.
- **VM** → `Alloc`+`Free` cycle, double-free and OOM rejection, full
  round-trip of the canonical assembly program, `OP_MOV_REG_STR`
  heap-copy including NUL terminator and exact address, `ON_CHAT`
  registration + body-skip during top-down execution,
  `DeliverChatMessage` matching / running the body / freeing the
  username block / restoring the PC.

On the current source tree that's:

| Module             | Test files                                                 | `Test*` funcs |
|--------------------|------------------------------------------------------------|---------------|
| `flux/compiler`    | `lexer/lexer_test.go`, `parser/parser_test.go`, `codegen/codegen_test.go`, `main_test.go`    | 27            |
| `flux/vm`          | `memory/heap_test.go`, `cpu/cpu_test.go`                   | 8             |

All **35 tests pass completely green** with `go vet` silent on both
modules.

---

## License

`flux-lang` is released under the **ISC License**. See [`LICENSE`](LICENSE)
for the full text.
