package interp

import (
	"fmt"
	"math"
	"strings"

	rtlua "vexlua/internal/runtime/lua"
	rtmeta "vexlua/internal/runtime/meta"
	"vexlua/internal/runtime/state"
	rtstring "vexlua/internal/runtime/string"
	"vexlua/internal/runtime/value"
)

const metaConcatName = "__concat"

func (engine *Engine) ConcatBoundary(thread *state.ThreadState, values []value.TValue) (value.TValue, error) {
	if len(values) == 0 {
		empty, err := engine.Strings.Intern("")
		if err != nil {
			return value.NilValue(), err
		}
		return empty.Value, nil
	}

	working := append([]value.TValue(nil), values...)
	total := len(working)
	last := total - 1
	for total > 1 {
		left := working[last-1]
		right := working[last]
		leftText, leftStringLike, err := rtlua.ToStringText(left, engine.Strings.Text)
		if err != nil {
			return value.NilValue(), err
		}
		rightText, rightStringLike, err := rtlua.ToStringText(right, engine.Strings.Text)
		if err != nil {
			return value.NilValue(), err
		}

		handledCount := 2
		if leftStringLike && rightStringLike {
			result, count, err := engine.concatStringOperands(working[:total], last, leftText, rightText)
			if err != nil {
				return value.NilValue(), err
			}
			working[last-count+1] = result
			handledCount = count
		} else {
			result, handled, err := engine.callConcatMetamethod(thread, left, right)
			if err != nil {
				return value.NilValue(), err
			}
			if !handled {
				return value.NilValue(), concatBoundaryTypeError(left, right)
			}
			working[last-1] = result
		}

		total -= handledCount - 1
		last -= handledCount - 1
	}
	return working[0], nil
}

func (engine *Engine) concatStringOperands(working []value.TValue, last int, leftText string, rightText string) (value.TValue, int, error) {
	parts := []string{rightText, leftText}
	totalLength := uint64(len(rightText) + len(leftText))
	handledCount := 2
	for handledCount < len(working) {
		index := last - handledCount
		partText, ok, err := rtlua.ToStringText(working[index], engine.Strings.Text)
		if err != nil {
			return value.NilValue(), 0, err
		}
		if !ok {
			break
		}
		if totalLength > math.MaxUint64-uint64(len(partText)) {
			return value.NilValue(), 0, fmt.Errorf("string length overflow")
		}
		totalLength += uint64(len(partText))
		parts = append(parts, partText)
		handledCount++
	}
	if err := validateConcatLength(totalLength); err != nil {
		return value.NilValue(), 0, err
	}
	var builder strings.Builder
	builder.Grow(int(totalLength))
	for index := len(parts) - 1; index >= 0; index-- {
		builder.WriteString(parts[index])
	}
	handle, err := engine.Strings.Intern(builder.String())
	if err != nil {
		return value.NilValue(), 0, err
	}
	return handle.Value, handledCount, nil
}

func validateConcatLength(totalLength uint64) error {
	if totalLength > math.MaxInt {
		return fmt.Errorf("string length overflow")
	}
	if _, err := rtstring.LayoutSize(int(totalLength)); err != nil {
		return fmt.Errorf("string length overflow")
	}
	return nil
}

func (engine *Engine) callConcatMetamethod(thread *state.ThreadState, left value.TValue, right value.TValue) (value.TValue, bool, error) {
	metamethod, ok, err := engine.valueMetamethod(left, metaConcatName)
	if err != nil {
		return value.NilValue(), false, err
	}
	if !ok && left.Bits() != right.Bits() {
		metamethod, ok, err = engine.valueMetamethod(right, metaConcatName)
		if err != nil {
			return value.NilValue(), false, err
		}
	}
	if !ok {
		return value.NilValue(), false, nil
	}
	results, err := engine.CallValueBoundary(thread, metamethod, []value.TValue{left, right}, 1)
	if err != nil {
		return value.NilValue(), false, err
	}
	if len(results) == 0 {
		return value.NilValue(), true, nil
	}
	return results[0], true, nil
}

func concatBoundaryTypeError(left value.TValue, right value.TValue) error {
	blame := left
	if left.IsNumber() || left.IsBoxedTag(value.TagStringRef) {
		blame = right
	}
	return fmt.Errorf("attempt to concatenate a %s value", rtmeta.TypeName(blame))
}
