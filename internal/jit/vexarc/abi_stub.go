//go:build !amd64

package vexarc

import "vexlua/internal/jit"

func invokeEnterABI(entry uintptr, thread *jit.NativeThreadState, frame *jit.NativeFrameState) jit.NativeExitRecord {
	if thread != nil {
		thread.PendingExit = uint32(jit.ExitInterpret)
		thread.LastExit = jit.NativeExitRecord{Reason: jit.ExitInterpret}
		if frame != nil {
			thread.LastExit.ResumePC = frame.PC
		}
		return thread.LastExit
	}
	exit := jit.NativeExitRecord{Reason: jit.ExitInterpret}
	if frame != nil {
		exit.ResumePC = frame.PC
	}
	return exit
}
