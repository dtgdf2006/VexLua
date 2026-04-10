package jit

import (
	"fmt"
	"sync"

	"vexlua/internal/jit/native"
)

type CodeBlob struct {
	ID          uint32
	Name        string
	EntryOffset uint32
	Memory      *native.ExecutableMemory
	Meta        *CompiledUnitMeta
}

func (blob *CodeBlob) Entry() uintptr {
	if blob == nil || blob.Memory == nil {
		return 0
	}
	return blob.Memory.Entry(blob.EntryOffset)
}

func (blob *CodeBlob) EntryAt(codeOffset uint32) uintptr {
	if blob == nil || blob.Memory == nil {
		return 0
	}
	return blob.Memory.Entry(codeOffset)
}

func (blob *CodeBlob) Size() int {
	if blob == nil || blob.Memory == nil {
		return 0
	}
	return blob.Memory.Size()
}

func (blob *CodeBlob) Close() error {
	if blob == nil || blob.Memory == nil {
		return nil
	}
	err := blob.Memory.Close()
	blob.Memory = nil
	return err
}

type CodeCache struct {
	mu     sync.Mutex
	nextID uint32
	blobs  map[uint32]*CodeBlob
}

func NewCodeCache() *CodeCache {
	return &CodeCache{blobs: make(map[uint32]*CodeBlob)}
}

func (cache *CodeCache) Install(name string, meta *CompiledUnitMeta, code []byte, entryOffset uint32) (*CodeBlob, error) {
	if meta == nil {
		return nil, fmt.Errorf("install code blob %q: missing metadata", name)
	}
	if len(code) == 0 {
		code = []byte{0xC3}
	}
	if entryOffset >= uint32(len(code)) {
		return nil, fmt.Errorf("install code blob %q: entry offset %d is outside code size %d", name, entryOffset, len(code))
	}
	mem, err := native.AllocExecutable(len(code))
	if err != nil {
		return nil, err
	}
	if err := mem.Write(code); err != nil {
		_ = mem.Close()
		return nil, err
	}
	if err := mem.Seal(); err != nil {
		_ = mem.Close()
		return nil, err
	}
	if err := meta.Finalize(entryOffset, len(code)); err != nil {
		_ = mem.Close()
		return nil, err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.nextID++
	blob := &CodeBlob{
		ID:          cache.nextID,
		Name:        name,
		EntryOffset: entryOffset,
		Memory:      mem,
		Meta:        meta,
	}
	meta.UnitID = blob.ID
	cache.blobs[blob.ID] = blob
	return blob, nil
}

func (cache *CodeCache) Lookup(id uint32) (*CodeBlob, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	blob, ok := cache.blobs[id]
	return blob, ok
}

func (cache *CodeCache) Close() error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	var firstErr error
	for id, blob := range cache.blobs {
		if err := blob.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(cache.blobs, id)
	}
	return firstErr
}
