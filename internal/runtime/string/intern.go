package rtstring

import (
	"fmt"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

type Handle struct {
	Ref    value.HeapRef44
	Value  value.TValue
	Hash   uint32
	Length uint32
}

type InternTable struct {
	heap  *heap.Heap
	seed  uint32
	refs  map[string]value.HeapRef44
	count uint32
}

func NewInternTable(runtimeHeap *heap.Heap, seed uint32) *InternTable {
	if runtimeHeap == nil {
		panic("intern table requires a heap")
	}
	return &InternTable{
		heap: runtimeHeap,
		seed: seed,
		refs: make(map[string]value.HeapRef44),
	}
}

func (table *InternTable) Seed() uint32 {
	return table.seed
}

func (table *InternTable) Count() uint32 {
	return table.count
}

func (table *InternTable) Intern(text string) (Handle, error) {
	if ref, ok := table.refs[text]; ok {
		return table.handleForRef(ref)
	}
	hash := HashString(text, table.seed)
	header, err := NewHeader(len(text), hash)
	if err != nil {
		return Handle{}, err
	}
	allocation, err := table.heap.AllocObject(header.Common)
	if err != nil {
		return Handle{}, err
	}
	if err := WriteObject(allocation.Bytes, header, text); err != nil {
		return Handle{}, err
	}
	ref, err := table.heap.EncodeHeapRef(allocation.Address)
	if err != nil {
		return Handle{}, err
	}
	table.refs[text] = ref
	table.count++
	return Handle{
		Ref:    ref,
		Value:  value.StringRefValue(ref),
		Hash:   hash,
		Length: uint32(len(text)),
	}, nil
}

func (table *InternTable) Lookup(text string) (Handle, bool, error) {
	ref, ok := table.refs[text]
	if !ok {
		return Handle{}, false, nil
	}
	handle, err := table.handleForRef(ref)
	if err != nil {
		return Handle{}, false, err
	}
	return handle, true, nil
}

func (table *InternTable) WalkRefs(visit func(value.HeapRef44) error) error {
	if table == nil || visit == nil {
		return nil
	}
	for _, ref := range table.refs {
		if ref == 0 {
			continue
		}
		if err := visit(ref); err != nil {
			return err
		}
	}
	return nil
}

func (table *InternTable) SweepDead(isDead func(value.HeapRef44) (bool, error)) (int, error) {
	if table == nil {
		return 0, nil
	}
	if isDead == nil {
		return 0, fmt.Errorf("dead predicate cannot be nil")
	}
	removed := 0
	for text, ref := range table.refs {
		dead, err := isDead(ref)
		if err != nil {
			return removed, err
		}
		if !dead {
			continue
		}
		delete(table.refs, text)
		if table.count > 0 {
			table.count--
		}
		removed++
	}
	return removed, nil
}

func (table *InternTable) Header(ref value.HeapRef44) (Header, error) {
	header, _, err := HeaderAt(table.heap, ref)
	return header, err
}

func (table *InternTable) Text(ref value.HeapRef44) (string, error) {
	_, text, err := StringAt(table.heap, ref)
	return text, err
}

func (table *InternTable) handleForRef(ref value.HeapRef44) (Handle, error) {
	header, err := table.Header(ref)
	if err != nil {
		return Handle{}, err
	}
	return Handle{
		Ref:    ref,
		Value:  value.StringRefValue(ref),
		Hash:   header.Hash,
		Length: header.Length,
	}, nil
}

func HeaderAt(runtimeHeap *heap.Heap, ref value.HeapRef44) (Header, []byte, error) {
	if runtimeHeap == nil {
		return Header{}, nil, fmt.Errorf("heap is nil")
	}
	address, err := runtimeHeap.DecodeHeapRef(ref)
	if err != nil {
		return Header{}, nil, err
	}
	offset, err := runtimeHeap.OffsetForAddress(address)
	if err != nil {
		return Header{}, nil, err
	}
	commonBytes, err := runtimeHeap.Resolve(offset, value.CommonHeaderSize)
	if err != nil {
		return Header{}, nil, err
	}
	common, err := value.ReadCommonHeader(commonBytes)
	if err != nil {
		return Header{}, nil, err
	}
	objectBytes, err := runtimeHeap.Resolve(offset, uint64(common.SizeBytes))
	if err != nil {
		return Header{}, nil, err
	}
	header, err := ReadHeader(objectBytes)
	if err != nil {
		return Header{}, nil, err
	}
	return header, objectBytes, nil
}

func StringAt(runtimeHeap *heap.Heap, ref value.HeapRef44) (Header, string, error) {
	header, objectBytes, err := HeaderAt(runtimeHeap, ref)
	if err != nil {
		return Header{}, "", err
	}
	_, text, err := Decode(objectBytes)
	if err != nil {
		return Header{}, "", err
	}
	return header, text, nil
}
