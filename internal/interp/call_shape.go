package interp

import (
	"fmt"

	"vexlua/internal/runtime/feedback"
	rtmeta "vexlua/internal/runtime/meta"
	"vexlua/internal/runtime/value"
)

func (engine *Engine) describeCallShape(callee value.TValue) (feedback.CallShape, error) {
	if isCallableBoundaryValue(callee) {
		return feedback.CallShape{Kind: feedback.CallShapeDirect}, nil
	}
	if callee.IsBoxedTag(value.TagTableRef) {
		return engine.describeTableCallShape(callee)
	}
	if callee.IsBoxedTag(value.TagHostObjectRef) {
		return engine.describeHostObjectCallShape(callee)
	}
	return engine.describeTypeMetatableCallShape(callee)
}

func (engine *Engine) describeTableCallShape(callee value.TValue) (feedback.CallShape, error) {
	ref, _ := callee.HeapRef()
	object, err := engine.Tables.Object(ref)
	if err != nil {
		return feedback.CallShape{}, err
	}
	metatableVersion, err := engine.metatableTableVersion(object.Metatable)
	if err != nil {
		return feedback.CallShape{}, err
	}
	return feedback.CallShape{Kind: feedback.CallShapeTableMetatable, VersionA: object.TableVersion, VersionB: metatableVersion}, nil
}

func (engine *Engine) describeHostObjectCallShape(callee value.TValue) (feedback.CallShape, error) {
	ref, _ := callee.HeapRef()
	header, _, _, err := engine.Hosts.ReadHostObject(ref)
	if err != nil {
		return feedback.CallShape{}, err
	}
	if header.Metatable.IsBoxedTag(value.TagNil) {
		return engine.describeTypeMetatableCallShape(callee)
	}
	metatableVersion, err := engine.metatableTableVersion(header.Metatable)
	if err != nil {
		return feedback.CallShape{}, err
	}
	return feedback.CallShape{Kind: feedback.CallShapeHostObjectMetatable, VersionA: header.MetatableVersion, VersionB: metatableVersion}, nil
}

func (engine *Engine) describeTypeMetatableCallShape(callee value.TValue) (feedback.CallShape, error) {
	kind, ok := rtmeta.KindForValue(callee)
	if !ok {
		return feedback.CallShape{}, fmt.Errorf("cannot describe call shape for %s", callee)
	}
	metatable, found := engine.Meta.Get(kind)
	version := engine.Meta.Version(kind)
	if !found {
		return feedback.CallShape{Kind: feedback.CallShapeTypeMetatable, VersionA: version}, nil
	}
	metatableVersion, err := engine.metatableTableVersion(metatable)
	if err != nil {
		return feedback.CallShape{}, err
	}
	return feedback.CallShape{Kind: feedback.CallShapeTypeMetatable, VersionA: version, VersionB: metatableVersion}, nil
}

func (engine *Engine) metatableTableVersion(metatable value.TValue) (uint32, error) {
	if metatable.IsBoxedTag(value.TagNil) {
		return 0, nil
	}
	if !metatable.IsBoxedTag(value.TagTableRef) {
		return 0, fmt.Errorf("metatable must be table or nil, got %s", metatable)
	}
	ref, _ := metatable.HeapRef()
	object, err := engine.Tables.Object(ref)
	if err != nil {
		return 0, err
	}
	return object.TableVersion, nil
}

func (engine *Engine) MatchCallFeedbackCell(callee value.TValue, cell feedback.Cell) (value.TValue, bool, error) {
	switch cell.State {
	case feedback.StateMonomorphic:
		return engine.matchCallFeedbackEntry(callee, feedback.NewCallPolymorphicEntry(cell.AccessKind, cell.TargetRef(), cell.ValueBits, feedback.CallShape{Kind: cell.CallShapeKind(), VersionA: cell.CallShapeVersionA(), VersionB: cell.CallShapeVersionB()}))
	case feedback.StatePolymorphic:
		entries, err := engine.readCallPolymorphicEntries(cell.CallPolymorphicDataOffset())
		if err != nil {
			return value.NilValue(), false, err
		}
		for _, entry := range entries {
			matched, ok, err := engine.matchCallFeedbackEntry(callee, entry)
			if err != nil || ok {
				return matched, ok, err
			}
		}
		return value.NilValue(), false, nil
	case feedback.StateMegamorphic:
		if cell.HasMegamorphicCallSidecar() {
			entries, err := engine.readCallMegamorphicEntries(cell.CallMegamorphicDataOffset())
			if err != nil {
				return value.NilValue(), false, err
			}
			for _, entry := range entries {
				matched, ok, err := engine.matchCallFeedbackEntry(callee, entry)
				if err != nil || ok {
					return matched, ok, err
				}
			}
			return value.NilValue(), false, nil
		}
		return engine.matchCallFeedbackEntry(callee, feedback.NewCallPolymorphicEntry(cell.AccessKind, cell.TargetRef(), cell.ValueBits, feedback.CallShape{Kind: cell.CallShapeKind(), VersionA: cell.CallShapeVersionA(), VersionB: cell.CallShapeVersionB()}))
	default:
		return value.NilValue(), false, nil
	}
	return value.NilValue(), false, nil
}

func (engine *Engine) matchCallFeedbackEntry(callee value.TValue, entry feedback.CallPolymorphicEntry) (value.TValue, bool, error) {
	if entry.ValueBits != callee.Bits() {
		return value.NilValue(), false, nil
	}
	switch entry.AccessKind {
	case feedback.AccessCallLuaClosure:
		if !callee.IsBoxedTag(value.TagLuaClosureRef) {
			return value.NilValue(), false, nil
		}
		ref, _ := callee.HeapRef()
		if ref != entry.TargetRef {
			return value.NilValue(), false, nil
		}
		return callee, true, nil
	case feedback.AccessCallHostFunction:
		if !callee.IsBoxedTag(value.TagHostFunctionRef) {
			return value.NilValue(), false, nil
		}
		ref, _ := callee.HeapRef()
		if ref != entry.TargetRef {
			return value.NilValue(), false, nil
		}
		return callee, true, nil
	case feedback.AccessCallResolvedLuaClosure, feedback.AccessCallResolvedHostFunction:
		shape, err := engine.describeCallShape(callee)
		if err != nil {
			return value.NilValue(), false, err
		}
		if shape.Kind != entry.Shape.Kind || shape.VersionA != entry.Shape.VersionA || shape.VersionB != entry.Shape.VersionB {
			return value.NilValue(), false, nil
		}
		if entry.AccessKind == feedback.AccessCallResolvedLuaClosure {
			return value.LuaClosureRefValue(entry.TargetRef), true, nil
		}
		return value.HostFunctionRefValue(entry.TargetRef), true, nil
	default:
		return value.NilValue(), false, nil
	}
}
