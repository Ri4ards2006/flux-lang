# 🗺️ flux-lang Roadmap

This document outlines the architectural milestones and future direction of the flux-lang toolchain and virtual machine.

---

## Phase 1: Core Language & Control Flow (Embedded Scripting)
Goal: Transform flux-lang from a linear event-trigger DSL into a Turing-complete embedded scripting runtime.

- [x] **Arithmetic & Logic Unit (ALU)**
  - [x] Implement `OP_ADD`, `OP_SUB`, `OP_MUL`, `OP_DIV`
  - [x] Implement bitwise instructions: `OP_AND`, `OP_OR`, `OP_XOR`, `OP_SHL`, `OP_SHR`
- [x] **Control Flow & Branching**
  - [x] Implement conditional and unconditional jumps: `OP_JMP`, `OP_JZ`, `OP_JNZ`, `OP_CMP`
  - [x] Add Lexer, AST, Parser, and Codegen support for labels and backpatching jumps
  - [ ] Add structured syntax sugar for `IF/ELSE` and `WHILE/LOOP` constructs
- [ ] **Stack & Subroutines**
  - [ ] Add execution call-stack with `OP_CALL` and `OP_RET`
  - [ ] Scoped stack frames for local variables

---

## Phase 2: Ternary Logic & Multi-Valued Architecture
Goal: Provide software-level emulation and interface hooks for balanced ternary systems.

- [ ] **Trit & Tryte Data Types**
  - [ ] Define 2-bit encoding scheme for ternary states (`-1`, `0`, `+1`)
  - [ ] Add parser support for ternary literals (e.g. `3T-`, `3T0`, `3T+`)
- [ ] **Ternary ALU Extensions**
  - [ ] Implement Kleene/Łukasiewicz logic operators (`OP_T_AND`, `OP_T_OR`, `OP_T_NOT`, `OP_T_CONSENSUS`)
- [ ] **3-Way Branching**
  - [ ] Implement `OP_T_CMP` with single-instruction 3-way branching targets (Negative / Zero / Positive)

---

## Phase 3: Real-World I/O & Telemetry Integration
Goal: Replace mock string logs with concrete hardware and networking transports.

- [ ] **Hardware I/O Layer**
  - [ ] Linux GPIO integration via `/dev/gpiochip` or sysfs for `OP_TRIGGER_PIN`
  - [ ] Low-level UART/Serial communication bridge
- [ ] **Event Telemetry Drivers**
  - [ ] Real TCP/WebSocket event loop backend (Twitch IRC / MQTT streams)
  - [ ] Preemptive ring-buffered event dispatch queue

---

## Phase 4: Runtime Hardening & Memory Architecture
Goal: Production-grade memory safety, zero fragmentation, and performance optimization.

- [ ] **Heap Manager Enhancements**
  - [ ] Implement block coalescing on `Free()` to eliminate heap fragmentation
  - [ ] Add dynamic memory usage analytics and debugging flags
- [ ] **Portability & Transpilation**
  - [ ] Standalone C-runtime target for running `.flx` bytecode on bare-metal / microcontrollers
  - [ ] Deterministic timing & cycle-accurate profiling