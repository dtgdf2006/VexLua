package heap

import (
	"fmt"
	"unsafe"

	"vexlua/internal/runtime/value"
)

const (
	DefaultPageSize uint64  = 64 * 1024
	DefaultHeapBase uintptr = 0x1000_0000_0000
)

type Allocation struct {
	Offset  value.HeapOff64
	Address uintptr
	Size    uint64
	Bytes   []byte
}

type allocationSpan struct {
	start uint64
	size  uint64
}

type page struct {
	start uint64
	data  []byte
	used  uint64
}

type Heap struct {
	base        uintptr
	pageSize    uint64
	pages       []*page
	allocations map[value.HeapOff64]allocationSpan
	nextPage    uint64
	nativeArena []byte
	nativeData  []byte
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
	h.ensureNativeSize(value.ObjectAlignment)
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
	heap.ensureNativeSize(value.ObjectAlignment)
	return uintptr(unsafe.Pointer(&heap.nativeData[0]))
}

func (heap *Heap) PageSize() uint64 {
	return heap.pageSize
}

func (heap *Heap) Alloc(size uint64) (Allocation, error) {
	if size == 0 {
		return Allocation{}, fmt.Errorf("allocation size cannot be zero")
	}
	alignedSize := alignUp(size, value.ObjectAlignment)
	page := heap.currentPage(alignedSize)
	if page == nil || page.remaining() < alignedSize {
		page = heap.newPage(alignedSize)
	}
	off := page.start + page.used
	page.used += alignedSize
	allocation := Allocation{
		Offset:  value.HeapOff64(off),
		Address: heap.base + uintptr(off),
		Size:    alignedSize,
		Bytes:   page.data[page.used-alignedSize : page.used],
	}
	heap.allocations[allocation.Offset] = allocationSpan{start: off, size: alignedSize}
	return allocation, nil
}

func (heap *Heap) AllocObject(header value.CommonHeader) (Allocation, error) {
	if header.SizeBytes < value.CommonHeaderSize {
		header.SizeBytes = value.CommonHeaderSize
	}
	header.SizeBytes = uint32(alignUp(uint64(header.SizeBytes), value.ObjectAlignment))
	allocation, err := heap.Alloc(uint64(header.SizeBytes))
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
	page, localOffset := heap.findPage(uint64(offset), size)
	if page == nil {
		return nil, fmt.Errorf("heap offset %#x is out of range", uint64(offset))
	}
	return page.data[localOffset : localOffset+size], nil
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

func (heap *Heap) ResolveNative(offset value.HeapOff64, size uint64) ([]byte, error) {
	if offset == 0 {
		return nil, fmt.Errorf("offset zero is reserved")
	}
	if _, err := heap.Resolve(offset, size); err != nil {
		return nil, err
	}
	heap.ensureNativeSize(uint64(offset) + size)
	start := int(offset)
	end := start + int(size)
	return heap.nativeData[start:end], nil
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

func (heap *Heap) ValidateObjectAddress(address uintptr) error {
	if address%value.ObjectAlignment != 0 {
		return fmt.Errorf("address %#x is not %d-byte aligned", address, value.ObjectAlignment)
	}
	offset, err := heap.OffsetForAddress(address)
	if err != nil {
		return err
	}
	if _, ok := heap.allocations[offset]; !ok {
		return fmt.Errorf("address %#x is not an object start", address)
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

func (heap *Heap) SyncNative(offset value.HeapOff64, bytes []byte) error {
	if offset == 0 {
		return fmt.Errorf("offset zero is reserved")
	}
	if len(bytes) == 0 {
		return nil
	}
	if _, err := heap.Resolve(offset, uint64(len(bytes))); err != nil {
		return err
	}
	heap.ensureNativeSize(uint64(offset) + uint64(len(bytes)))
	start := int(offset)
	end := start + len(bytes)
	copy(heap.nativeData[start:end], bytes)
	return nil
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
		data:  make([]byte, pageSize),
	}
	heap.pages = append(heap.pages, page)
	heap.nextPage = start + uint64(len(page.data))
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
	return uint64(len(page.data)) - page.used
}

func (heap *Heap) ensureNativeSize(size uint64) {
	if size <= uint64(len(heap.nativeData)) {
		return
	}
	newSize := alignUp(size, value.ObjectAlignment)
	backing, data := allocAlignedBytes(int(newSize), value.ObjectAlignment)
	copy(data, heap.nativeData)
	heap.nativeArena = backing
	heap.nativeData = data
}

func allocAlignedBytes(size int, alignment uintptr) ([]byte, []byte) {
	if size <= 0 {
		size = 1
	}
	padding := int(alignment)
	if padding < 1 {
		padding = 1
	}
	backing := make([]byte, size+padding)
	base := uintptr(unsafe.Pointer(&backing[0]))
	aligned := (base + alignment - 1) &^ (alignment - 1)
	start := int(aligned - base)
	return backing, backing[start : start+size]
}

func alignUp(valueToAlign uint64, alignment uint64) uint64 {
	if alignment == 0 {
		return valueToAlign
	}
	mask := alignment - 1
	return (valueToAlign + mask) &^ mask
}
