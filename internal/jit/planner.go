package jit

import "vexlua/internal/bytecode"

type HotCounters struct {
	Runs         uint32
	QuickenedOps int
	HotPC        int
	HotPCCount   uint32
}

type Planner interface {
	Plan(proto *bytecode.Proto, counters HotCounters) (CompileRequest, bool)
}

type WholeProtoPlanner struct{}

func (WholeProtoPlanner) Plan(proto *bytecode.Proto, counters HotCounters) (CompileRequest, bool) {
	if proto == nil || len(proto.Code) == 0 || counters.Runs == 0 {
		return CompileRequest{}, false
	}
	return CompileRequest{Proto: proto, Mode: CompileWholeProto}, true
}
