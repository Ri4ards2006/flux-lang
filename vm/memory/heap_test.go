package memory

import "testing"

// TestAllocFreeCycle is the headline test: a single Alloc + Free
// round trip must succeed without panicking.
func TestAllocFreeCycle(t *testing.T) {
	Reset()

	addr, err := Alloc(64)
	if err != nil {
		t.Fatalf("Alloc(64): unexpected error: %v", err)
	}
	if addr == 0 {
		t.Fatalf("Alloc(64): got address 0, want non-zero")
	}
	if !IsAllocated(addr) {
		t.Errorf("Alloc(64): block at %d is not marked allocated", addr)
	}

	if err := Free(addr); err != nil {
		t.Fatalf("Free(addr): unexpected error: %v", err)
	}
	if IsAllocated(addr) {
		t.Errorf("Free(addr): block at %d still marked allocated", addr)
	}
}

// TestAllocZeroSize verifies that requesting zero bytes is rejected
// rather than silently returning a useless block.
func TestAllocZeroSize(t *testing.T) {
	Reset()
	if _, err := Alloc(0); err == nil {
		t.Errorf("Alloc(0): expected error, got nil")
	}
}

// TestAllocOOM verifies that asking for more memory than RAM can
// hold fails cleanly with ErrOOM.
func TestAllocOOM(t *testing.T) {
	Reset()
	if _, err := Alloc(uint32(RAMSize)); err == nil {
		t.Errorf("Alloc(RAMSize): expected error, got nil")
	}
}
