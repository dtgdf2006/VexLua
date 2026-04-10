//go:build amd64

package vexarc

import "vexlua/internal/jit"

func enterVexarcABI(entry uintptr, slotsBase uintptr, thread *jit.NativeThreadState, frame *jit.NativeFrameState, exit *jit.NativeExitRecord)

func invokeEnterABI(entry uintptr, thread *jit.NativeThreadState, frame *jit.NativeFrameState) jit.NativeExitRecord {
	if thread != nil {
		thread.LastExit = jit.NativeExitRecord{Reason: jit.ExitInterpret}
		if frame != nil {
			thread.LastExit.ResumePC = frame.PC
		}
		if entry == 0 {
			thread.PendingExit = uint32(jit.ExitInterpret)
			return thread.LastExit
		}
		enterVexarcABI(entry, frame.SlotsBase, thread, frame, &thread.LastExit)
		thread.PendingExit = uint32(thread.LastExit.Reason)
		return thread.LastExit
	}
	exit := jit.NativeExitRecord{Reason: jit.ExitInterpret}
	if frame != nil {
		exit.ResumePC = frame.PC
	}
	if entry == 0 {
		return exit
	}
	enterVexarcABI(entry, frame.SlotsBase, thread, frame, &exit)
	return exit
}
