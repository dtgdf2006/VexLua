//go:build amd64

package vexarc

import (
	"errors"

	"vexlua/internal/bytecode"
	"vexlua/internal/jit"
)

func planRetryLaterRegion(req jit.CompileRequest) (jit.CompileRequest, bool) {
	region, ok := longestNativeRegion(req.Proto)
	if !ok || !region.ValidFor(req.Proto) {
		return jit.CompileRequest{}, false
	}
	return jit.CompileRequest{Proto: req.Proto, Mode: jit.CompileRegion, Region: region}, true
}

func longestNativeRegion(proto *bytecode.Proto) (jit.Region, bool) {
	if proto == nil || len(proto.Code) == 0 {
		return jit.Region{}, false
	}
	bestStart, bestEnd := -1, -1
	currentStart := -1
	commit := func(endPC int) {
		if currentStart < 0 {
			return
		}
		if endPC-currentStart > bestEnd-bestStart {
			bestStart = currentStart
			bestEnd = endPC
		}
	}
	for pc := 0; pc < len(proto.Code); {
		span, err := nativeInstrSpan(proto, pc, len(proto.Code))
		if err == nil {
			if currentStart < 0 {
				currentStart = pc
			}
			pc += span
			continue
		}
		commit(pc)
		currentStart = -1
		if !errors.Is(err, jit.ErrRetryLater) {
			return jit.Region{}, false
		}
		pc++
	}
	commit(len(proto.Code))
	if bestStart < 0 || bestEnd <= bestStart {
		return jit.Region{}, false
	}
	return jit.Region{ID: 1, StartPC: bestStart, EndPC: bestEnd}, true
}
