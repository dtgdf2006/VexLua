package state

import (
	"testing"
	"unsafe"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

func TestCallFrameLayoutMatchesContract(t *testing.T) {
	if err := ValidateLayout(); err != nil {
		t.Fatalf("call frame layout mismatch: %v", err)
	}
	if err := ValidateVMStateLayout(); err != nil {
		t.Fatalf("vm state layout mismatch: %v", err)
	}
}

func TestThreadFrameRegisterAndSpillAccess(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	vm := NewVMState(runtimeHeap)
	thread, err := vm.NewThread(32, 4)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	constBase, err := thread.SlotAddress(20)
	if err != nil {
		t.Fatalf("const base: %v", err)
	}
	resultBase, err := thread.SlotAddress(24)
	if err != nil {
		t.Fatalf("result base: %v", err)
	}
	frame1, err := thread.PushFrame(FrameSpec{
		RegisterBase:  0,
		ConstBase:     constBase,
		ResultBase:    resultBase,
		RegisterCount: 8,
		SpillCount:    2,
		NResults:      2,
	})
	if err != nil {
		t.Fatalf("push frame1: %v", err)
	}
	if err := thread.SetRegister(frame1, 0, value.NumberValue(11)); err != nil {
		t.Fatalf("set register: %v", err)
	}
	if err := thread.SetSpill(frame1, 1, value.BoolValue(true)); err != nil {
		t.Fatalf("set spill: %v", err)
	}
	register, err := thread.Register(frame1, 0)
	if err != nil {
		t.Fatalf("read register: %v", err)
	}
	number, _ := register.Float64()
	if number != 11 {
		t.Fatalf("unexpected register value %g", number)
	}
	spill, err := thread.Spill(frame1, 1)
	if err != nil {
		t.Fatalf("read spill: %v", err)
	}
	boolean, ok := spill.Bool()
	if !ok || !boolean {
		t.Fatalf("unexpected spill value %s", spill)
	}

	frame2, err := thread.PushFrame(FrameSpec{
		RegisterBase:  10,
		RegisterCount: 4,
		SpillCount:    1,
		VarargBase:    constBase,
		VarargCount:   3,
	})
	if err != nil {
		t.Fatalf("push frame2: %v", err)
	}
	if thread.CurrentFrame() != frame2 {
		t.Fatalf("expected frame2 to be current")
	}
	previous, err := thread.PreviousFrame()
	if err != nil {
		t.Fatalf("resolve previous frame: %v", err)
	}
	if previous != frame1 {
		t.Fatalf("expected frame1 to be previous frame")
	}
	if !frame2.Flags.Has(FrameFlagHasVararg | FrameFlagIsLuaFrame) {
		t.Fatalf("expected lua-frame and vararg flags, got %#x", uint16(frame2.Flags))
	}

	frame1Address, err := thread.FrameAddress(0)
	if err != nil {
		t.Fatalf("frame1 address: %v", err)
	}
	if frame2.PrevFrame != uint64(frame1Address) {
		t.Fatalf("unexpected previous frame link %#x", frame2.PrevFrame)
	}

	popped, err := thread.PopFrame()
	if err != nil {
		t.Fatalf("pop frame2: %v", err)
	}
	if popped.PrevFrame != uint64(frame1Address) {
		t.Fatalf("unexpected popped frame link %#x", popped.PrevFrame)
	}
	if thread.CurrentFrame() != frame1 {
		t.Fatalf("expected frame1 to become current after pop")
	}
}

func TestPushFramePreservesNativeABIFields(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	vm := NewVMState(runtimeHeap)
	thread, err := vm.NewThread(32, 4)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	constBase, err := thread.SlotAddress(12)
	if err != nil {
		t.Fatalf("const base: %v", err)
	}
	varargBase, err := thread.SlotAddress(18)
	if err != nil {
		t.Fatalf("vararg base: %v", err)
	}
	resultBase, err := thread.SlotAddress(24)
	if err != nil {
		t.Fatalf("result base: %v", err)
	}
	frame, err := thread.PushFrame(FrameSpec{
		RegisterBase:  4,
		ConstBase:     constBase,
		VarargBase:    varargBase,
		ResultBase:    resultBase,
		RegisterCount: 6,
		SpillCount:    2,
		VarargCount:   2,
		NResults:      3,
	})
	if err != nil {
		t.Fatalf("push frame: %v", err)
	}
	regsBase, err := thread.SlotAddress(4)
	if err != nil {
		t.Fatalf("regs base: %v", err)
	}
	if frame.RegsBase != uint64(regsBase) {
		t.Fatalf("frame regs base %#x, want %#x", frame.RegsBase, regsBase)
	}
	if frame.ConstBase != uint64(constBase) {
		t.Fatalf("frame const base %#x, want %#x", frame.ConstBase, constBase)
	}
	if frame.VarargBase != uint64(varargBase) {
		t.Fatalf("frame vararg base %#x, want %#x", frame.VarargBase, varargBase)
	}
	if frame.ResultBase != uint64(resultBase) {
		t.Fatalf("frame result base %#x, want %#x", frame.ResultBase, resultBase)
	}
	if frame.RegisterCount != 6 || frame.SpillCount != 2 || frame.VarargCount != 2 {
		t.Fatalf("unexpected frame counts: regs=%d spill=%d vararg=%d", frame.RegisterCount, frame.SpillCount, frame.VarargCount)
	}
	if frame.NResults != 3 {
		t.Fatalf("frame nresults = %d, want 3", frame.NResults)
	}
	if !frame.Flags.Has(FrameFlagIsLuaFrame | FrameFlagHasVararg) {
		t.Fatalf("expected lua-frame and vararg flags, got %#x", uint16(frame.Flags))
	}
	if regIndex, err := thread.slotIndex(uintptr(frame.RegsBase)); err != nil {
		t.Fatalf("resolve regs base: %v", err)
	} else if regIndex != 4 {
		t.Fatalf("regs base index = %d, want 4", regIndex)
	}
	if constIndex, err := thread.slotIndex(uintptr(frame.ConstBase)); err != nil {
		t.Fatalf("resolve const base: %v", err)
	} else if constIndex != 12 {
		t.Fatalf("const base index = %d, want 12", constIndex)
	}
	if varargIndex, err := thread.slotIndex(uintptr(frame.VarargBase)); err != nil {
		t.Fatalf("resolve vararg base: %v", err)
	} else if varargIndex != 18 {
		t.Fatalf("vararg base index = %d, want 18", varargIndex)
	}
	if resultIndex, err := thread.slotIndex(uintptr(frame.ResultBase)); err != nil {
		t.Fatalf("resolve result base: %v", err)
	} else if resultIndex != 24 {
		t.Fatalf("result base index = %d, want 24", resultIndex)
	}
	frameAddress, err := thread.FrameAddress(0)
	if err != nil {
		t.Fatalf("frame address: %v", err)
	}
	resolved, err := thread.FrameAtAddress(frameAddress)
	if err != nil {
		t.Fatalf("resolve frame address: %v", err)
	}
	if resolved != frame {
		t.Fatalf("resolved frame pointer does not match pushed frame")
	}
	if uintptr(unsafe.Pointer(resolved)) != frameAddress {
		t.Fatalf("native frame address %#x does not match frame pointer %#x", frameAddress, uintptr(unsafe.Pointer(resolved)))
	}
}

func TestThreadUsesPinnedNativeAddressableArenas(t *testing.T) {
	runtimeHeap := heap.MustNew(0, 0)
	vm := NewVMState(runtimeHeap)
	thread, err := vm.NewThread(32, 4)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	if thread.stackBase != uintptr(unsafe.Pointer(&thread.stack[0])) {
		t.Fatalf("stack base %#x does not match backing slice address %#x", thread.stackBase, uintptr(unsafe.Pointer(&thread.stack[0])))
	}
	if thread.frameBase != uintptr(unsafe.Pointer(&thread.frames[0])) {
		t.Fatalf("frame base %#x does not match frame arena address %#x", thread.frameBase, uintptr(unsafe.Pointer(&thread.frames[0])))
	}
	if thread.frameBase%value.ObjectAlignment != 0 {
		t.Fatalf("frame base %#x is not %d-byte aligned", thread.frameBase, value.ObjectAlignment)
	}
	if thread.stackBase%value.TValueSize != 0 {
		t.Fatalf("stack base %#x is not %d-byte aligned", thread.stackBase, value.TValueSize)
	}
	nextBase, err := thread.NextRegisterBase()
	if err != nil {
		t.Fatalf("next register base on empty thread: %v", err)
	}
	if nextBase != 0 {
		t.Fatalf("empty thread next register base = %d, want 0", nextBase)
	}
	if vm.NativePointer() == nil {
		t.Fatalf("vm native pointer should not be nil")
	}
	vheader := (*VMStateHeader)(vm.NativePointer())
	if uintptr(vheader.HeapBase) != vm.HeapBase {
		t.Fatalf("vm heap base %#x does not match native header %#x", vm.HeapBase, uintptr(vheader.HeapBase))
	}
	vm.SyncActiveThread(thread)
	if uintptr(vheader.ActiveThreadStackBase) != thread.stackBase {
		t.Fatalf("native active stack base %#x does not match thread %#x", uintptr(vheader.ActiveThreadStackBase), thread.stackBase)
	}
	if uintptr(vheader.ActiveThreadFrameBase) != thread.frameBase {
		t.Fatalf("native active frame base %#x does not match thread %#x", uintptr(vheader.ActiveThreadFrameBase), thread.frameBase)
	}
	if uintptr(vheader.ActiveThreadStackEnd) != thread.stackBase+uintptr(len(thread.stack))*value.TValueSize {
		t.Fatalf("native active stack end %#x does not match thread stack end %#x", uintptr(vheader.ActiveThreadStackEnd), thread.stackBase+uintptr(len(thread.stack))*value.TValueSize)
	}
	if uintptr(vheader.ActiveThreadFrameEnd) != thread.frameBase+uintptr(len(thread.frames))*CallFrameHeaderSize {
		t.Fatalf("native active frame end %#x does not match thread frame end %#x", uintptr(vheader.ActiveThreadFrameEnd), thread.frameBase+uintptr(len(thread.frames))*CallFrameHeaderSize)
	}
	if vheader.ThreadCount != 1 {
		t.Fatalf("native thread count = %d, want 1", vheader.ThreadCount)
	}
}
