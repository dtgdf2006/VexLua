// Package abi contains platform bridge code between VexArc and external ABIs.
package abi

type BuiltinResult uint32

const (
	WindowsShadowSpaceSize = 32
	StackAlignment         = 16
	StubCallBlockSize      = 0x30
)

const (
	BuiltinResultContinue BuiltinResult = iota
	BuiltinResultDispatchToRuntime
	BuiltinResultDeopt
	BuiltinResultReturn
	BuiltinResultError
)
