package interp

import "vexlua/internal/runtime/state"

func (engine *Engine) WalkThreadFrames(visit func(thread *state.ThreadState, frame *state.CallFrameHeader, top uint32) error) error {
	if engine.State == nil {
		return nil
	}
	for _, thread := range engine.State.Threads() {
		if err := thread.SyncCurrentFrameFromNative(); err != nil {
			return err
		}
		for frame := thread.CurrentFrame(); frame != nil; {
			if err := visit(thread, frame, uint32(frame.Top)); err != nil {
				return err
			}
			if frame.PrevFrame == 0 {
				break
			}
			previous, err := thread.FrameAtAddress(uintptr(frame.PrevFrame))
			if err != nil {
				return err
			}
			frame = previous
		}
	}
	return nil
}
