Eine saubere ARCHITECTURE.md ist der Standard für systemnahe Open-Source-Projekte, um Speichermodelle, Wire-Formats und Subsysteme exakt zu definieren.

Hier ist eine maßgeschneiderte Vorlage, die du direkt als ARCHITECTURE.md im Root-Verzeichnis von flux-lang anlegen kannst:

Markdown
# 🏛️ flux-lang Architecture & System Design

This document details the architectural invariants, internal subsystems, memory layouts, and wire formats of the `flux-lang` toolchain and virtual machine.

---

## 1. High-Level System Overview

The project is structured into two completely decoupled Go modules communicating solely via the flat `.flx` binary format.

+----------------------------------------------------------------+
|                        COMPILER PIPELINE                       |
|  [Source Code]                                                 |
|        │                                                       |
|        ▼ (Zero-Alloc Lexer)                                    |
|    [Tokens]                                                    |
|        │                                                       |
|        ▼ (Recursive Descent Parser)                            |
|     [AST]                                                      |
|        │                                                       |
|        ▼ (Codegen & String Deduplication)                      |
|  [.flx Binary Stream]                                          |
+─────────────────────────────────┬──────────────────────────────+
│
▼
+─────────────────────────────────┴──────────────────────────────+
|                     VIRTUAL MACHINE RUNTIME                    |
|                                                                |
|   ┌──────────────────────────┐    ┌─────────────────────────┐  |
|   │     Virtual CPU Core     │    │   Static Heap Manager   │  |
|   │ ──────────────────────── │    │ ─────────────────────── │  |
|   │ • 16x uint32 Registers   │    │ • 1 MB Flat Memory      │  |
|   │ • Program Counter (PC)   │◄──►│ • 6-Byte Block Headers  │  |
|   │ • Event Dispatch Table   │    │ • First-Fit Allocation  │  |
|   │ • Tagged Instruction Dec │    │ • Manual Alloc / Free   │  |
|   └──────────────────────────┘    └─────────────────────────┘  |
+----------------------------------------------------------------+


---

## 2. Binary Wire Format (`.flx`)

Every compiled `.flx` binary contains a 15-byte header, followed by a deduplicated constant pool, and concludes with raw bytecode instructions.

### 2.1 File Header Layout (15 Bytes)

| Byte Offset | Size (Bytes) | Field Name | Type / Encoding | Description |
| :--- | :--- | :--- | :--- | :--- |
| `0x00..0x03` | 4 | `Magic` | ASCII (`FLUX`) | File signature identifier |
| `0x04` | 1 | `Version` | `uint8` | Format version (default: `1`) |
| `0x05..0x06` | 2 | `ConstantsCount` | `uint16` Big-Endian | Number of entries in constant pool |
| `0x07..0x0A` | 4 | `CodeOffset` | `uint32` Big-Endian | Byte offset where bytecode begins |
| `0x0B..0x0E` | 4 | `CodeSize` | `uint32` Big-Endian | Total length of code section in bytes |

### 2.2 Constant Pool
Entries are packed sequentially:
`[Length: uint32 BE][Raw UTF-8 String Bytes]` (without null-terminator in the file).

---

## 3. Instruction Set Architecture (ISA)

The virtual CPU implements a single-byte opcode format with big-endian immediate operands:

| Opcode | Mnemonic | Encoding Layout | Behavior / Semantics |
| :--- | :--- | :--- | :--- |
| `0x01` | `OP_ALLOC` | `[0x01][Reg:1][Size:4 BE]` | `Registers[Reg] = Heap.Alloc(Size)` |
| `0x02` | `OP_FREE` | `[0x02][Reg:1]` | `Heap.Free(Registers[Reg])` |
| `0x03` | `OP_MOV_REG_REG` | `[0x03][Dst:1][Src:1]` | `Registers[Dst] = Registers[Src]` |
| `0x04` | `OP_MOV_REG_INT` | `[0x04][Dst:1][Val:4 BE]` | `Registers[Dst] = Val` |
| `0x05` | `OP_MOV_REG_STR` | `[0x05][Dst:1][ConstIdx:4 BE]` | Copies string to Heap, writes pointer to `Registers[Dst]` |
| `0x06` | `OP_TRIGGER_PIN` | `[0x06][Pin:1][State:1]` | Simulates GPIO logic state switch |
| `0x07` | `OP_SEND_CHAT` | `[0x07][Operand:4 BE]` | Tagged operand: Bit 31 set = Constant Index; Bit 31 clear = Register pointer |
| `0x08` | `OP_ON_CHAT` | `[0x08][TrigIdx:4][Reg:1][Start:4][Len:4]` | Registers event hook; skips body during linear execution |
| `0x09` | `OP_ADD` | `[0x09][Dst:1][Src:1]` | `Registers[Dst] = Registers[Dst] + Registers[Src]` (wrapping) |
| `0x0A` | `OP_SUB` | `[0x0A][Dst:1][Src:1]` | `Registers[Dst] = Registers[Dst] - Registers[Src]` (wrapping) |
| `0x0B` | `OP_MUL` | `[0x0B][Dst:1][Src:1]` | `Registers[Dst] = Registers[Dst] * Registers[Src]` (wrapping) |
| `0x0C` | `OP_DIV` | `[0x0C][Dst:1][Src:1]` | `Registers[Dst] = Registers[Dst] / Registers[Src]` (trap on 0) |
| `0x0D` | `OP_AND` | `[0x0D][Dst:1][Src:1]` | `Registers[Dst] = Registers[Dst] & Registers[Src]` |
| `0x0E` | `OP_OR` | `[0x0E][Dst:1][Src:1]` | `Registers[Dst] = Registers[Dst] \| Registers[Src]` |
| `0x0F` | `OP_XOR` | `[0x0F][Dst:1][Src:1]` | `Registers[Dst] = Registers[Dst] ^ Registers[Src]` |
| `0x10` | `OP_SHL` | `[0x10][Dst:1][Src:1]` | `Registers[Dst] = Registers[Dst] << Registers[Src]` (0 if $\ge 32$) |
| `0x11` | `OP_SHR` | `[0x11][Dst:1][Src:1]` | `Registers[Dst] = Registers[Dst] >> Registers[Src]` (0 if $\ge 32$) |

---

## 4. Memory Architecture (`vm/memory/heap.go`)

Memory management bypasses host OS allocations via a static package-level array:
```go
var RAM [1024 * 1024]byte // 1 MB Static Execution Island
4.1 Block Header Layout (6 Bytes)
Plaintext
+-----------------------+------------------+------------------+--------------------+
|  BlockSize (4 Bytes)  | IsAlloc (1 Byte) | Padding (1 Byte) |  Data Area (N B)   |
|   uint32 Big-Endian   | 0 = Free, 1 = Occ|  Alignment Pad   |    Nutzdaten...    |
+-----------------------+------------------+------------------+--------------------+
4.2 Allocation Invariants
Strategy: First-Fit traversal from base index 0x00.

Block Splitting: Occurs if remainder >= 6 bytes (Header size).

Fault Handling:

Double free on already freed header returns "memory: double free".

Free on unaligned/out-of-bounds pointer returns "memory: invalid address".

Insufficient contiguous space returns ErrOOM.

5. Event Execution Model
Plaintext
Incoming Event: DeliverChatMessage(user, msg)
                    │
                    ▼
     Match pattern in ActiveSubscriptions?
         ├── No  ──► Ignore / No-Op
         └── Yes ──► 1. Alloc & copy user string to Heap
                     2. Assign heap address to Target Register
                     3. Push PC: savedPC = c.PC
                     4. Set c.PC = Sub.BodyOffset
                     5. Execute Sub.BodyLength bytes
                     6. Free user string from Heap
                     7. Restore PC: c.PC = savedPC