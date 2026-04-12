package interp

import "vexlua/internal/runtime/state"

func (engine *Engine) WalkActivationFrames(visit func(thread *state.ThreadState, frame *state.CallFrameHeader, top uint32) error) error {
	if engine == nil || visit == nil {
		return nil
	}
	for _, ctx := range engine.threads {
		for _, act := range ctx.activations {
			if act == nil || act.thread == nil || act.frame == nil {
				continue
			}
			if err := visit(act.thread, act.frame, act.top); err != nil {
				return err
			}
		}
	}
	return nil
}
