package host

import (
	"encoding/binary"
	"fmt"

	"vexlua/internal/runtime/value"
)

const (
	hostObjectSize     = 0x40
	hostFunctionSize   = 0x40
	hostDescriptorSize = 0x40

	hostHandleOffset        = 0x10
	reservedDescriptorOff   = 0x18
	descriptorVersionOffset = 0x1C
	reservedCacheSlotOff    = 0x20
	flagsOffset             = 0x24
	envOffset               = 0x28
	nativeMetaOffset        = 0x30
	reserved1Offset         = 0x38

	hostDescriptorIDOffset      = 0x10
	hostDescriptorVersionOffset = 0x14
	hostDescriptorShapeIDOffset = 0x18
	hostDescriptorCacheSlotOff  = 0x1C
	hostDescriptorArityOffset   = 0x20
	hostDescriptorFlagsOffset   = 0x22
	hostDescriptorKindOffset    = 0x24
	hostDescriptorReserved0Off  = 0x28
	hostDescriptorReserved1Off  = 0x30
	hostDescriptorReserved2Off  = 0x38
)

type WrapperFlags uint32

type DescriptorFlags uint16

const (
	WrapperFlagCallable WrapperFlags = 1 << iota
	WrapperFlagIndexable
	WrapperFlagWritable
	WrapperFlagMethodBinding
)

const (
	DescriptorFlagCallable DescriptorFlags = 1 << iota
	DescriptorFlagIndexable
	DescriptorFlagWritable
	DescriptorFlagVariadic
)

func WrapperFlagsForDescriptor(kind DescriptorKind, flags DescriptorFlags) WrapperFlags {
	var wrapper WrapperFlags
	if flags&DescriptorFlagCallable != 0 {
		wrapper |= WrapperFlagCallable
	}
	if flags&DescriptorFlagIndexable != 0 {
		wrapper |= WrapperFlagIndexable
	}
	if flags&DescriptorFlagWritable != 0 {
		wrapper |= WrapperFlagWritable
	}
	if kind == DescriptorKindFunction {
		wrapper &^= WrapperFlagIndexable | WrapperFlagWritable
	}
	return wrapper
}

type WrapperHeader struct {
	Common            value.CommonHeader
	HostHandle        uint64
	DescriptorVersion uint32
	Flags             WrapperFlags
	Env               value.TValue
	NativeMeta        value.HeapOff64
}

type NativeDescriptor struct {
	Common            value.CommonHeader
	DescriptorID      uint32
	DescriptorVersion uint32
	ShapeID           uint32
	CacheSlot         uint32
	Arity             uint16
	Flags             DescriptorFlags
	Kind              DescriptorKind
}

func newWrapperHeader(kind value.ObjectKind, size uint32, handle uint64, descriptorVersion uint32, flags WrapperFlags, env value.TValue, nativeMeta value.HeapOff64) WrapperHeader {
	return WrapperHeader{
		Common: value.CommonHeader{
			Kind:      kind,
			SizeBytes: size,
			Version:   1,
			Flags:     value.HeaderFlagHostManaged,
		},
		HostHandle:        handle,
		DescriptorVersion: descriptorVersion,
		Env:               env,
		Flags:             flags,
		NativeMeta:        nativeMeta,
	}
}

func newHostObjectHeader(handle uint64, descriptorVersion uint32, env value.TValue, nativeMeta value.HeapOff64) WrapperHeader {
	return newWrapperHeader(value.KindHostObject, hostObjectSize, handle, descriptorVersion, WrapperFlagIndexable|WrapperFlagWritable, env, nativeMeta)
}

func newHostFunctionHeader(handle uint64, descriptorVersion uint32, env value.TValue, nativeMeta value.HeapOff64) WrapperHeader {
	return newWrapperHeader(value.KindHostFunction, hostFunctionSize, handle, descriptorVersion, WrapperFlagCallable, env, nativeMeta)
}

func newNativeDescriptor(descriptorID uint32, descriptorVersion uint32, shapeID uint32, cacheSlot uint32, arity uint16, flags DescriptorFlags, kind DescriptorKind) NativeDescriptor {
	return NativeDescriptor{
		Common: value.CommonHeader{
			Kind:      value.KindHostDescriptor,
			SizeBytes: hostDescriptorSize,
			Version:   descriptorVersion,
			Flags:     value.HeaderFlagHostManaged,
		},
		DescriptorID:      descriptorID,
		DescriptorVersion: descriptorVersion,
		ShapeID:           shapeID,
		CacheSlot:         cacheSlot,
		Arity:             arity,
		Flags:             flags,
		Kind:              kind,
	}
}

func readWrapperHeader(buffer []byte, expectedKind value.ObjectKind, expectedSize uint32) (WrapperHeader, error) {
	if len(buffer) < int(expectedSize) {
		return WrapperHeader{}, fmt.Errorf("buffer too small for host wrapper: %d", len(buffer))
	}
	common, err := value.ReadCommonHeader(buffer)
	if err != nil {
		return WrapperHeader{}, err
	}
	if common.Kind != expectedKind {
		return WrapperHeader{}, fmt.Errorf("expected %s object, got %s", expectedKind, common.Kind)
	}
	return WrapperHeader{
		Common:            common,
		HostHandle:        binary.LittleEndian.Uint64(buffer[hostHandleOffset : hostHandleOffset+8]),
		DescriptorVersion: binary.LittleEndian.Uint32(buffer[descriptorVersionOffset : descriptorVersionOffset+4]),
		Flags:             WrapperFlags(binary.LittleEndian.Uint32(buffer[flagsOffset : flagsOffset+4])),
		Env:               value.FromRaw(value.Raw(binary.LittleEndian.Uint64(buffer[envOffset : envOffset+8]))),
		NativeMeta:        value.HeapOff64(binary.LittleEndian.Uint64(buffer[nativeMetaOffset : nativeMetaOffset+8])),
	}, nil
}

func writeWrapperHeader(buffer []byte, header WrapperHeader) error {
	if len(buffer) < int(header.Common.SizeBytes) {
		return fmt.Errorf("buffer too small for host wrapper: need %d got %d", header.Common.SizeBytes, len(buffer))
	}
	if err := value.WriteCommonHeader(buffer, header.Common); err != nil {
		return err
	}
	binary.LittleEndian.PutUint64(buffer[hostHandleOffset:hostHandleOffset+8], header.HostHandle)
	binary.LittleEndian.PutUint32(buffer[reservedDescriptorOff:reservedDescriptorOff+4], 0)
	binary.LittleEndian.PutUint32(buffer[descriptorVersionOffset:descriptorVersionOffset+4], header.DescriptorVersion)
	binary.LittleEndian.PutUint32(buffer[reservedCacheSlotOff:reservedCacheSlotOff+4], 0)
	binary.LittleEndian.PutUint32(buffer[flagsOffset:flagsOffset+4], uint32(header.Flags))
	binary.LittleEndian.PutUint64(buffer[envOffset:envOffset+8], uint64(header.Env.Bits()))
	binary.LittleEndian.PutUint64(buffer[nativeMetaOffset:nativeMetaOffset+8], uint64(header.NativeMeta))
	binary.LittleEndian.PutUint64(buffer[reserved1Offset:reserved1Offset+8], 0)
	return nil
}

func readNativeDescriptor(buffer []byte) (NativeDescriptor, error) {
	if len(buffer) < hostDescriptorSize {
		return NativeDescriptor{}, fmt.Errorf("buffer too small for host descriptor: %d", len(buffer))
	}
	common, err := value.ReadCommonHeader(buffer)
	if err != nil {
		return NativeDescriptor{}, err
	}
	if common.Kind != value.KindHostDescriptor {
		return NativeDescriptor{}, fmt.Errorf("expected %s object, got %s", value.KindHostDescriptor, common.Kind)
	}
	return NativeDescriptor{
		Common:            common,
		DescriptorID:      binary.LittleEndian.Uint32(buffer[hostDescriptorIDOffset : hostDescriptorIDOffset+4]),
		DescriptorVersion: binary.LittleEndian.Uint32(buffer[hostDescriptorVersionOffset : hostDescriptorVersionOffset+4]),
		ShapeID:           binary.LittleEndian.Uint32(buffer[hostDescriptorShapeIDOffset : hostDescriptorShapeIDOffset+4]),
		CacheSlot:         binary.LittleEndian.Uint32(buffer[hostDescriptorCacheSlotOff : hostDescriptorCacheSlotOff+4]),
		Arity:             binary.LittleEndian.Uint16(buffer[hostDescriptorArityOffset : hostDescriptorArityOffset+2]),
		Flags:             DescriptorFlags(binary.LittleEndian.Uint16(buffer[hostDescriptorFlagsOffset : hostDescriptorFlagsOffset+2])),
		Kind:              DescriptorKind(binary.LittleEndian.Uint32(buffer[hostDescriptorKindOffset : hostDescriptorKindOffset+4])),
	}, nil
}

func writeNativeDescriptor(buffer []byte, descriptor NativeDescriptor) error {
	if len(buffer) < int(descriptor.Common.SizeBytes) {
		return fmt.Errorf("buffer too small for host descriptor: need %d got %d", descriptor.Common.SizeBytes, len(buffer))
	}
	if err := value.WriteCommonHeader(buffer, descriptor.Common); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(buffer[hostDescriptorIDOffset:hostDescriptorIDOffset+4], descriptor.DescriptorID)
	binary.LittleEndian.PutUint32(buffer[hostDescriptorVersionOffset:hostDescriptorVersionOffset+4], descriptor.DescriptorVersion)
	binary.LittleEndian.PutUint32(buffer[hostDescriptorShapeIDOffset:hostDescriptorShapeIDOffset+4], descriptor.ShapeID)
	binary.LittleEndian.PutUint32(buffer[hostDescriptorCacheSlotOff:hostDescriptorCacheSlotOff+4], descriptor.CacheSlot)
	binary.LittleEndian.PutUint16(buffer[hostDescriptorArityOffset:hostDescriptorArityOffset+2], descriptor.Arity)
	binary.LittleEndian.PutUint16(buffer[hostDescriptorFlagsOffset:hostDescriptorFlagsOffset+2], uint16(descriptor.Flags))
	binary.LittleEndian.PutUint32(buffer[hostDescriptorKindOffset:hostDescriptorKindOffset+4], uint32(descriptor.Kind))
	binary.LittleEndian.PutUint64(buffer[hostDescriptorReserved0Off:hostDescriptorReserved0Off+8], 0)
	binary.LittleEndian.PutUint64(buffer[hostDescriptorReserved1Off:hostDescriptorReserved1Off+8], 0)
	binary.LittleEndian.PutUint64(buffer[hostDescriptorReserved2Off:hostDescriptorReserved2Off+8], 0)
	return nil
}
