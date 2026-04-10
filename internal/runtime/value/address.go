package value

import "fmt"

func EncodeHeapRef44(heapBase uintptr, address uintptr) (HeapRef44, error) {
	if heapBase == 0 {
		return 0, fmt.Errorf("heap base cannot be zero")
	}
	if address < heapBase {
		return 0, fmt.Errorf("address %#x is below heap base %#x", address, heapBase)
	}
	delta := uint64(address - heapBase)
	if delta == 0 {
		return 0, fmt.Errorf("heap reference zero is reserved")
	}
	if delta%ObjectAlignment != 0 {
		return 0, fmt.Errorf("address %#x is not %d-byte aligned", address, ObjectAlignment)
	}
	ref := delta >> 4
	if ref > uint64(PayloadMask) {
		return 0, fmt.Errorf("heap reference exceeds 44-bit payload: %#x", ref)
	}
	return HeapRef44(ref), nil
}

func DecodeHeapRef44(heapBase uintptr, ref HeapRef44) (uintptr, error) {
	if heapBase == 0 {
		return 0, fmt.Errorf("heap base cannot be zero")
	}
	if ref == 0 {
		return 0, fmt.Errorf("heap reference zero is reserved")
	}
	if uint64(ref) > uint64(PayloadMask) {
		return 0, fmt.Errorf("heap reference exceeds 44-bit payload: %#x", uint64(ref))
	}
	return heapBase + uintptr(uint64(ref)<<4), nil
}

func EncodeHeapOff64(heapBase uintptr, address uintptr) (HeapOff64, error) {
	if address == 0 {
		return 0, nil
	}
	if heapBase == 0 {
		return 0, fmt.Errorf("heap base cannot be zero")
	}
	if address < heapBase {
		return 0, fmt.Errorf("address %#x is below heap base %#x", address, heapBase)
	}
	return HeapOff64(uint64(address - heapBase)), nil
}

func DecodeHeapOff64(heapBase uintptr, offset HeapOff64) uintptr {
	if offset == 0 {
		return 0
	}
	return heapBase + uintptr(offset)
}
