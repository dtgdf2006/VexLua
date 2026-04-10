// Package amd64 contains amd64-specific VexArc code generation helpers.
package amd64

type Register uint8

type XMMRegister uint8

const (
	RegRAX Register = iota
	RegRCX
	RegRDX
	RegRBX
	RegRSP
	RegRBP
	RegRSI
	RegRDI
	RegR8
	RegR9
	RegR10
	RegR11
	RegR12
	RegR13
	RegR14
	RegR15
)

const (
	HeapBaseRegister  = RegR15
	VMStateRegister   = RegR14
	CallFrameRegister = RegR13
	RegsBaseRegister  = RegR12
)

const (
	XMM0 XMMRegister = iota
	XMM1
	XMM2
	XMM3
	XMM4
	XMM5
	XMM6
	XMM7
	XMM8
	XMM9
	XMM10
	XMM11
	XMM12
	XMM13
	XMM14
	XMM15
)
