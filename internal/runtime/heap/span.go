package heap

import (
	"fmt"

	"vexlua/internal/runtime/value"
)

type SpanKind uint8

const (
	SpanKindInvalid SpanKind = iota
	SpanKindObject
	SpanKindPayload
)

type SpanState uint8

const (
	SpanStateInvalid SpanState = iota
	SpanStateLive
	SpanStateFree
)

type PayloadLayout uint8

const (
	PayloadLayoutNone PayloadLayout = iota
	PayloadLayoutOpaque
	PayloadLayoutTValueArray
	PayloadLayoutHeapRefArray
	PayloadLayoutHashControl
	PayloadLayoutHashEntryArray
	PayloadLayoutBytecode
	PayloadLayoutInt32Array
	PayloadLayoutClosureSiteArray
	PayloadLayoutCaptureDescriptorArray
	PayloadLayoutFeedbackVector
)

type SpanMetadata struct {
	Kind    SpanKind
	State   SpanState
	Layout  PayloadLayout
	Owner   value.HeapOff64
	Size    uint64
	Address uintptr
}

func (heap *Heap) AllocPayload(size uint64, layout PayloadLayout, owner value.HeapOff64) (Allocation, error) {
	if layout == PayloadLayoutNone {
		return Allocation{}, fmt.Errorf("payload allocation requires an explicit layout")
	}
	return heap.allocSpan(size, SpanMetadata{Kind: SpanKindPayload, State: SpanStateLive, Layout: layout, Owner: owner})
}

func (heap *Heap) SpanMetadata(offset value.HeapOff64) (SpanMetadata, error) {
	if offset == 0 {
		return SpanMetadata{}, fmt.Errorf("offset zero is reserved")
	}
	span, ok := heap.allocations[offset]
	if !ok {
		return SpanMetadata{}, fmt.Errorf("heap offset %#x does not reference a known span", uint64(offset))
	}
	return span.metadata, nil
}

func (heap *Heap) SetSpanOwner(offset value.HeapOff64, owner value.HeapOff64) error {
	if offset == 0 {
		return fmt.Errorf("offset zero is reserved")
	}
	span, ok := heap.allocations[offset]
	if !ok {
		return fmt.Errorf("heap offset %#x does not reference a known span", uint64(offset))
	}
	if span.metadata.Kind != SpanKindPayload {
		return fmt.Errorf("span %#x is not a payload span", uint64(offset))
	}
	if err := heap.validateSpanOwner(owner); err != nil {
		return err
	}
	span.metadata.Owner = owner
	heap.allocations[offset] = span
	return nil
}

func (heap *Heap) FreeSpan(offset value.HeapOff64) error {
	if offset == 0 {
		return fmt.Errorf("offset zero is reserved")
	}
	span, ok := heap.allocations[offset]
	if !ok {
		return fmt.Errorf("heap offset %#x does not reference a known span", uint64(offset))
	}
	if span.metadata.State == SpanStateFree {
		return nil
	}
	span.metadata.State = SpanStateFree
	span.metadata.Owner = 0
	heap.allocations[offset] = span
	if heap.gc != nil {
		if span.size >= heap.gc.liveBytes {
			heap.gc.liveBytes = 0
		} else {
			heap.gc.liveBytes -= span.size
		}
	}
	bytes, err := heap.native.Bytes(span.start, span.size)
	if err != nil {
		return err
	}
	clear(bytes)
	return nil
}

func (heap *Heap) WalkSpans(visit func(offset value.HeapOff64, metadata SpanMetadata) error) error {
	_, _, err := heap.WalkSpansChunk(0, heap.SpanCount(), 0, visit)
	return err
}

func (heap *Heap) SpanCount() int {
	return len(heap.spanOrder)
}

func (heap *Heap) WalkSpansChunk(cursor int, limit int, budget uint64, visit func(offset value.HeapOff64, metadata SpanMetadata) error) (int, bool, error) {
	if cursor < 0 {
		cursor = 0
	}
	if limit <= 0 || limit > len(heap.spanOrder) {
		limit = len(heap.spanOrder)
	}
	if cursor >= limit {
		return limit, true, nil
	}
	scanned := uint64(0)
	for index := cursor; index < limit; index++ {
		offset := heap.spanOrder[index]
		span, ok := heap.allocations[offset]
		if !ok {
			return index, false, fmt.Errorf("heap span order references unknown offset %#x", uint64(offset))
		}
		if err := visit(offset, span.metadata); err != nil {
			return index, false, err
		}
		if span.metadata.Size == 0 {
			scanned++
		} else {
			scanned += span.metadata.Size
		}
		if budget != 0 && scanned >= budget {
			next := index + 1
			return next, next >= limit, nil
		}
	}
	return limit, true, nil
}

func (heap *Heap) validateSpanMetadata(metadata SpanMetadata) error {
	switch metadata.Kind {
	case SpanKindObject:
		metadata.Layout = PayloadLayoutNone
		metadata.Owner = 0
	case SpanKindPayload:
		if metadata.Layout == PayloadLayoutNone {
			return fmt.Errorf("payload span requires a layout")
		}
	default:
		return fmt.Errorf("span kind %d is invalid", metadata.Kind)
	}
	if metadata.State == SpanStateInvalid {
		return fmt.Errorf("span state must be set")
	}
	return heap.validateSpanOwner(metadata.Owner)
}

func (heap *Heap) validateSpanOwner(owner value.HeapOff64) error {
	if owner == 0 {
		return nil
	}
	span, ok := heap.allocations[owner]
	if !ok {
		return fmt.Errorf("owner span %#x does not exist", uint64(owner))
	}
	if span.metadata.Kind != SpanKindObject {
		return fmt.Errorf("owner span %#x is not an object span", uint64(owner))
	}
	_, err := heap.HeaderAtOffset(owner)
	return err
}
