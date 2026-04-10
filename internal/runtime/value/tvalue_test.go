package value_test

import (
	"math"
	"testing"

	"vexlua/internal/runtime/value"
)

func TestTValueEncodesNumbersAndBoxedValues(t *testing.T) {
	number := value.NumberValue(12.5)
	if !number.IsNumber() || number.IsBoxed() {
		t.Fatalf("expected numeric TValue, got %v", number)
	}
	decoded, ok := number.Float64()
	if !ok || decoded != 12.5 {
		t.Fatalf("unexpected decoded number: %v %t", decoded, ok)
	}

	nan := value.NumberValue(math.NaN())
	if nan.Bits() != value.Raw(value.CanonicalNaN) {
		t.Fatalf("expected canonical NaN bits, got %#x", uint64(nan.Bits()))
	}

	nilValue := value.NilValue()
	if !nilValue.IsBoxedTag(value.TagNil) || nilValue.Payload() != 0 {
		t.Fatalf("unexpected nil TValue: %v", nilValue)
	}

	trueValue := value.BoolValue(true)
	boolean, ok := trueValue.Bool()
	if !ok || !boolean {
		t.Fatalf("unexpected bool TValue: %v", trueValue)
	}
}

func TestHeapRefAndHeapOffRoundTrip(t *testing.T) {
	const base = uintptr(0x1000_0000_0000)
	const address = uintptr(0x1000_0000_0030)

	ref, err := value.EncodeHeapRef44(base, address)
	if err != nil {
		t.Fatalf("unexpected heap ref encode error: %v", err)
	}
	decodedAddress, err := value.DecodeHeapRef44(base, ref)
	if err != nil {
		t.Fatalf("unexpected heap ref decode error: %v", err)
	}
	if decodedAddress != address {
		t.Fatalf("unexpected decoded heap ref address: %#x", decodedAddress)
	}

	offset, err := value.EncodeHeapOff64(base, address)
	if err != nil {
		t.Fatalf("unexpected heap offset encode error: %v", err)
	}
	if got := value.DecodeHeapOff64(base, offset); got != address {
		t.Fatalf("unexpected decoded heap offset address: %#x", got)
	}

	boxed := value.TableRefValue(ref)
	decodedRef, ok := boxed.HeapRef()
	if !ok || decodedRef != ref {
		t.Fatalf("unexpected boxed heap ref: %v", boxed)
	}
}

func TestCommonHeaderReadWriteHelpers(t *testing.T) {
	header := value.CommonHeader{
		Kind:      value.KindTable,
		Mark:      value.MarkWhite0.With(value.MarkRemembered),
		Flags:     value.HeaderFlagImmutable.With(value.HeaderFlagHasEmbeddedRefs),
		SizeBytes: 0x20,
		Version:   7,
		Aux:       11,
	}
	buffer := make([]byte, value.CommonHeaderSize)
	if err := value.WriteCommonHeader(buffer, header); err != nil {
		t.Fatalf("unexpected write header error: %v", err)
	}
	decoded, err := value.ReadCommonHeader(buffer)
	if err != nil {
		t.Fatalf("unexpected read header error: %v", err)
	}
	if decoded != header {
		t.Fatalf("unexpected decoded header: %+v", decoded)
	}

	if kind, err := value.ReadObjectKind(buffer); err != nil || kind != value.KindTable {
		t.Fatalf("unexpected kind read: %v %v", kind, err)
	}
	if sizeBytes, err := value.ReadSizeBytes(buffer); err != nil || sizeBytes != 0x20 {
		t.Fatalf("unexpected size read: %#x %v", sizeBytes, err)
	}
	if versionValue, err := value.ReadVersion(buffer); err != nil || versionValue != 7 {
		t.Fatalf("unexpected version read: %d %v", versionValue, err)
	}
	if auxValue, err := value.ReadAux(buffer); err != nil || auxValue != 11 {
		t.Fatalf("unexpected aux read: %d %v", auxValue, err)
	}
}
