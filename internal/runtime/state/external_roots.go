package state

import (
	"fmt"

	"vexlua/internal/runtime/value"
)

type ExternalRootTable struct {
	refs map[value.HeapRef44]uint32
}

func NewExternalRootTable() *ExternalRootTable {
	return &ExternalRootTable{refs: make(map[value.HeapRef44]uint32)}
}

func (table *ExternalRootTable) RetainRef(ref value.HeapRef44) bool {
	if table == nil || ref == 0 {
		return false
	}
	table.refs[ref]++
	return true
}

func (table *ExternalRootTable) RetainValue(slotValue value.TValue) bool {
	ref, ok := slotValue.HeapRef()
	if !ok || ref == 0 {
		return false
	}
	return table.RetainRef(ref)
}

func (table *ExternalRootTable) ReleaseRef(ref value.HeapRef44) error {
	if table == nil || ref == 0 {
		return nil
	}
	count, ok := table.refs[ref]
	if !ok {
		return fmt.Errorf("external root %#x is not retained", uint64(ref))
	}
	if count <= 1 {
		delete(table.refs, ref)
		return nil
	}
	table.refs[ref] = count - 1
	return nil
}

func (table *ExternalRootTable) ReleaseValue(slotValue value.TValue) error {
	ref, ok := slotValue.HeapRef()
	if !ok || ref == 0 {
		return nil
	}
	return table.ReleaseRef(ref)
}

func (table *ExternalRootTable) WalkRefs(visit func(value.HeapRef44) error) error {
	if table == nil || visit == nil {
		return nil
	}
	for ref := range table.refs {
		if ref == 0 {
			continue
		}
		if err := visit(ref); err != nil {
			return err
		}
	}
	return nil
}

func (table *ExternalRootTable) RefCount(ref value.HeapRef44) uint32 {
	if table == nil || ref == 0 {
		return 0
	}
	return table.refs[ref]
}

func (table *ExternalRootTable) Len() int {
	if table == nil {
		return 0
	}
	return len(table.refs)
}

func (table *ExternalRootTable) Clear() {
	if table == nil {
		return
	}
	clear(table.refs)
}
