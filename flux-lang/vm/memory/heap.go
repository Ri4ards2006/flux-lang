// Package memory implements the flux VM's byte-level heap.
//
// The heap is a fixed 1MB byte slice plus a tiny manual first-fit
// allocator with a 6-byte block header:
//
//	[0..4)   block data length (big-endian uint32)
//	[4]      IsAllocated flag (0 = free, 1 = allocated)
//	[5]      reserved / padding
//
// The first time the package loads, an init() function installs a
// single free block at address 0 that covers every byte after the
// header. After that, Alloc walks the chain looking for the first
// free block big enough, splits off any leftover into a new free
// block, and returns the address that immediately follows the
// block's header.
package memory

import (
	"encoding/binary"
	"errors"
)

// RAMSize is the VM's virtual address space: 1 MB.
const RAMSize = 1024 * 1024

// HeaderSize is the per-block metadata preamble.
const HeaderSize = 6

// RAM is the VM's fixed virtual address space.
var RAM [RAMSize]byte

func init() {
	Reset()
}

// Errors returned by Alloc / Free.
var (
	ErrZeroAlloc = errors.New("memory: zero-size allocation is not allowed")
	ErrOOM       = errors.New("memory: out of memory")
)

// Reset wipes RAM and installs a single free block at offset 0 that
// covers every byte after the header. Test code calls this between
// cases; production code does not need to.
func Reset() {
	for i := range RAM {
		RAM[i] = 0
	}
	binary.BigEndian.PutUint32(RAM[0:4], RAMSize-HeaderSize)
	RAM[4] = 0 // free
}

// Alloc returns the address of the first byte of a freshly allocated
// block of the requested size. The address sits immediately after
// the block's 6-byte header. The call writes a new free block into
// any leftover large enough to host another header.
//
// Errors:
//   - ErrZeroAlloc when size == 0
//   - ErrOOM when no free block is large enough
func Alloc(size uint32) (uint32, error) {
	if size == 0 {
		return 0, ErrZeroAlloc
	}

	cursor := uint32(0)
	for cursor+HeaderSize <= RAMSize {
		blkSize := binary.BigEndian.Uint32(RAM[cursor : cursor+4])
		blkAlloc := RAM[cursor+4]

		if blkAlloc == 0 && blkSize >= size {
			addr := cursor + HeaderSize

			// Resize the chosen free block to fit our request and
			// mark it allocated.
			binary.BigEndian.PutUint32(RAM[cursor:cursor+4], size)
			RAM[cursor+4] = 1

			// If there's leftover room for at least one more header
			// + data area, install a fresh free block immediately
			// after the just-allocated region.
			if blkSize > size {
				remainder := blkSize - size
				if remainder >= HeaderSize {
					newHdr := addr + size
					binary.BigEndian.PutUint32(RAM[newHdr:newHdr+4], remainder-HeaderSize)
					RAM[newHdr+4] = 0 // free
				}
			}
			return addr, nil
		}

		// Step forward to the next block in the chain.
		cursor += HeaderSize + blkSize
	}
	return 0, ErrOOM
}

// Free marks the block whose data starts at addr as free. The caller
// must pass back the same address Alloc returned; passing an address
// that doesn't lie just past a 6-byte header is a programmer error
// and exits with ErrInvalidAddress.
//
// ErrDoubleFree is returned if the block has already been freed.
func Free(addr uint32) error {
	if addr < HeaderSize || addr >= RAMSize {
		return errors.New("memory: invalid address")
	}
	hdr := addr - HeaderSize
	if RAM[hdr+4] == 0 {
		return errors.New("memory: double free")
	}
	RAM[hdr+4] = 0
	return nil
}

// IsAllocated reports whether the block at addr is currently marked
// allocated. Useful from tests and from VM debug paths.
func IsAllocated(addr uint32) bool {
	if addr < HeaderSize || addr >= RAMSize {
		return false
	}
	return RAM[addr-HeaderSize+4] == 1
}
