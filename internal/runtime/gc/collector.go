package gc

import (
	"fmt"

	"vexlua/internal/runtime/heap"
	"vexlua/internal/runtime/host"
	rproto "vexlua/internal/runtime/proto"
	"vexlua/internal/runtime/state"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
	"vexlua/internal/vexarc/baseline"
)

type Config struct {
	Threshold       uint64
	StepBudget      uint64
	ProtoStore      *rproto.Store
	DefaultRoots    []RootSource
	VM              *state.VMState
	Strings         *rtstring.InternTable
	CompiledRuntime *baseline.Runtime
}

type Collector struct {
	heap           *heap.Heap
	scanner        *Scanner
	tracer         *Tracer
	protos         *rproto.Store
	defaultRoots   []RootSource
	weakRefs       []WeakRef
	weakRefSet     map[WeakRef]struct{}
	vm             *state.VMState
	strings        *rtstring.InternTable
	allocDebt      uint64
	prepareCursor  int
	prepareLimit   int
	preparing      bool
	sweepCursor    int
	sweepLimit     int
	sweeping       bool
	sweepPayloads  map[value.HeapOff64][]value.HeapOff64
	sweepPending   map[value.HeapOff64]struct{}
	sweepDeadWhite value.MarkBits
	sweepNextWhite value.MarkBits
	sweepStats     SweepStats
}

type SweepStats struct {
	FreedObjects          int
	FreedPayloads         int
	ScrubbedFeedbackCells int
	ClearedWeakTableEdges int
	FinalizedHostWrappers int
}

func NewCollector(runtimeHeap *heap.Heap, hosts *host.Registry, config Config) *Collector {
	if runtimeHeap == nil {
		panic("gc collector requires a heap")
	}
	collector := &Collector{
		heap:         runtimeHeap,
		scanner:      NewScanner(runtimeHeap),
		tracer:       NewTracer(runtimeHeap, hosts),
		protos:       config.ProtoStore,
		defaultRoots: append([]RootSource(nil), config.DefaultRoots...),
		vm:           config.VM,
		strings:      config.Strings,
	}
	if config.Threshold != 0 {
		collector.SetThreshold(config.Threshold)
	}
	if config.StepBudget != 0 {
		collector.SetStepBudget(config.StepBudget)
	}
	if config.CompiledRuntime != nil {
		collector.scanner.BindCompiledRuntime(config.CompiledRuntime)
		collector.defaultRoots = append(collector.defaultRoots, CompiledMetadataRoots(config.CompiledRuntime))
	}
	return collector
}

func (collector *Collector) Heap() *heap.Heap {
	if collector == nil {
		return nil
	}
	return collector.heap
}

func (collector *Collector) Scanner() *Scanner {
	if collector == nil {
		return nil
	}
	return collector.scanner
}

func (collector *Collector) Tracer() *Tracer {
	if collector == nil {
		return nil
	}
	return collector.tracer
}

func (collector *Collector) Phase() heap.GCPhase {
	if collector == nil || collector.heap == nil {
		return heap.GCPhasePause
	}
	return collector.heap.GCPhase()
}

func (collector *Collector) SetPhase(phase heap.GCPhase) {
	if collector == nil || collector.heap == nil {
		return
	}
	collector.heap.SetGCPhase(phase)
}

func (collector *Collector) Threshold() uint64 {
	if collector == nil || collector.heap == nil {
		return 0
	}
	return collector.heap.GCThreshold()
}

func (collector *Collector) SetThreshold(threshold uint64) {
	if collector == nil || collector.heap == nil {
		return
	}
	collector.heap.SetGCThreshold(threshold)
}

func (collector *Collector) StepBudget() uint64 {
	if collector == nil || collector.heap == nil {
		return 0
	}
	return collector.heap.GCStepBudget()
}

func (collector *Collector) SetStepBudget(budget uint64) {
	if collector == nil || collector.heap == nil {
		return
	}
	collector.heap.SetGCStepBudget(budget)
}

func (collector *Collector) BindRuntime(vm *state.VMState, strings *rtstring.InternTable) {
	if collector == nil {
		return
	}
	collector.vm = vm
	collector.strings = strings
}

func (collector *Collector) AllocationDebt() uint64 {
	if collector == nil {
		return 0
	}
	return collector.allocDebt
}

func (collector *Collector) AssistAllocation(bytes uint64) error {
	if collector == nil || collector.heap == nil || collector.vm == nil || collector.strings == nil || bytes == 0 {
		return nil
	}
	collector.allocDebt += bytes
	if collector.Phase() == heap.GCPhasePause {
		if !collector.heap.GCTargetReached() {
			return nil
		}
		return collector.stepOnce()
	}
	budget := collector.StepBudget()
	if budget != 0 && collector.allocDebt < budget {
		return nil
	}
	if err := collector.stepOnce(); err != nil {
		return err
	}
	if collector.Phase() == heap.GCPhasePause {
		collector.allocDebt = 0
		return nil
	}
	if budget == 0 || collector.allocDebt < budget {
		collector.allocDebt = 0
		return nil
	}
	collector.allocDebt -= budget
	return nil
}

func (collector *Collector) AssistSafepoint() error {
	if collector == nil || collector.heap == nil || collector.vm == nil || collector.strings == nil {
		return nil
	}
	if collector.Phase() == heap.GCPhasePause && !collector.heap.GCTargetReached() {
		return nil
	}
	if err := collector.stepOnce(); err != nil {
		return err
	}
	if collector.Phase() == heap.GCPhasePause {
		collector.allocDebt = 0
	}
	return nil
}

func (collector *Collector) QueueLengths() heap.GCQueueLengths {
	if collector == nil || collector.heap == nil {
		return heap.GCQueueLengths{}
	}
	return collector.heap.GCQueueLengths()
}

func (collector *Collector) BeginMarkPhase() {
	if collector == nil || collector.heap == nil {
		return
	}
	collector.clearWeakRefs()
	collector.resetPrepareState()
	collector.resetSweepState()
	collector.heap.ResetGCQueues()
	collector.heap.SetGCPhase(heap.GCPhaseMark)
}

func (collector *Collector) StartCollection() error {
	if collector == nil || collector.heap == nil {
		return fmt.Errorf("collector is not initialized")
	}
	collector.beginPreparePhase()
	for {
		done, err := collector.resetObjectMarksStep(0)
		if err != nil {
			return err
		}
		if done {
			break
		}
	}
	collector.heap.SetGCPhase(heap.GCPhaseMark)
	return nil
}

func (collector *Collector) SeedRoots(vm *state.VMState, extra ...RootSource) error {
	if collector == nil || collector.scanner == nil || collector.heap == nil {
		return fmt.Errorf("collector is not initialized")
	}
	sources := make([]RootSource, 0, len(collector.defaultRoots)+len(extra)+2)
	if collector.tracer != nil && collector.tracer.hosts != nil {
		sources = append(sources, HostRegistryRoots(collector.tracer.hosts))
	}
	if collector.protos != nil {
		sources = append(sources, ProtoStoreRoots(collector.protos))
	}
	sources = append(sources, collector.defaultRoots...)
	sources = append(sources, extra...)
	return collector.scanner.WalkRoots(vm, collector.shadeGray, sources...)
}

func (collector *Collector) Propagate() error {
	if collector == nil || collector.heap == nil || collector.tracer == nil {
		return fmt.Errorf("collector is not initialized")
	}
	for {
		gray := collector.heap.DrainGrayQueue()
		if len(gray) == 0 {
			return nil
		}
		for _, ref := range gray {
			if err := collector.traceGrayObject(ref); err != nil {
				return err
			}
		}
	}
}

func (collector *Collector) RunAtomic(vm *state.VMState, extra ...RootSource) error {
	if collector == nil || collector.heap == nil {
		return fmt.Errorf("collector is not initialized")
	}
	collector.heap.SetGCPhase(heap.GCPhaseAtomic)
	if err := collector.SeedRoots(vm, extra...); err != nil {
		return err
	}
	if err := collector.Propagate(); err != nil {
		return err
	}
	for {
		grayAgain := collector.heap.DrainGrayAgainQueue()
		if len(grayAgain) == 0 {
			break
		}
		for _, ref := range grayAgain {
			collector.heap.EnqueueGray(ref)
		}
		if err := collector.Propagate(); err != nil {
			return err
		}
	}
	if err := collector.heap.SetCurrentWhite(otherWhite(collector.heap.CurrentWhite())); err != nil {
		return err
	}
	collector.heap.SetGCPhase(heap.GCPhaseSweepStrings)
	return nil
}

func (collector *Collector) RunFullMark(vm *state.VMState, extra ...RootSource) error {
	if err := collector.StartCollection(); err != nil {
		return err
	}
	if err := collector.SeedRoots(vm, extra...); err != nil {
		return err
	}
	if err := collector.Propagate(); err != nil {
		return err
	}
	return collector.RunAtomic(vm, extra...)
}

func (collector *Collector) RunSweepStrings(table *rtstring.InternTable) (int, error) {
	if collector == nil || collector.heap == nil {
		return 0, fmt.Errorf("collector is not initialized")
	}
	if collector.heap.GCPhase() != heap.GCPhaseSweepStrings {
		return 0, fmt.Errorf("collector phase %d is not sweep strings", collector.heap.GCPhase())
	}
	removed := 0
	if table != nil {
		count, err := table.SweepDead(collector.isCurrentCycleDead)
		if err != nil {
			return 0, err
		}
		removed = count
	}
	collector.heap.SetGCPhase(heap.GCPhaseSweepObjects)
	return removed, nil
}

func (collector *Collector) RunSweepObjects() (SweepStats, error) {
	if collector == nil || collector.heap == nil {
		return SweepStats{}, fmt.Errorf("collector is not initialized")
	}
	if collector.heap.GCPhase() != heap.GCPhaseSweepObjects {
		return SweepStats{}, fmt.Errorf("collector phase %d is not sweep objects", collector.heap.GCPhase())
	}
	if !collector.sweeping {
		if err := collector.beginSweepPhase(); err != nil {
			return SweepStats{}, err
		}
	}
	for {
		done, err := collector.sweepSpansStep(0)
		if err != nil {
			return SweepStats{}, err
		}
		if done {
			return collector.finishSweepPhase(), nil
		}
	}
}

func (collector *Collector) WeakRefsSnapshot() []WeakRef {
	if collector == nil || len(collector.weakRefs) == 0 {
		return nil
	}
	return append([]WeakRef(nil), collector.weakRefs...)
}

func (collector *Collector) clearWeakRefs() {
	if collector == nil {
		return
	}
	collector.weakRefs = collector.weakRefs[:0]
	if collector.weakRefSet != nil {
		clear(collector.weakRefSet)
	}
}

func (collector *Collector) resetPrepareState() {
	if collector == nil {
		return
	}
	collector.prepareCursor = 0
	collector.prepareLimit = 0
	collector.preparing = false
}

func (collector *Collector) resetSweepState() {
	if collector == nil {
		return
	}
	collector.sweepCursor = 0
	collector.sweepLimit = 0
	collector.sweeping = false
	collector.sweepPayloads = nil
	collector.sweepPending = nil
	collector.sweepDeadWhite = 0
	collector.sweepNextWhite = 0
	collector.sweepStats = SweepStats{}
}

func (collector *Collector) snapshotSweepPayloads() error {
	collector.sweepPayloads = nil
	collector.sweepPending = nil
	return collector.heap.WalkSpans(func(offset value.HeapOff64, metadata heap.SpanMetadata) error {
		if metadata.State != heap.SpanStateLive || metadata.Kind != heap.SpanKindPayload || metadata.Owner == 0 {
			return nil
		}
		if collector.sweepPayloads == nil {
			collector.sweepPayloads = make(map[value.HeapOff64][]value.HeapOff64)
		}
		collector.sweepPayloads[metadata.Owner] = append(collector.sweepPayloads[metadata.Owner], offset)
		return nil
	})
}

func (collector *Collector) markSweepPayloads(owner value.HeapOff64) {
	if collector == nil || owner == 0 || collector.sweepPayloads == nil {
		return
	}
	offsets := collector.sweepPayloads[owner]
	if len(offsets) == 0 {
		return
	}
	if collector.sweepPending == nil {
		collector.sweepPending = make(map[value.HeapOff64]struct{}, len(offsets))
	}
	for _, offset := range offsets {
		collector.sweepPending[offset] = struct{}{}
	}
	delete(collector.sweepPayloads, owner)
}

func (collector *Collector) sweepPayloadMarked(offset value.HeapOff64) bool {
	if collector == nil || collector.sweepPending == nil {
		return false
	}
	_, ok := collector.sweepPending[offset]
	return ok
}

func (collector *Collector) clearSweepPayload(offset value.HeapOff64) {
	if collector == nil || collector.sweepPending == nil {
		return
	}
	delete(collector.sweepPending, offset)
}

func (collector *Collector) beginPreparePhase() {
	collector.clearWeakRefs()
	collector.resetPrepareState()
	collector.resetSweepState()
	collector.heap.SetGCPhase(heap.GCPhasePause)
	collector.prepareCursor = 0
	collector.prepareLimit = collector.heap.SpanCount()
	collector.preparing = true
}

func (collector *Collector) resetObjectMarksStep(budget uint64) (bool, error) {
	if collector == nil || collector.heap == nil {
		return false, fmt.Errorf("collector is not initialized")
	}
	if !collector.preparing {
		collector.beginPreparePhase()
	}
	currentWhite := collector.heap.CurrentWhite()
	next, done, err := collector.heap.WalkSpansChunk(collector.prepareCursor, collector.prepareLimit, budget, func(offset value.HeapOff64, metadata heap.SpanMetadata) error {
		if metadata.Kind != heap.SpanKindObject || metadata.State != heap.SpanStateLive {
			return nil
		}
		header, err := collector.heap.HeaderAtOffset(offset)
		if err != nil {
			return err
		}
		header.Mark = normalizeCycleMark(header.Mark, currentWhite)
		return collector.heap.WriteHeader(offset, header)
	})
	collector.prepareCursor = next
	if done {
		collector.preparing = false
	}
	return done, err
}

func (collector *Collector) shadeGray(ref value.HeapRef44) error {
	if ref == 0 {
		return nil
	}
	offset, header, err := collector.headerForRef(ref)
	if err != nil {
		return err
	}
	if header.Mark.Has(value.MarkGray) || header.Mark.Has(value.MarkBlack) {
		return nil
	}
	if !header.Mark.Has(collector.heap.CurrentWhite()) {
		return nil
	}
	header.Mark = header.Mark.With(value.MarkGray).Without(value.MarkWhite0 | value.MarkWhite1 | value.MarkBlack | value.MarkRemembered)
	if err := collector.heap.WriteHeader(offset, header); err != nil {
		return err
	}
	collector.heap.EnqueueGray(ref)
	return nil
}

func (collector *Collector) traceGrayObject(ref value.HeapRef44) error {
	offset, header, err := collector.headerForRef(ref)
	if err != nil {
		return err
	}
	if !header.Mark.Has(value.MarkGray) {
		return nil
	}
	header.Mark = header.Mark.With(value.MarkBlack).Without(value.MarkGray | value.MarkWhite0 | value.MarkWhite1 | value.MarkRemembered)
	if err := collector.heap.WriteHeader(offset, header); err != nil {
		return err
	}
	return collector.tracer.TraceObject(ref, collector.shadeGray, collector.recordWeakRef)
}

func (collector *Collector) recordWeakRef(edge WeakRef) error {
	if collector.weakRefSet == nil {
		collector.weakRefSet = make(map[WeakRef]struct{}, 8)
	}
	if _, ok := collector.weakRefSet[edge]; !ok {
		collector.weakRefSet[edge] = struct{}{}
		collector.weakRefs = append(collector.weakRefs, edge)
	}
	if edge.Owner != 0 {
		collector.heap.EnqueueWeak(edge.Owner)
	}
	return nil
}

func (collector *Collector) headerForRef(ref value.HeapRef44) (value.HeapOff64, value.CommonHeader, error) {
	address, err := collector.heap.DecodeHeapRef(ref)
	if err != nil {
		return 0, value.CommonHeader{}, err
	}
	offset, err := collector.heap.OffsetForAddress(address)
	if err != nil {
		return 0, value.CommonHeader{}, err
	}
	header, err := collector.heap.HeaderAtOffset(offset)
	if err != nil {
		return 0, value.CommonHeader{}, err
	}
	return offset, header, nil
}

func (collector *Collector) isCurrentCycleDead(ref value.HeapRef44) (bool, error) {
	return collector.isDeadWithWhite(collector.deadWhite(), ref)
}

func (collector *Collector) isDeadWithWhite(deadWhite value.MarkBits, ref value.HeapRef44) (bool, error) {
	_, header, err := collector.headerForRef(ref)
	if err != nil {
		return false, err
	}
	if header.Mark.Has(value.MarkGray) || header.Mark.Has(value.MarkBlack) {
		return false, nil
	}
	return header.Mark.Has(deadWhite), nil
}

func (collector *Collector) isDeadOrStale(deadWhite value.MarkBits, ref value.HeapRef44) bool {
	dead, err := collector.isDeadWithWhite(deadWhite, ref)
	return err != nil || dead
}

func normalizeCycleMark(mark value.MarkBits, currentWhite value.MarkBits) value.MarkBits {
	return mark.Without(value.MarkWhite0 | value.MarkWhite1 | value.MarkGray | value.MarkBlack | value.MarkRemembered).With(currentWhite)
}

func otherWhite(mark value.MarkBits) value.MarkBits {
	if mark == value.MarkWhite1 {
		return value.MarkWhite0
	}
	return value.MarkWhite1
}

func (collector *Collector) scrubWeakEdges(deadWhite value.MarkBits) (SweepStats, error) {
	stats := SweepStats{}
	feedbackOwners := make(map[value.HeapRef44]struct{})
	weakTableOwners := make(map[value.HeapRef44]struct{})
	for _, owner := range collector.heap.DrainWeakQueue() {
		if owner == 0 {
			continue
		}
		_, header, err := collector.headerForRef(owner)
		if err != nil {
			return SweepStats{}, err
		}
		switch header.Kind {
		case value.KindTable:
			weakTableOwners[owner] = struct{}{}
		case value.KindLuaClosure:
			feedbackOwners[owner] = struct{}{}
		}
	}
	for _, edge := range collector.weakRefs {
		switch edge.Kind {
		case WeakRefFeedbackCellHeapRef, WeakRefFeedbackCellValueBits:
			if edge.Owner != 0 {
				feedbackOwners[edge.Owner] = struct{}{}
			}
		case WeakRefWeakTableKey, WeakRefWeakTableValue:
			if edge.Owner != 0 {
				weakTableOwners[edge.Owner] = struct{}{}
			}
		}
	}
	for tableRef := range weakTableOwners {
		removed, err := collector.tracer.ScrubDeadWeakTableEntries(tableRef, func(ref value.HeapRef44) bool {
			return collector.isDeadOrStale(deadWhite, ref)
		})
		if err != nil {
			return SweepStats{}, err
		}
		stats.ClearedWeakTableEdges += removed
	}
	for closureRef := range feedbackOwners {
		scrubbed, err := collector.tracer.ScrubDeadFeedbackCells(closureRef, func(ref value.HeapRef44) bool {
			return collector.isDeadOrStale(deadWhite, ref)
		})
		if err != nil {
			return SweepStats{}, err
		}
		stats.ScrubbedFeedbackCells += scrubbed
	}
	return stats, nil
}

func (collector *Collector) beginSweepPhase() error {
	deadWhite := otherWhite(collector.heap.CurrentWhite())
	stats, err := collector.scrubWeakEdges(deadWhite)
	if err != nil {
		return err
	}
	if err := collector.snapshotSweepPayloads(); err != nil {
		return err
	}
	collector.clearWeakRefs()
	collector.sweepCursor = 0
	collector.sweepLimit = collector.heap.SpanCount()
	collector.sweeping = true
	collector.sweepDeadWhite = deadWhite
	collector.sweepNextWhite = collector.heap.CurrentWhite()
	collector.sweepStats = stats
	return nil
}

func (collector *Collector) sweepSpansStep(budget uint64) (bool, error) {
	if collector == nil || collector.heap == nil {
		return false, fmt.Errorf("collector is not initialized")
	}
	if !collector.sweeping {
		if err := collector.beginSweepPhase(); err != nil {
			return false, err
		}
	}
	next, done, err := collector.heap.WalkSpansChunk(collector.sweepCursor, collector.sweepLimit, budget, func(offset value.HeapOff64, metadata heap.SpanMetadata) error {
		if metadata.State != heap.SpanStateLive {
			return nil
		}
		switch metadata.Kind {
		case heap.SpanKindPayload:
			if collector.sweepPayloadMarked(offset) {
				collector.clearSweepPayload(offset)
				if err := collector.heap.FreeSpan(offset); err != nil {
					return err
				}
				collector.sweepStats.FreedPayloads++
				return nil
			}
			if metadata.Owner == 0 {
				return nil
			}
			dead, err := collector.ownerIsDead(metadata.Owner, collector.sweepDeadWhite)
			if err != nil {
				return err
			}
			if !dead {
				return nil
			}
			if err := collector.heap.FreeSpan(offset); err != nil {
				return err
			}
			collector.sweepStats.FreedPayloads++
		case heap.SpanKindObject:
			header, err := collector.heap.HeaderAtOffset(offset)
			if err != nil {
				return err
			}
			if header.Mark.Has(value.MarkGray) || header.Mark.Has(value.MarkBlack) {
				header.Mark = normalizeCycleMark(header.Mark, collector.sweepNextWhite)
				return collector.heap.WriteHeader(offset, header)
			}
			if header.Mark.Has(collector.sweepDeadWhite) {
				collector.markSweepPayloads(offset)
				if header.Kind == value.KindHostObject || header.Kind == value.KindHostFunction {
					if err := collector.finalizeHostWrapper(offset); err != nil {
						return err
					}
					collector.sweepStats.FinalizedHostWrappers++
				}
				if err := collector.heap.FreeSpan(offset); err != nil {
					return err
				}
				collector.sweepStats.FreedObjects++
				return nil
			}
			header.Mark = normalizeCycleMark(header.Mark, collector.sweepNextWhite)
			return collector.heap.WriteHeader(offset, header)
		}
		return nil
	})
	collector.sweepCursor = next
	if done {
		lateStats, lateErr := collector.scrubWeakEdges(collector.sweepDeadWhite)
		if lateErr != nil {
			return false, lateErr
		}
		collector.sweepStats.ScrubbedFeedbackCells += lateStats.ScrubbedFeedbackCells
		collector.sweepStats.ClearedWeakTableEdges += lateStats.ClearedWeakTableEdges
		collector.sweeping = false
	}
	return done, err
}

func (collector *Collector) ownerIsDead(owner value.HeapOff64, deadWhite value.MarkBits) (bool, error) {
	metadata, err := collector.heap.SpanMetadata(owner)
	if err != nil {
		return false, err
	}
	if metadata.State != heap.SpanStateLive || metadata.Kind != heap.SpanKindObject {
		return true, nil
	}
	header, err := collector.heap.HeaderAtOffset(owner)
	if err != nil {
		return false, err
	}
	if header.Mark.Has(value.MarkGray) || header.Mark.Has(value.MarkBlack) {
		return false, nil
	}
	return header.Mark.Has(deadWhite), nil
}

func (collector *Collector) finishSweepPhase() SweepStats {
	stats := collector.sweepStats
	collector.clearWeakRefs()
	collector.resetSweepState()
	collector.heap.SetGCPhase(heap.GCPhasePause)
	collector.heap.RearmGCThreshold()
	return stats
}

func (collector *Collector) finalizeHostWrapper(offset value.HeapOff64) error {
	if collector.tracer == nil || collector.tracer.hosts == nil {
		return nil
	}
	address, err := collector.heap.AddressForOffset(offset)
	if err != nil {
		return err
	}
	ref, err := collector.heap.EncodeHeapRef(address)
	if err != nil {
		return err
	}
	header, _, err := collector.tracer.objectBytes(ref)
	if err != nil {
		return err
	}
	var wrapper host.WrapperHeader
	switch header.Kind {
	case value.KindHostObject:
		wrapper, _, _, err = collector.tracer.hosts.ReadHostObject(ref)
	case value.KindHostFunction:
		wrapper, _, _, err = collector.tracer.hosts.ReadHostFunction(ref)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	return collector.tracer.hosts.Release(host.Handle(wrapper.HostHandle))
}

func (collector *Collector) stepOnce() error {
	if collector == nil || collector.heap == nil || collector.vm == nil {
		return fmt.Errorf("collector runtime is not bound")
	}
	switch collector.Phase() {
	case heap.GCPhasePause:
		done, err := collector.resetObjectMarksStep(collector.StepBudget())
		if err != nil {
			return err
		}
		if !done {
			return nil
		}
		collector.heap.SetGCPhase(heap.GCPhaseMark)
		return collector.SeedRoots(collector.vm)
	case heap.GCPhaseMark:
		if err := collector.Propagate(); err != nil {
			return err
		}
		collector.heap.SetGCPhase(heap.GCPhaseAtomic)
		return nil
	case heap.GCPhaseAtomic:
		return collector.RunAtomic(collector.vm)
	case heap.GCPhaseSweepStrings:
		_, err := collector.RunSweepStrings(collector.strings)
		return err
	case heap.GCPhaseSweepObjects:
		done, err := collector.sweepSpansStep(collector.StepBudget())
		if err != nil {
			return err
		}
		if done {
			collector.finishSweepPhase()
		}
		return nil
	case heap.GCPhaseFinalize:
		collector.heap.SetGCPhase(heap.GCPhasePause)
		collector.heap.RearmGCThreshold()
		collector.allocDebt = 0
		return nil
	default:
		return fmt.Errorf("unknown collector phase %d", collector.Phase())
	}
}

func (collector *Collector) deadWhite() value.MarkBits {
	if collector == nil || collector.heap == nil {
		return value.MarkWhite0
	}
	switch collector.heap.GCPhase() {
	case heap.GCPhaseSweepStrings, heap.GCPhaseSweepObjects, heap.GCPhaseFinalize:
		return otherWhite(collector.heap.CurrentWhite())
	default:
		return collector.heap.CurrentWhite()
	}
}
