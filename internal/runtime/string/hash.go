package rtstring

import "vexlua/internal/runtime/value"

func HashBytes(data []byte, seed uint32) uint32 {
	hash := uint32(len(data)) ^ seed
	step := (len(data) >> 5) + 1
	for index := len(data); index >= step; index -= step {
		hash ^= (hash << 5) + (hash >> 2) + uint32(data[index-1])
	}
	return hash
}

func HashString(text string, seed uint32) uint32 {
	return HashBytes([]byte(text), seed)
}

func IdentityEqual(left value.TValue, right value.TValue) bool {
	if !left.IsBoxedTag(value.TagStringRef) || !right.IsBoxedTag(value.TagStringRef) {
		return false
	}
	return left.Payload() == right.Payload()
}
