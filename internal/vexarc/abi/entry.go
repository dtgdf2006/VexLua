package abi

import "unsafe"

func enterCompiled(entry uintptr, heapBase uintptr, vmState unsafe.Pointer, frame unsafe.Pointer, regsBase uintptr, execCtx unsafe.Pointer) (status uint64, aux uint64)

func EnterCompiled(entry uintptr, heapBase uintptr, vmState unsafe.Pointer, frame unsafe.Pointer, regsBase uintptr, execCtx unsafe.Pointer) (status uint64, aux uint64) {
	return enterCompiled(entry, heapBase, vmState, frame, regsBase, execCtx)
}
