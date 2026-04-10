//go:build !amd64

package vexarc

import "vexlua/internal/jit"

func planRetryLaterRegion(req jit.CompileRequest) (jit.CompileRequest, bool) {
	_ = req
	return jit.CompileRequest{}, false
}
