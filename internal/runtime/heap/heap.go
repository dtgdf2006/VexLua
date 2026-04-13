package heap

import (
	"fmt"

	"vexlua/internal/runtime/value"
)

const (
	DefaultPageSize          uint64  = 64 * 1024
	DefaultHeapBase          uintptr = 0x1000_0000_0000
	DefaultNativeReserveSize uint64  = 1 * 1024 * 1024 * 1024
)

type Allocation struct {
	Offset  value.HeapOff64
	Address uintptr
	Size    uint64
	Bytes   []byte
}

type allocationSpan struct {
	start    uint64
	size     uint64
	metadata SpanMetadata
}

type page struct {
	start uint64
	size  uint64
	used  uint64
}

type Heap struct {
	base        uintptr
	pageSize    uint64
	pages       []*page
	allocations map[value.HeapOff64]allocationSpan
	spanOrder   []value.HeapOff64
	nextPage    uint64
	native      nativeArena
	gc          *GCController
}

func New(base uintptr, pageSize uint64) (*Heap, error) {
	if base == 0 {
		base = DefaultHeapBase
	}
	if pageSize == 0 {
		pageSize = DefaultPageSize
	}
	if pageSize < value.ObjectAlignment {
		return nil, fmt.Errorf("page size %d is smaller than object alignment %d", pageSize, value.ObjectAlignment)
	}
	h := &Heap{
		base:        base,
		pageSize:    alignUp(pageSize, value.ObjectAlignment),
		allocations: make(map[value.HeapOff64]allocationSpan),
		nextPage:    value.ObjectAlignment,
	}
	h.gc = newGCController(h.pageSize)
	reserveSize := DefaultNativeReserveSize
	if reserveSize < h.pageSize {
		reserveSize = h.pageSize
	}
	native, err := newNativeArena(reserveSize, value.ObjectAlignment)
	if err != nil {
		return nil, err
	}
	h.native = native
	return h, nil
}

func MustNew(base uintptr, pageSize uint64) *Heap {
	heap, err := New(base, pageSize)
	if err != nil {
		panic(err)
	}
	return heap
}

func (heap *Heap) Base() uintptr {
	return heap.base
}

func (heap *Heap) NativeBase() uintptr {
	return heap.native.Base()
}

func (heap *Heap) PageSize() uint64 {
	return heap.pageSize
}

func (heap *Heap) Alloc(size uint64) (Allocation, error) {
	return heap.allocSpan(size, SpanMetadata{Kind: SpanKindPayload, State: SpanStateLive, Layout: PayloadLayoutOpaque})
}

func (heap *Heap) allocSpan(size uint64, metadata SpanMetadata) (Allocation, error) {
	if size == 0 {
		return Allocation{}, fmt.Errorf("allocation size cannot be zero")
	}
	if err := heap.validateSpanMetadata(metadata); err != nil {
		return Allocation{}, err
	}
	alignedSize := alignUp(size, value.ObjectAlignment)
	if allocation, ok, err := heap.reuseFreeSpan(alignedSize, metadata); err != nil {
		return Allocation{}, err
	} else if ok {
		return allocation, nil
	}
	page := heap.currentPage(alignedSize)
	if page == nil || page.remaining() < alignedSize {
		page = heap.newPage(alignedSize)
	}
	off := page.start + page.used
	if err := heap.native.EnsureCommitted(off + alignedSize); err != nil {
		return Allocation{}, err
	}
	bytes, err := heap.native.Bytes(off, alignedSize)
	if err != nil {
		return Allocation{}, err
	}
	page.used += alignedSize
	allocation := Allocation{
		Offset:  value.HeapOff64(off),
		Address: heap.base + uintptr(off),
		Size:    alignedSize,
		Bytes:   bytes,
	}
	metadata.Size = alignedSize
	metadata.Address = allocation.Address
	heap.allocations[allocation.Offset] = allocationSpan{start: off, size: alignedSize, metadata: metadata}
	heap.spanOrder = append(heap.spanOrder, allocation.Offset)
	if heap.gc != nil {
		heap.gc.liveBytes += alignedSize
	}
	return allocation, nil
}

func (heap *Heap) reuseFreeSpan(size uint64, metadata SpanMetadata) (Allocation, bool, error) {
	var bestOffset value.HeapOff64
	var bestSpan allocationSpan
	found := false
	for offset, span := range heap.allocations {
		if span.metadata.State != SpanStateFree || span.size < size {
			continue
		}
		if !found || span.size < bestSpan.size {
			bestOffset = offset
			bestSpan = span
			found = true
		}
	}
	if !found {
		return Allocation{}, false, nil
	}
	if err := heap.native.EnsureCommitted(bestSpan.start + bestSpan.size); err != nil {
		return Allocation{}, false, err
	}
	bytes, err := heap.native.Bytes(bestSpan.start, bestSpan.size)
	if err != nil {
		return Allocation{}, false, err
	}
	clear(bytes)
	metadata.State = SpanStateLive
	metadata.Size = bestSpan.size
	metadata.Address = heap.base + uintptr(bestSpan.start)
	bestSpan.metadata = metadata
	heap.allocations[bestOffset] = bestSpan
	if heap.gc != nil {
		heap.gc.liveBytes += bestSpan.size
	}
	return Allocation{
		Offset:  bestOffset,
		Address: metadata.Address,
		Size:    bestSpan.size,
		Bytes:   bytes,
	}, true, nil
}

func (heap *Heap) AllocObject(header value.CommonHeader) (Allocation, error) {
	if header.SizeBytes < value.CommonHeaderSize {
		header.SizeBytes = value.CommonHeaderSize
	}
	header.SizeBytes = uint32(alignUp(uint64(header.SizeBytes), value.ObjectAlignment))
	if header.Mark == 0 {
		header.Mark = heap.CurrentWhite()
	}
	allocation, err := heap.allocSpan(uint64(header.SizeBytes), SpanMetadata{Kind: SpanKindObject, State: SpanStateLive})
	if err != nil {
		return Allocation{}, err
	}
	if err := value.WriteCommonHeader(allocation.Bytes, header); err != nil {
		return Allocation{}, err
	}
	return allocation, nil
}

func (heap *Heap) Resolve(offset value.HeapOff64, size uint64) ([]byte, error) {
	if offset == 0 {
		return nil, fmt.Errorf("offset zero is reserved")
	}
	if page, _ := heap.findPage(uint64(offset), size); page == nil {
		return nil, fmt.Errorf("heap offset %#x is out of range", uint64(offset))
	}
	if size > 0 {
		if err := heap.native.EnsureCommitted(uint64(offset) + size); err != nil {
			return nil, err
		}
	}
	return heap.native.Bytes(uint64(offset), size)
}

func (heap *Heap) AddressForOffset(offset value.HeapOff64) (uintptr, error) {
	if offset == 0 {
		return 0, nil
	}
	if _, err := heap.Resolve(offset, 1); err != nil {
		return 0, err
	}
	return heap.base + uintptr(offset), nil
}

func (heap *Heap) NativeAddressForOffset(offset value.HeapOff64) (uintptr, error) {
	if offset == 0 {
		return 0, nil
	}
	if _, err := heap.Resolve(offset, 1); err != nil {
		return 0, err
	}
	return heap.NativeBase() + uintptr(offset), nil
}

func (heap *Heap) OffsetForAddress(address uintptr) (value.HeapOff64, error) {
	offset, err := value.EncodeHeapOff64(heap.base, address)
	if err != nil {
		return 0, err
	}
	if offset == 0 {
		return 0, fmt.Errorf("address %#x does not reference allocated heap memory", address)
	}
	if _, err := heap.Resolve(offset, 1); err != nil {
		return 0, err
	}
	return offset, nil
}

func (heap *Heap) OffsetForNativeAddress(address uintptr) (value.HeapOff64, error) {
	offset, err := value.EncodeHeapOff64(heap.NativeBase(), address)
	if err != nil {
		return 0, err
	}
	if offset == 0 {
		return 0, fmt.Errorf("native address %#x does not reference allocated heap memory", address)
	}
	if _, err := heap.Resolve(offset, 1); err != nil {
		return 0, err
	}
	return offset, nil
}

func (heap *Heap) ValidateObjectAddress(address uintptr) error {
	if address%value.ObjectAlignment != 0 {
		return fmt.Errorf("address %#x is not %d-byte aligned", address, value.ObjectAlignment)
	}
	offset, err := heap.OffsetForAddress(address)
	if err != nil {
		return err
	}
	span, ok := heap.allocations[offset]
	if !ok {
		return fmt.Errorf("address %#x is not an object start", address)
	}
	if span.metadata.State != SpanStateLive {
		return fmt.Errorf("address %#x does not reference a live object", address)
	}
	if span.metadata.Kind != SpanKindObject {
		return fmt.Errorf("address %#x does not reference an object span", address)
	}
	header, err := heap.HeaderAtOffset(offset)
	if err != nil {
		return err
	}
	return header.Validate()
}

func (heap *Heap) HeaderAtOffset(offset value.HeapOff64) (value.CommonHeader, error) {
	bytes, err := heap.Resolve(offset, value.CommonHeaderSize)
	if err != nil {
		return value.CommonHeader{}, err
	}
	return value.ReadCommonHeader(bytes)
}

func (heap *Heap) HeaderAtAddress(address uintptr) (value.CommonHeader, error) {
	offset, err := heap.OffsetForAddress(address)
	if err != nil {
		return value.CommonHeader{}, err
	}
	return heap.HeaderAtOffset(offset)
}

func (heap *Heap) WriteHeader(offset value.HeapOff64, header value.CommonHeader) error {
	bytes, err := heap.Resolve(offset, value.CommonHeaderSize)
	if err != nil {
		return err
	}
	return value.WriteCommonHeader(bytes, header)
}

func (heap *Heap) EncodeHeapRef(address uintptr) (value.HeapRef44, error) {
	if err := heap.ValidateObjectAddress(address); err != nil {
		return 0, err
	}
	return value.EncodeHeapRef44(heap.base, address)
}

func (heap *Heap) DecodeHeapRef(ref value.HeapRef44) (uintptr, error) {
	address, err := value.DecodeHeapRef44(heap.base, ref)
	if err != nil {
		return 0, err
	}
	if err := heap.ValidateObjectAddress(address); err != nil {
		return 0, err
	}
	return address, nil
}

func (heap *Heap) currentPage(size uint64) *page {
	if len(heap.pages) == 0 {
		return nil
	}
	page := heap.pages[len(heap.pages)-1]
	if page.remaining() >= size {
		return page
	}
	return nil
}

func (heap *Heap) newPage(minSize uint64) *page {
	pageSize := heap.pageSize
	if minSize > pageSize {
		pageSize = alignUp(minSize, value.ObjectAlignment)
	}
	start := alignUp(heap.nextPage, value.ObjectAlignment)
	page := &page{
		start: start,
		size:  pageSize,
	}
	heap.pages = append(heap.pages, page)
	heap.nextPage = start + page.size
	return page
}

func (heap *Heap) findPage(offset uint64, size uint64) (*page, uint64) {
	for _, page := range heap.pages {
		if offset < page.start {
			continue
		}
		local := offset - page.start
		if local > page.used {
			continue
		}
		if local+size > page.used {
			continue
		}
		return page, local
	}
	return nil, 0
}

func (page *page) remaining() uint64 {
	return page.size - page.used
}

func alignUp(valueToAlign uint64, alignment uint64) uint64 {
	if alignment == 0 {
		return valueToAlign
	}
	mask := alignment - 1
	return (valueToAlign + mask) &^ mask
}
