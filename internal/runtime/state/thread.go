package state

import (
	"fmt"
	"runtime"
	"unsafe"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/value"
)

const (
	defaultStackSlots = 256
	defaultFrameSlots = 64
)

type FrameSpec struct {
	Closure       value.TValue
	Proto         value.TValue
	RegisterBase  uint32
	ConstBase     uintptr
	VarargBase    uintptr
	ResultBase    uintptr
	SavedBCOff    uint32
	Flags         FrameFlags
	NResults      int16
	VarargCount   uint32
	RegisterCount uint16
	SpillCount    uint16
	Top           uint16
	ResultCap     uint16
}

type VMState struct {
	Heap          *heap.Heap
	HeapBase      uintptr
	nextThreadID  uint64
	threads       []*ThreadState
	threadByID    map[uint64]*ThreadState
	externalRoots *ExternalRootTable
	nativeArena   []byte
	nativeHeader  *VMStateHeader
	pinner        runtime.Pinner
	closed        bool
}

type ThreadState struct {
	VM           *VMState
	ID           uint64
	nativeArena  []byte
	nativeHeader *ThreadStateHeader
	stackBase    uintptr
	frameBase    uintptr
	stackArena   []byte
	frameArena   []byte
	stack        []value.TValue
	frames       []CallFrameHeader
	currentFrame int
	pinner       runtime.Pinner
	closed       bool
}

func NewVMState(runtimeHeap *heap.Heap) *VMState {
	if runtimeHeap == nil {
		panic("vm state requires a heap")
	}
	nativeArena, nativePtr := allocAlignedArena(VMStateHeaderSize, value.ObjectAlignment)
	header := (*VMStateHeader)(nativePtr)
	*header = VMStateHeader{HeapBase: uint64(runtimeHeap.Base())}
	vm := &VMState{
		Heap:          runtimeHeap,
		HeapBase:      runtimeHeap.Base(),
		nextThreadID:  1,
		threadByID:    make(map[uint64]*ThreadState),
		externalRoots: NewExternalRootTable(),
		nativeArena:   nativeArena,
		nativeHeader:  header,
	}
	vm.pinner.Pin(&vm.nativeArena[0])
	runtime.SetFinalizer(vm, (*VMState).Close)
	return vm
}

func (vm *VMState) NewThread(stackSlots uint32, frameCapacity uint32) (*ThreadState, error) {
	if stackSlots == 0 {
		stackSlots = defaultStackSlots
	}
	if frameCapacity == 0 {
		frameCapacity = defaultFrameSlots
	}
	stackArena, stackPtr := allocAlignedArena(int(stackSlots)*value.TValueSize, value.TValueSize)
	frameArena, framePtr := allocAlignedArena(int(frameCapacity)*CallFrameHeaderSize, value.ObjectAlignment)
	threadArena, threadPtr := allocAlignedArena(ThreadStateHeaderSize, value.ObjectAlignment)
	stack := unsafe.Slice((*value.TValue)(stackPtr), int(stackSlots))
	frames := unsafe.Slice((*CallFrameHeader)(framePtr), int(frameCapacity))
	thread := &ThreadState{
		VM:           vm,
		ID:           vm.nextThreadID,
		nativeArena:  threadArena,
		nativeHeader: (*ThreadStateHeader)(threadPtr),
		stackBase:    uintptr(stackPtr),
		frameBase:    uintptr(framePtr),
		stackArena:   stackArena,
		frameArena:   frameArena,
		stack:        stack,
		frames:       frames,
		currentFrame: -1,
	}
	thread.pinner.Pin(&thread.nativeArena[0])
	thread.pinner.Pin(&thread.stackArena[0])
	thread.pinner.Pin(&thread.frameArena[0])
	runtime.SetFinalizer(thread, (*ThreadState).Close)
	vm.nextThreadID++
	vm.threads = append(vm.threads, thread)
	vm.threadByID[thread.ID] = thread
	vm.syncHeader(nil)
	for index := range thread.stack {
		thread.stack[index] = value.NilValue()
	}
	for index := range thread.frames {
		thread.frames[index] = CallFrameHeader{}
	}
	thread.syncNativeHeader()
	return thread, nil
}

func (vm *VMState) Close() {
	if vm == nil || vm.closed {
		return
	}
	vm.closed = true
	runtime.SetFinalizer(vm, nil)
	threads := append([]*ThreadState(nil), vm.threads...)
	for _, thread := range threads {
		thread.Close()
	}
	vm.threads = nil
	vm.threadByID = nil
	if vm.externalRoots != nil {
		vm.externalRoots.Clear()
	}
	vm.externalRoots = nil
	vm.nativeHeader = nil
	vm.nativeArena = nil
	vm.pinner.Unpin()
}

func (thread *ThreadState) Close() {
	if thread == nil || thread.closed {
		return
	}
	thread.closed = true
	runtime.SetFinalizer(thread, nil)
	if thread.VM != nil {
		delete(thread.VM.threadByID, thread.ID)
		for index, candidate := range thread.VM.threads {
			if candidate == thread {
				thread.VM.threads = append(thread.VM.threads[:index], thread.VM.threads[index+1:]...)
				break
			}
		}
		if thread.VM.nativeHeader != nil && thread.VM.nativeHeader.ActiveThreadStateBase == uint64(uintptr(unsafe.Pointer(thread.nativeHeader))) {
			thread.VM.syncHeader(nil)
		}
	}
	thread.nativeHeader = nil
	thread.nativeArena = nil
	thread.stack = nil
	thread.frames = nil
	thread.stackArena = nil
	thread.frameArena = nil
	thread.pinner.Unpin()
}

func (vm *VMState) ThreadByID(id uint64) *ThreadState {
	if vm == nil {
		return nil
	}
	return vm.threadByID[id]
}

func (vm *VMState) Threads() []*ThreadState {
	if vm == nil {
		return nil
	}
	return append([]*ThreadState(nil), vm.threads...)
}

func (vm *VMState) ExternalRoots() *ExternalRootTable {
	if vm == nil {
		return nil
	}
	return vm.externalRoots
}

func (thread *ThreadState) StackSlots() uint32 {
	return uint32(len(thread.stack))
}

func (thread *ThreadState) FrameCapacity() uint32 {
	return uint32(len(thread.frames))
}

func (thread *ThreadState) NativePointer() unsafe.Pointer {
	if thread == nil {
		return nil
	}
	return unsafe.Pointer(thread.nativeHeader)
}

func (thread *ThreadState) OpenUpvalueHead() value.HeapRef44 {
	if thread == nil || thread.nativeHeader == nil {
		return 0
	}
	return value.HeapRef44(thread.nativeHeader.OpenUpvalueHead)
}

func (thread *ThreadState) SetOpenUpvalueHead(ref value.HeapRef44) {
	if thread == nil || thread.nativeHeader == nil {
		return
	}
	thread.nativeHeader.OpenUpvalueHead = uint64(ref)
}

func (thread *ThreadState) NextRegisterBase() (uint32, error) {
	if thread.currentFrame < 0 {
		return 0, nil
	}
	current := &thread.frames[thread.currentFrame]
	baseIndex, err := thread.slotIndex(uintptr(current.RegsBase))
	if err != nil {
		return 0, err
	}
	return uint32(baseIndex) + uint32(current.RegisterCount) + uint32(current.SpillCount), nil
}

func (thread *ThreadState) SlotAddress(index uint32) (uintptr, error) {
	if index >= uint32(len(thread.stack)) {
		return 0, fmt.Errorf("stack slot %d is outside %d slots", index, len(thread.stack))
	}
	return thread.stackBase + uintptr(index)*value.TValueSize, nil
}

func (thread *ThreadState) ValueAtAddress(address uintptr) (value.TValue, error) {
	index, err := thread.slotIndex(address)
	if err != nil {
		return value.NilValue(), err
	}
	return thread.stack[index], nil
}

func (thread *ThreadState) SlotIndexForAddress(address uintptr) (uint32, error) {
	index, err := thread.slotIndex(address)
	if err != nil {
		return 0, err
	}
	return uint32(index), nil
}

func (thread *ThreadState) SetValueAtAddress(address uintptr, slotValue value.TValue) error {
	index, err := thread.slotIndex(address)
	if err != nil {
		return err
	}
	thread.stack[index] = slotValue
	return nil
}

func (thread *ThreadState) FrameAddress(index int) (uintptr, error) {
	if index < 0 || index >= len(thread.frames) {
		return 0, fmt.Errorf("frame index %d is outside %d slots", index, len(thread.frames))
	}
	return thread.frameBase + uintptr(index)*CallFrameHeaderSize, nil
}

func (thread *ThreadState) FrameAtAddress(address uintptr) (*CallFrameHeader, error) {
	if address < thread.frameBase {
		return nil, fmt.Errorf("frame address %#x is outside thread frame region", address)
	}
	delta := address - thread.frameBase
	if delta%CallFrameHeaderSize != 0 {
		return nil, fmt.Errorf("frame address %#x is not header aligned", address)
	}
	index := int(delta / CallFrameHeaderSize)
	if index < 0 || index >= len(thread.frames) {
		return nil, fmt.Errorf("frame address %#x is outside thread frame capacity", address)
	}
	return &thread.frames[index], nil
}

func (thread *ThreadState) CurrentFrame() *CallFrameHeader {
	if thread.currentFrame < 0 {
		return nil
	}
	return &thread.frames[thread.currentFrame]
}

func (thread *ThreadState) SyncCurrentFrameFromNative() error {
	if thread == nil || thread.nativeHeader == nil {
		return nil
	}
	if thread.nativeHeader.CurrentFrame == 0 {
		thread.currentFrame = -1
		return nil
	}
	address := uintptr(thread.nativeHeader.CurrentFrame)
	if address < thread.frameBase {
		return fmt.Errorf("native current frame %#x is outside thread frame region", address)
	}
	delta := address - thread.frameBase
	if delta%CallFrameHeaderSize != 0 {
		return fmt.Errorf("native current frame %#x is not frame aligned", address)
	}
	index := int(delta / CallFrameHeaderSize)
	if index < 0 || index >= len(thread.frames) {
		return fmt.Errorf("native current frame %#x is outside thread frame capacity", address)
	}
	thread.currentFrame = index
	return nil
}

func (thread *ThreadState) PreviousFrame() (*CallFrameHeader, error) {
	current := thread.CurrentFrame()
	if current == nil || current.PrevFrame == 0 {
		return nil, nil
	}
	return thread.FrameAtAddress(uintptr(current.PrevFrame))
}

func (thread *ThreadState) PushFrame(spec FrameSpec) (*CallFrameHeader, error) {
	if thread.currentFrame+1 >= len(thread.frames) {
		return nil, fmt.Errorf("thread frame capacity %d is exhausted", len(thread.frames))
	}
	if uint32(spec.RegisterBase)+uint32(spec.RegisterCount)+uint32(spec.SpillCount) > uint32(len(thread.stack)) {
		return nil, fmt.Errorf("frame slots exceed thread stack capacity")
	}
	regsBase, err := thread.SlotAddress(spec.RegisterBase)
	if err != nil {
		return nil, err
	}
	flags := spec.Flags | FrameFlagIsLuaFrame
	if spec.VarargBase != 0 || spec.VarargCount > 0 {
		flags |= FrameFlagHasVararg
	}
	frameIndex := thread.currentFrame + 1
	frame := CallFrameHeader{
		Closure:       spec.Closure,
		Proto:         spec.Proto,
		RegsBase:      uint64(regsBase),
		ConstBase:     uint64(spec.ConstBase),
		VarargBase:    uint64(spec.VarargBase),
		SavedBCOff:    spec.SavedBCOff,
		Flags:         flags,
		NResults:      spec.NResults,
		VarargCount:   spec.VarargCount,
		RegisterCount: spec.RegisterCount,
		SpillCount:    spec.SpillCount,
		ResultBase:    uint64(spec.ResultBase),
		Top:           spec.Top,
		ResultCap:     spec.ResultCap,
	}
	if thread.currentFrame >= 0 {
		previousAddress, err := thread.FrameAddress(thread.currentFrame)
		if err != nil {
			return nil, err
		}
		frame.PrevFrame = uint64(previousAddress)
	}
	if err := frame.Validate(); err != nil {
		return nil, err
	}
	thread.frames[frameIndex] = frame
	thread.currentFrame = frameIndex
	if thread.nativeHeader != nil {
		thread.nativeHeader.CurrentFrame = uint64(thread.frameBase + uintptr(frameIndex)*CallFrameHeaderSize)
	}
	return &thread.frames[frameIndex], nil
}

func (thread *ThreadState) PopFrame() (*CallFrameHeader, error) {
	if thread.currentFrame < 0 {
		return nil, fmt.Errorf("thread has no active frame")
	}
	frame := thread.frames[thread.currentFrame]
	thread.frames[thread.currentFrame] = CallFrameHeader{}
	thread.currentFrame--
	if thread.nativeHeader != nil {
		if thread.currentFrame < 0 {
			thread.nativeHeader.CurrentFrame = 0
		} else {
			thread.nativeHeader.CurrentFrame = uint64(thread.frameBase + uintptr(thread.currentFrame)*CallFrameHeaderSize)
		}
	}
	return &frame, nil
}

func (thread *ThreadState) Register(frame *CallFrameHeader, index uint16) (value.TValue, error) {
	address, err := frame.RegisterAddress(index)
	if err != nil {
		return value.NilValue(), err
	}
	return thread.ValueAtAddress(address)
}

func (thread *ThreadState) SetRegister(frame *CallFrameHeader, index uint16, slotValue value.TValue) error {
	address, err := frame.RegisterAddress(index)
	if err != nil {
		return err
	}
	return thread.SetValueAtAddress(address, slotValue)
}

func (thread *ThreadState) Spill(frame *CallFrameHeader, index uint16) (value.TValue, error) {
	address, err := frame.SpillAddress(index)
	if err != nil {
		return value.NilValue(), err
	}
	return thread.ValueAtAddress(address)
}

func (thread *ThreadState) SetSpill(frame *CallFrameHeader, index uint16, slotValue value.TValue) error {
	address, err := frame.SpillAddress(index)
	if err != nil {
		return err
	}
	return thread.SetValueAtAddress(address, slotValue)
}

func (thread *ThreadState) slotIndex(address uintptr) (int, error) {
	if address < thread.stackBase {
		return 0, fmt.Errorf("address %#x is outside thread stack region", address)
	}
	delta := address - thread.stackBase
	if delta%value.TValueSize != 0 {
		return 0, fmt.Errorf("address %#x is not slot aligned", address)
	}
	index := int(delta / value.TValueSize)
	if index < 0 || index >= len(thread.stack) {
		return 0, fmt.Errorf("address %#x is outside thread stack capacity", address)
	}
	return index, nil
}

func (thread *ThreadState) syncNativeHeader() {
	if thread == nil || thread.nativeHeader == nil {
		return
	}
	thread.nativeHeader.StackBase = uint64(thread.stackBase)
	thread.nativeHeader.StackEnd = uint64(thread.stackBase + uintptr(len(thread.stack))*value.TValueSize)
	thread.nativeHeader.FrameBase = uint64(thread.frameBase)
	thread.nativeHeader.FrameEnd = uint64(thread.frameBase + uintptr(len(thread.frames))*CallFrameHeaderSize)
	if thread.currentFrame < 0 {
		thread.nativeHeader.CurrentFrame = 0
		return
	}
	thread.nativeHeader.CurrentFrame = uint64(thread.frameBase + uintptr(thread.currentFrame)*CallFrameHeaderSize)
}

func allocAlignedArena(size int, alignment uintptr) ([]byte, unsafe.Pointer) {
	if size <= 0 {
		size = 1
	}
	padding := int(alignment)
	if padding < 1 {
		padding = 1
	}
	arena := make([]byte, size+padding)
	basePtr := unsafe.Pointer(&arena[0])
	base := uintptr(basePtr)
	aligned := (base + alignment - 1) &^ (alignment - 1)
	return arena, unsafe.Add(basePtr, aligned-base)
}
