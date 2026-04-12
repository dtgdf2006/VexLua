package heap

import (
	"fmt"

	"vexlua/internal/runtime/value"
)

type GCPhase uint8

const (
	GCPhasePause GCPhase = iota
	GCPhaseMark
	GCPhaseAtomic
	GCPhaseSweepStrings
	GCPhaseSweepObjects
	GCPhaseFinalize
)

type GCQueueLengths struct {
	Gray       int
	GrayAgain  int
	Weak       int
	Finalize   int
	Remembered int
}

type GCController struct {
	phase        GCPhase
	currentWhite value.MarkBits
	threshold    uint64
	nextTrigger  uint64
	stepBudget   uint64
	liveBytes    uint64
	gray         []value.HeapRef44
	grayAgain    []value.HeapRef44
	weak         []value.HeapRef44
	finalize     []value.HeapRef44
	remembered   []value.HeapRef44
}

func newGCController(pageSize uint64) *GCController {
	threshold := pageSize * 4
	if threshold == 0 {
		threshold = DefaultPageSize * 4
	}
	return &GCController{
		phase:        GCPhasePause,
		currentWhite: value.MarkWhite0,
		threshold:    threshold,
		nextTrigger:  threshold,
		stepBudget:   1024,
	}
}

func (heap *Heap) GCController() *GCController {
	if heap == nil {
		return nil
	}
	return heap.gc
}

func (heap *Heap) GCPhase() GCPhase {
	if heap == nil || heap.gc == nil {
		return GCPhasePause
	}
	return heap.gc.phase
}

func (heap *Heap) SetGCPhase(phase GCPhase) {
	if heap == nil || heap.gc == nil {
		return
	}
	heap.gc.phase = phase
	if phase == GCPhasePause {
		heap.gc.ResetQueues()
	}
	if phase != GCPhaseMark {
		heap.gc.remembered = heap.gc.remembered[:0]
	}
	if phase != GCPhaseAtomic {
		heap.gc.grayAgain = heap.gc.grayAgain[:0]
	}
	if phase != GCPhaseFinalize {
		heap.gc.finalize = heap.gc.finalize[:0]
	}
}

func (heap *Heap) GCThreshold() uint64 {
	if heap == nil || heap.gc == nil {
		return 0
	}
	return heap.gc.threshold
}

func (heap *Heap) SetGCThreshold(threshold uint64) {
	if heap == nil || heap.gc == nil {
		return
	}
	heap.gc.threshold = threshold
	heap.gc.rearmThreshold()
}

func (heap *Heap) GCStepBudget() uint64 {
	if heap == nil || heap.gc == nil {
		return 0
	}
	return heap.gc.stepBudget
}

func (heap *Heap) SetGCStepBudget(budget uint64) {
	if heap == nil || heap.gc == nil {
		return
	}
	heap.gc.stepBudget = budget
}

func (heap *Heap) LiveBytes() uint64 {
	if heap == nil || heap.gc == nil {
		return 0
	}
	return heap.gc.liveBytes
}

func (heap *Heap) NextGCTrigger() uint64 {
	if heap == nil || heap.gc == nil {
		return 0
	}
	return heap.gc.nextTrigger
}

func (heap *Heap) RearmGCThreshold() {
	if heap == nil || heap.gc == nil {
		return
	}
	heap.gc.rearmThreshold()
}

func (heap *Heap) GCTargetReached() bool {
	if heap == nil || heap.gc == nil {
		return false
	}
	if heap.gc.threshold == 0 {
		return false
	}
	return heap.gc.liveBytes >= heap.gc.nextTrigger
}

func (heap *Heap) CurrentWhite() value.MarkBits {
	if heap == nil || heap.gc == nil {
		return value.MarkWhite0
	}
	return heap.gc.currentWhite
}

func (heap *Heap) SetCurrentWhite(mark value.MarkBits) error {
	if heap == nil || heap.gc == nil {
		return nil
	}
	if mark != value.MarkWhite0 && mark != value.MarkWhite1 {
		return fmt.Errorf("current white must be white0 or white1, got %#x", uint8(mark))
	}
	heap.gc.currentWhite = mark
	return nil
}

func (heap *Heap) FlipCurrentWhite() {
	if heap == nil || heap.gc == nil {
		return
	}
	if heap.gc.currentWhite == value.MarkWhite1 {
		heap.gc.currentWhite = value.MarkWhite0
		return
	}
	heap.gc.currentWhite = value.MarkWhite1
}

func (heap *Heap) GCQueueLengths() GCQueueLengths {
	if heap == nil || heap.gc == nil {
		return GCQueueLengths{}
	}
	return GCQueueLengths{
		Gray:       len(heap.gc.gray),
		GrayAgain:  len(heap.gc.grayAgain),
		Weak:       len(heap.gc.weak),
		Finalize:   len(heap.gc.finalize),
		Remembered: len(heap.gc.remembered),
	}
}

func (heap *Heap) ResetGCQueues() {
	if heap == nil || heap.gc == nil {
		return
	}
	heap.gc.ResetQueues()
}

func (controller *GCController) ResetQueues() {
	if controller == nil {
		return
	}
	controller.gray = controller.gray[:0]
	controller.grayAgain = controller.grayAgain[:0]
	controller.weak = controller.weak[:0]
	controller.finalize = controller.finalize[:0]
	controller.remembered = controller.remembered[:0]
}

func (heap *Heap) EnqueueGray(ref value.HeapRef44) {
	if heap == nil || heap.gc == nil || ref == 0 {
		return
	}
	heap.gc.gray = append(heap.gc.gray, ref)
}

func (heap *Heap) EnqueueWeak(ref value.HeapRef44) {
	if heap == nil || heap.gc == nil || ref == 0 {
		return
	}
	heap.gc.weak = appendUniqueRef(heap.gc.weak, ref)
}

func (heap *Heap) EnqueueFinalize(ref value.HeapRef44) {
	if heap == nil || heap.gc == nil || ref == 0 {
		return
	}
	heap.gc.finalize = appendUniqueRef(heap.gc.finalize, ref)
}

func (heap *Heap) GrayQueueSnapshot() []value.HeapRef44 {
	return cloneRefs(heap, func(controller *GCController) []value.HeapRef44 { return controller.gray })
}

func (heap *Heap) DrainGrayQueue() []value.HeapRef44 {
	return drainRefs(heap, func(controller *GCController) *[]value.HeapRef44 { return &controller.gray })
}

func (heap *Heap) GrayAgainQueueSnapshot() []value.HeapRef44 {
	return cloneRefs(heap, func(controller *GCController) []value.HeapRef44 { return controller.grayAgain })
}

func (heap *Heap) DrainGrayAgainQueue() []value.HeapRef44 {
	return drainRefs(heap, func(controller *GCController) *[]value.HeapRef44 { return &controller.grayAgain })
}

func (heap *Heap) WeakQueueSnapshot() []value.HeapRef44 {
	return cloneRefs(heap, func(controller *GCController) []value.HeapRef44 { return controller.weak })
}

func (heap *Heap) FinalizeQueueSnapshot() []value.HeapRef44 {
	return cloneRefs(heap, func(controller *GCController) []value.HeapRef44 { return controller.finalize })
}

func (heap *Heap) RememberedQueueSnapshot() []value.HeapRef44 {
	return cloneRefs(heap, func(controller *GCController) []value.HeapRef44 { return controller.remembered })
}

func (heap *Heap) WriteBarrier(parentRef value.HeapRef44, childRef value.HeapRef44) error {
	if parentRef == 0 || childRef == 0 {
		return nil
	}
	parentAddress, err := heap.DecodeHeapRef(parentRef)
	if err != nil {
		return err
	}
	parentOffset, err := heap.OffsetForAddress(parentAddress)
	if err != nil {
		return err
	}
	return heap.writeBarrier(parentOffset, parentRef, childRef)
}

func (heap *Heap) WriteBarrierByOffset(parentOffset value.HeapOff64, childRef value.HeapRef44) error {
	if parentOffset == 0 || childRef == 0 {
		return nil
	}
	parentRef, err := heap.refForOffset(parentOffset)
	if err != nil {
		return err
	}
	return heap.writeBarrier(parentOffset, parentRef, childRef)
}

func (heap *Heap) WriteBarrierValueByOffset(parentOffset value.HeapOff64, slotValue value.TValue) error {
	childRef, ok := slotValue.HeapRef()
	if !ok || childRef == 0 {
		return nil
	}
	return heap.WriteBarrierByOffset(parentOffset, childRef)
}

func (heap *Heap) RememberWeakOwner(ref value.HeapRef44) {
	if heap == nil || heap.gc == nil || ref == 0 || heap.gc.phase == GCPhasePause {
		return
	}
	heap.gc.weak = appendUniqueRef(heap.gc.weak, ref)
}

func (heap *Heap) RememberWeakOwnerByOffset(offset value.HeapOff64) error {
	if heap == nil || heap.gc == nil || offset == 0 || heap.gc.phase == GCPhasePause {
		return nil
	}
	ref, err := heap.refForOffset(offset)
	if err != nil {
		return err
	}
	heap.gc.weak = appendUniqueRef(heap.gc.weak, ref)
	return nil
}

func (heap *Heap) writeBarrier(parentOffset value.HeapOff64, parentRef value.HeapRef44, childRef value.HeapRef44) error {
	if heap == nil || heap.gc == nil || heap.gc.phase != GCPhaseMark {
		return nil
	}
	parentHeader, err := heap.HeaderAtOffset(parentOffset)
	if err != nil {
		return err
	}
	if !parentHeader.Mark.Has(value.MarkBlack) {
		return nil
	}
	childAddress, err := heap.DecodeHeapRef(childRef)
	if err != nil {
		return err
	}
	childOffset, err := heap.OffsetForAddress(childAddress)
	if err != nil {
		return err
	}
	childHeader, err := heap.HeaderAtOffset(childOffset)
	if err != nil {
		return err
	}
	if !childHeader.Mark.Has(heap.gc.currentWhite) {
		return nil
	}
	parentHeader.Mark = parentHeader.Mark.With(value.MarkGray | value.MarkRemembered).Without(value.MarkBlack)
	if err := heap.WriteHeader(parentOffset, parentHeader); err != nil {
		return err
	}
	heap.gc.grayAgain = appendUniqueRef(heap.gc.grayAgain, parentRef)
	heap.gc.remembered = appendUniqueRef(heap.gc.remembered, parentRef)
	return nil
}

func (heap *Heap) refForOffset(offset value.HeapOff64) (value.HeapRef44, error) {
	address, err := heap.AddressForOffset(offset)
	if err != nil {
		return 0, err
	}
	return heap.EncodeHeapRef(address)
}

func cloneRefs(heap *Heap, selectQueue func(*GCController) []value.HeapRef44) []value.HeapRef44 {
	if heap == nil || heap.gc == nil || selectQueue == nil {
		return nil
	}
	queue := selectQueue(heap.gc)
	if len(queue) == 0 {
		return nil
	}
	return append([]value.HeapRef44(nil), queue...)
}

func drainRefs(heap *Heap, selectQueue func(*GCController) *[]value.HeapRef44) []value.HeapRef44 {
	if heap == nil || heap.gc == nil || selectQueue == nil {
		return nil
	}
	queue := selectQueue(heap.gc)
	if queue == nil || len(*queue) == 0 {
		return nil
	}
	drained := append([]value.HeapRef44(nil), (*queue)...)
	*queue = (*queue)[:0]
	return drained
}

func (controller *GCController) rearmThreshold() {
	if controller == nil {
		return
	}
	if controller.threshold == 0 {
		controller.nextTrigger = 0
		return
	}
	controller.nextTrigger = controller.liveBytes + controller.threshold
	if controller.nextTrigger < controller.liveBytes {
		controller.nextTrigger = ^uint64(0)
	}
}

func appendUniqueRef(queue []value.HeapRef44, ref value.HeapRef44) []value.HeapRef44 {
	for _, existing := range queue {
		if existing == ref {
			return queue
		}
	}
	return append(queue, ref)
}
