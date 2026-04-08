package stdlib

import (
	"fmt"
	"strings"
	"sync"

	"vexlua/internal/chunk51"
	rt "vexlua/internal/runtime"
	"vexlua/internal/vm"
)

const (
	stringGSubReplacementLiteral = -2
	stringGSubReplacementFull    = -1
)

type stringGSubReplacementPart struct {
	literal      string
	captureIndex int
}

type stringGSubReplacementPlan struct {
	parts []stringGSubReplacementPart
}

type stringGSubStaticReplacement struct {
	literal string
	plan    *stringGSubReplacementPlan
}

var stringGSubReplacementCache sync.Map

func registerString(runtime *rt.Runtime, machine *vm.VM) error {
	handle := runtime.Heap().NewTable(8)
	table := runtime.Heap().Table(handle)
	stringValue := rt.HandleValue(handle)
	set := func(name string, value rt.Value) {
		table.SetSymbol(runtime.InternSymbol(name), value)
	}
	set("len", runtime.NewHostFunction("string.len", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("string.len expects 1 argument")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("string.len expects string or number")
		}
		return rt.NumberValue(float64(len(s))), nil
	}))
	set("sub", runtime.NewHostFunction("string.sub", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return rt.NilValue, fmt.Errorf("string.sub expects 2 or 3 arguments")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("string.sub expects string or number")
		}
		if !args[1].IsNumber() {
			return rt.NilValue, fmt.Errorf("string.sub start expects number")
		}
		start, end := luaStringRange(len(s), int(args[1].Number()), len(s))
		if len(args) == 3 {
			if !args[2].IsNumber() {
				return rt.NilValue, fmt.Errorf("string.sub end expects number")
			}
			start, end = luaStringRange(len(s), int(args[1].Number()), int(args[2].Number()))
		}
		if start > end {
			return runtime.StringValue(""), nil
		}
		return runtime.StringValue(s[start-1 : end]), nil
	}))
	set("lower", runtime.NewHostFunction("string.lower", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("string.lower expects 1 argument")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("string.lower expects string or number")
		}
		return runtime.StringValue(strings.ToLower(s)), nil
	}))
	set("upper", runtime.NewHostFunction("string.upper", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("string.upper expects 1 argument")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("string.upper expects string or number")
		}
		return runtime.StringValue(strings.ToUpper(s)), nil
	}))
	set("byte", runtime.NewHostFunctionMulti("string.byte", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) < 1 || len(args) > 3 {
			return nil, fmt.Errorf("string.byte expects 1 to 3 arguments")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return nil, fmt.Errorf("string.byte expects string or number")
		}
		start := 1
		finish := 1
		if len(args) >= 2 {
			if !args[1].IsNumber() {
				return nil, fmt.Errorf("string.byte start expects number")
			}
			start = int(args[1].Number())
			finish = start
		}
		if len(args) == 3 {
			if !args[2].IsNumber() {
				return nil, fmt.Errorf("string.byte end expects number")
			}
			finish = int(args[2].Number())
		}
		start, end := luaStringRange(len(s), start, finish)
		if start > end {
			return nil, nil
		}
		results := make([]rt.Value, 0, end-start+1)
		for i := start - 1; i < end; i++ {
			results = append(results, rt.NumberValue(float64(s[i])))
		}
		return results, nil
	}))
	set("char", runtime.NewHostFunction("string.char", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		buf := make([]byte, 0, len(args))
		for _, arg := range args {
			if !arg.IsNumber() {
				return rt.NilValue, fmt.Errorf("string.char expects numbers")
			}
			code := int(arg.Number())
			if code < 0 || code > 255 {
				return rt.NilValue, fmt.Errorf("string.char value out of range")
			}
			buf = append(buf, byte(code))
		}
		return runtime.StringValue(string(buf)), nil
	}))
	set("reverse", runtime.NewHostFunction("string.reverse", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("string.reverse expects 1 argument")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("string.reverse expects string or number")
		}
		buf := make([]byte, len(s))
		for i := range s {
			buf[len(s)-1-i] = s[i]
		}
		return runtime.StringValue(string(buf)), nil
	}))
	set("rep", runtime.NewHostFunction("string.rep", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 2 {
			return rt.NilValue, fmt.Errorf("string.rep expects 2 arguments")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("string.rep expects string or number")
		}
		if !args[1].IsNumber() {
			return rt.NilValue, fmt.Errorf("string.rep count expects number")
		}
		count := int(args[1].Number())
		if count <= 0 {
			return runtime.StringValue(""), nil
		}
		return runtime.StringValue(strings.Repeat(s, count)), nil
	}))
	set("find", runtime.NewHostFunctionMulti("string.find", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) < 2 || len(args) > 4 {
			return nil, fmt.Errorf("string.find expects 2 to 4 arguments")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return nil, fmt.Errorf("string.find expects string or number")
		}
		pattern, ok := concatString(runtime, args[1])
		if !ok {
			return nil, fmt.Errorf("string.find pattern expects string or number")
		}
		start := 1
		if len(args) >= 3 {
			if !args[2].IsNumber() {
				return nil, fmt.Errorf("string.find init expects number")
			}
			start = int(args[2].Number())
		}
		plain := len(args) == 4 && isTruthy(args[3])
		return stringFind(runtime, s, pattern, start, plain)
	}))
	set("match", runtime.NewHostFunctionMulti("string.match", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("string.match expects 2 or 3 arguments")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return nil, fmt.Errorf("string.match expects string or number")
		}
		pattern, ok := concatString(runtime, args[1])
		if !ok {
			return nil, fmt.Errorf("string.match pattern expects string or number")
		}
		start := 1
		if len(args) == 3 {
			if !args[2].IsNumber() {
				return nil, fmt.Errorf("string.match init expects number")
			}
			start = int(args[2].Number())
		}
		return stringMatch(runtime, s, pattern, start)
	}))
	gmatchFunc := runtime.NewHostFunction("string.gmatch", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return rt.NilValue, fmt.Errorf("string.gmatch expects 2 or 3 arguments")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("string.gmatch expects string or number")
		}
		pattern, ok := concatString(runtime, args[1])
		if !ok {
			return rt.NilValue, fmt.Errorf("string.gmatch pattern expects string or number")
		}
		start := 1
		if len(args) == 3 {
			if !args[2].IsNumber() {
				return rt.NilValue, fmt.Errorf("string.gmatch init expects number")
			}
			start = int(args[2].Number())
		}
		return stringGMatch(runtime, s, pattern, start)
	})
	set("gmatch", gmatchFunc)
	set("gfind", gmatchFunc)
	set("gsub", runtime.NewHostFunctionMulti("string.gsub", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) < 3 || len(args) > 4 {
			return nil, fmt.Errorf("string.gsub expects 3 or 4 arguments")
		}
		s, ok := concatString(runtime, args[0])
		if !ok {
			return nil, fmt.Errorf("string.gsub expects string or number")
		}
		pattern, ok := concatString(runtime, args[1])
		if !ok {
			return nil, fmt.Errorf("string.gsub pattern expects string or number")
		}
		limit := -1
		if len(args) == 4 {
			if !args[3].IsNumber() {
				return nil, fmt.Errorf("string.gsub count expects number")
			}
			limit = int(args[3].Number())
		}
		return stringGSub(runtime, machine, s, pattern, args[2], limit)
	}))
	set("format", runtime.NewHostFunction("string.format", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) == 0 {
			return rt.NilValue, fmt.Errorf("string.format expects format string")
		}
		format, ok := concatString(runtime, args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("string.format expects string or number format")
		}
		text, err := stringFormat(runtime, format, args[1:])
		if err != nil {
			return rt.NilValue, err
		}
		return runtime.StringValue(text), nil
	}))
	set("dump", runtime.NewHostFunction("string.dump", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("string.dump expects 1 argument")
		}
		h, ok := args[0].Handle()
		if !ok {
			return rt.NilValue, fmt.Errorf("string.dump expects function")
		}
		if h.Kind() == rt.ObjectHostFunction {
			return rt.NilValue, fmt.Errorf("unable to dump given function")
		}
		if h.Kind() != rt.ObjectLuaClosure {
			return rt.NilValue, fmt.Errorf("string.dump expects function")
		}
		closure := runtime.Heap().LuaClosure(h).(*vm.LuaClosure)
		data, err := chunk51.Dump(runtime, closure.Proto)
		if err != nil {
			return rt.NilValue, err
		}
		return runtime.StringValue(string(data)), nil
	}))
	metaHandle := runtime.Heap().NewTable(1)
	metaTable := runtime.Heap().Table(metaHandle)
	metaTable.SetSymbol(runtime.InternSymbol("__index"), stringValue)
	if err := runtime.SetStringMetatable(rt.HandleValue(metaHandle)); err != nil {
		return err
	}
	runtime.SetGlobal("string", stringValue)
	return nil
}

func stringFind(runtime *rt.Runtime, s string, pattern string, start int, plain bool) ([]rt.Value, error) {
	searcher, err := compileStringPattern(pattern, plain)
	if err != nil {
		return nil, err
	}
	start = luaStringStart(len(s), start)
	if searcher.re != nil {
		indices := searcher.findRegexIndices(s, start-1)
		if indices == nil {
			return []rt.Value{rt.NilValue}, nil
		}
		results := make([]rt.Value, 2+searcher.captures)
		results[0] = rt.NumberValue(float64(indices[0] + 1))
		results[1] = rt.NumberValue(float64(indices[1]))
		for i := 0; i < searcher.captures; i++ {
			captureStart := indices[2+i*2]
			captureEnd := indices[3+i*2]
			if captureStart < 0 {
				results[2+i] = runtime.StringValue("")
				continue
			}
			results[2+i] = runtime.StringValue(s[captureStart:captureEnd])
		}
		return results, nil
	}
	match, err := searcher.find(s, start-1)
	if err != nil {
		return nil, err
	}
	if match == nil {
		return []rt.Value{rt.NilValue}, nil
	}
	results := make([]rt.Value, 2+len(match.captures))
	results[0] = rt.NumberValue(float64(match.start + 1))
	results[1] = rt.NumberValue(float64(match.end))
	for i, capture := range match.captures {
		results[2+i] = stringCaptureToValue(runtime, capture)
	}
	return results, nil
}

func stringMatch(runtime *rt.Runtime, s string, pattern string, start int) ([]rt.Value, error) {
	searcher, err := compileStringPattern(pattern, false)
	if err != nil {
		return nil, err
	}
	start = luaStringStart(len(s), start)
	if searcher.re != nil {
		indices := searcher.findRegexIndices(s, start-1)
		if indices == nil {
			return []rt.Value{rt.NilValue}, nil
		}
		if searcher.captures == 0 {
			return []rt.Value{runtime.StringValue(s[indices[0]:indices[1]])}, nil
		}
		results := make([]rt.Value, searcher.captures)
		for i := 0; i < searcher.captures; i++ {
			captureStart := indices[2+i*2]
			captureEnd := indices[3+i*2]
			if captureStart < 0 {
				results[i] = runtime.StringValue("")
				continue
			}
			results[i] = runtime.StringValue(s[captureStart:captureEnd])
		}
		return results, nil
	}
	match, err := searcher.find(s, start-1)
	if err != nil {
		return nil, err
	}
	if match == nil {
		return []rt.Value{rt.NilValue}, nil
	}
	return stringMatchResults(runtime, s[match.start:match.end], match.captures), nil
}

func stringGMatch(runtime *rt.Runtime, s string, pattern string, start int) (rt.Value, error) {
	searcher, err := compileStringPattern(pattern, false)
	if err != nil {
		return rt.NilValue, err
	}
	position := luaStringStart(len(s), start) - 1
	done := false
	return runtime.NewHostFunctionMulti("string.gmatch.iter", func(runtime *rt.Runtime, _ []rt.Value) ([]rt.Value, error) {
		if done || position > len(s) {
			return nil, nil
		}
		match, err := searcher.find(s, position)
		if err != nil {
			return nil, err
		}
		if match == nil {
			done = true
			return nil, nil
		}
		full := s[match.start:match.end]
		if match.end == match.start {
			if match.end < len(s) {
				position = match.end + 1
			} else {
				position = len(s) + 1
				done = true
			}
		} else {
			position = match.end
		}
		return stringMatchResults(runtime, full, match.captures), nil
	}), nil
}

func stringGSub(runtime *rt.Runtime, machine *vm.VM, s string, pattern string, repl rt.Value, limit int) ([]rt.Value, error) {
	searcher, err := compileStringPattern(pattern, false)
	if err != nil {
		return nil, err
	}
	if limit == 0 {
		return []rt.Value{runtime.StringValue(s), rt.NumberValue(0)}, nil
	}
	staticReplacement, hasStaticReplacement, err := compileStringGSubStaticReplacement(runtime, repl)
	if err != nil {
		return nil, err
	}
	if hasStaticReplacement {
		if searcher.plain && searcher.literal != "" {
			return stringGSubPlainStatic(runtime, s, searcher.literal, staticReplacement, limit), nil
		}
		if searcher.re != nil {
			return stringGSubRegexStatic(runtime, s, searcher, staticReplacement, limit), nil
		}
	}
	count := 0
	position := 0
	var builder strings.Builder
	builder.Grow(len(s))
	for position <= len(s) && limit != 0 {
		match, err := searcher.find(s, position)
		if err != nil {
			return nil, err
		}
		if match == nil {
			break
		}
		full := s[match.start:match.end]
		builder.WriteString(s[position:match.start])
		if hasStaticReplacement {
			staticReplacement.appendTo(&builder, full, match.captures)
		} else {
			replacement, replace, err := stringGSubReplacement(runtime, machine, repl, full, match.captures)
			if err != nil {
				return nil, err
			}
			if replace {
				builder.WriteString(replacement)
			} else {
				builder.WriteString(full)
			}
		}
		count++
		if limit > 0 {
			limit--
		}
		if match.end == match.start {
			if match.end < len(s) {
				builder.WriteByte(s[match.end])
				position = match.end + 1
				continue
			}
			position = len(s) + 1
			break
		}
		position = match.end
	}
	if position <= len(s) {
		builder.WriteString(s[position:])
	}
	return []rt.Value{runtime.StringValue(builder.String()), rt.NumberValue(float64(count))}, nil
}

func stringGSubPlainStatic(runtime *rt.Runtime, s string, literal string, replacement *stringGSubStaticReplacement, limit int) []rt.Value {
	count := 0
	position := 0
	var builder strings.Builder
	builder.Grow(len(s))
	for position <= len(s) && limit != 0 {
		index := strings.Index(s[position:], literal)
		if index < 0 {
			break
		}
		matchStart := position + index
		matchEnd := matchStart + len(literal)
		builder.WriteString(s[position:matchStart])
		replacement.appendTo(&builder, s[matchStart:matchEnd], nil)
		count++
		if limit > 0 {
			limit--
		}
		position = matchEnd
	}
	builder.WriteString(s[position:])
	return []rt.Value{runtime.StringValue(builder.String()), rt.NumberValue(float64(count))}
}

func stringGSubRegexStatic(runtime *rt.Runtime, s string, searcher *stringPattern, replacement *stringGSubStaticReplacement, limit int) []rt.Value {
	count := 0
	position := 0
	var builder strings.Builder
	builder.Grow(len(s))
	for position <= len(s) && limit != 0 {
		indices := searcher.findRegexIndices(s, position)
		if indices == nil {
			break
		}
		matchStart := indices[0]
		matchEnd := indices[1]
		builder.WriteString(s[position:matchStart])
		replacement.appendRegexTo(&builder, s, indices)
		count++
		if limit > 0 {
			limit--
		}
		if matchEnd == matchStart {
			if matchEnd < len(s) {
				builder.WriteByte(s[matchEnd])
				position = matchEnd + 1
				continue
			}
			position = len(s) + 1
			break
		}
		position = matchEnd
	}
	if position <= len(s) {
		builder.WriteString(s[position:])
	}
	return []rt.Value{runtime.StringValue(builder.String()), rt.NumberValue(float64(count))}
}

func stringGSubReplacement(runtime *rt.Runtime, machine *vm.VM, repl rt.Value, full string, captures []stringPatternCapture) (string, bool, error) {
	if s, ok := runtime.ToString(repl); ok {
		text, err := expandStringGSubReplacement(s, full, captures)
		return text, true, err
	}
	if repl.IsNumber() {
		return rt.FormatNumber(repl.Number()), true, nil
	}
	h, ok := repl.Handle()
	if !ok {
		return "", false, fmt.Errorf("string.gsub replacement expects string, number, function or table")
	}
	switch h.Kind() {
	case rt.ObjectHostFunction, rt.ObjectLuaClosure:
		callArgs := make([]rt.Value, 0, max(1, len(captures)))
		if len(captures) == 0 {
			callArgs = append(callArgs, runtime.StringValue(full))
		} else {
			for _, capture := range captures {
				callArgs = append(callArgs, stringCaptureToValue(runtime, capture))
			}
		}
		results, err := machine.CallValue(repl, callArgs)
		if err != nil {
			return "", false, err
		}
		if len(results) == 0 || results[0].Kind() == rt.KindNil || (results[0].Kind() == rt.KindBool && !results[0].Bool()) {
			return "", false, nil
		}
		text, ok := concatString(runtime, results[0])
		if !ok {
			return "", false, fmt.Errorf("string.gsub replacement function must return string or number")
		}
		return text, true, nil
	case rt.ObjectTable:
		key := rt.Value(runtime.StringValue(full))
		if len(captures) > 0 {
			key = stringCaptureToValue(runtime, captures[0])
		}
		value, found, err := runtime.GetTable(repl, key)
		if err != nil {
			return "", false, err
		}
		if !found || value.Kind() == rt.KindNil || (value.Kind() == rt.KindBool && !value.Bool()) {
			return "", false, nil
		}
		text, ok := concatString(runtime, value)
		if !ok {
			return "", false, fmt.Errorf("string.gsub table replacement must contain strings or numbers")
		}
		return text, true, nil
	default:
		return "", false, fmt.Errorf("string.gsub replacement expects string, number, function or table")
	}
}

func compileStringGSubStaticReplacement(runtime *rt.Runtime, repl rt.Value) (*stringGSubStaticReplacement, bool, error) {
	if s, ok := runtime.ToString(repl); ok {
		if !strings.Contains(s, "%") {
			return &stringGSubStaticReplacement{literal: s}, true, nil
		}
		plan, err := compileStringGSubReplacementPlan(s)
		if err != nil {
			return nil, false, err
		}
		return &stringGSubStaticReplacement{plan: plan}, true, nil
	}
	if repl.IsNumber() {
		return &stringGSubStaticReplacement{literal: rt.FormatNumber(repl.Number())}, true, nil
	}
	return nil, false, nil
}

func (replacement *stringGSubStaticReplacement) appendTo(builder *strings.Builder, full string, captures []stringPatternCapture) {
	if replacement == nil {
		return
	}
	if replacement.plan == nil {
		builder.WriteString(replacement.literal)
		return
	}
	replacement.plan.appendTo(builder, full, captures)
}

func (replacement *stringGSubStaticReplacement) appendRegexTo(builder *strings.Builder, subject string, indices []int) {
	if replacement == nil {
		return
	}
	if replacement.plan == nil {
		builder.WriteString(replacement.literal)
		return
	}
	replacement.plan.appendRegexTo(builder, subject, indices)
}

func expandStringGSubReplacement(template string, full string, captures []stringPatternCapture) (string, error) {
	plan, err := compileStringGSubReplacementPlan(template)
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	plan.appendTo(&builder, full, captures)
	return builder.String(), nil
}

func compileStringGSubReplacementPlan(template string) (*stringGSubReplacementPlan, error) {
	if cached, ok := stringGSubReplacementCache.Load(template); ok {
		return cached.(*stringGSubReplacementPlan), nil
	}
	parts := make([]stringGSubReplacementPart, 0, 4)
	literalStart := 0
	for i := 0; i < len(template); i++ {
		if template[i] != '%' {
			continue
		}
		if literalStart < i {
			parts = append(parts, stringGSubReplacementPart{literal: template[literalStart:i], captureIndex: stringGSubReplacementLiteral})
		}
		if i+1 >= len(template) {
			return nil, fmt.Errorf("invalid use of '%%' in replacement string")
		}
		i++
		switch next := template[i]; {
		case next == '%':
			parts = append(parts, stringGSubReplacementPart{literal: "%", captureIndex: stringGSubReplacementLiteral})
		case next == '0':
			parts = append(parts, stringGSubReplacementPart{captureIndex: stringGSubReplacementFull})
		case next >= '1' && next <= '9':
			parts = append(parts, stringGSubReplacementPart{captureIndex: int(next - '1')})
		default:
			return nil, fmt.Errorf("invalid capture index %%%c in replacement string", next)
		}
		literalStart = i + 1
	}
	if literalStart < len(template) {
		parts = append(parts, stringGSubReplacementPart{literal: template[literalStart:], captureIndex: stringGSubReplacementLiteral})
	}
	plan := &stringGSubReplacementPlan{parts: parts}
	actual, _ := stringGSubReplacementCache.LoadOrStore(template, plan)
	return actual.(*stringGSubReplacementPlan), nil
}

func (plan *stringGSubReplacementPlan) appendTo(builder *strings.Builder, full string, captures []stringPatternCapture) {
	if plan == nil {
		return
	}
	for _, part := range plan.parts {
		switch part.captureIndex {
		case stringGSubReplacementLiteral:
			builder.WriteString(part.literal)
		case stringGSubReplacementFull:
			builder.WriteString(full)
		default:
			if part.captureIndex >= 0 && part.captureIndex < len(captures) {
				builder.WriteString(stringCaptureToString(captures[part.captureIndex]))
			}
		}
	}
}

func (plan *stringGSubReplacementPlan) appendRegexTo(builder *strings.Builder, subject string, indices []int) {
	if plan == nil || len(indices) < 2 {
		return
	}
	fullStart := indices[0]
	fullEnd := indices[1]
	for _, part := range plan.parts {
		switch part.captureIndex {
		case stringGSubReplacementLiteral:
			builder.WriteString(part.literal)
		case stringGSubReplacementFull:
			builder.WriteString(subject[fullStart:fullEnd])
		default:
			captureOffset := 2 + part.captureIndex*2
			if captureOffset+1 >= len(indices) {
				continue
			}
			captureStart := indices[captureOffset]
			captureEnd := indices[captureOffset+1]
			if captureStart >= 0 {
				builder.WriteString(subject[captureStart:captureEnd])
			}
		}
	}
}

func stringFormat(runtime *rt.Runtime, format string, args []rt.Value) (string, error) {
	var builder strings.Builder
	argIndex := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			builder.WriteByte(format[i])
			continue
		}
		if i+1 < len(format) && format[i+1] == '%' {
			builder.WriteByte('%')
			i++
			continue
		}
		spec, verb, next, err := parseStringFormatSpec(format, i)
		if err != nil {
			return "", err
		}
		if argIndex >= len(args) {
			return "", fmt.Errorf("string.format missing argument for %%%c", verb)
		}
		formatted, err := formatStringValue(runtime, spec, verb, args[argIndex])
		if err != nil {
			return "", err
		}
		builder.WriteString(formatted)
		argIndex++
		i = next - 1
	}
	return builder.String(), nil
}

const stringFormatFlags = "-+ #0"

func parseStringFormatSpec(format string, start int) (string, byte, int, error) {
	i := start + 1
	flagCount := 0
	for i < len(format) && strings.IndexByte(stringFormatFlags, format[i]) >= 0 {
		i++
		flagCount++
		if flagCount >= len(stringFormatFlags)+1 {
			return "", 0, 0, fmt.Errorf("invalid format (repeated flags)")
		}
	}
	widthDigits := 0
	for i < len(format) && isASCIIDigit(format[i]) {
		i++
		widthDigits++
		if widthDigits > 2 {
			return "", 0, 0, fmt.Errorf("invalid format (width or precision too long)")
		}
	}
	if i < len(format) && format[i] == '.' {
		i++
		precisionDigits := 0
		for i < len(format) && isASCIIDigit(format[i]) {
			i++
			precisionDigits++
			if precisionDigits > 2 {
				return "", 0, 0, fmt.Errorf("invalid format (width or precision too long)")
			}
		}
	}
	if i >= len(format) {
		return "", 0, 0, fmt.Errorf("unterminated format option")
	}
	verb := format[i]
	return format[start : i+1], verb, i + 1, nil
}

func formatStringValue(runtime *rt.Runtime, spec string, verb byte, value rt.Value) (string, error) {
	switch verb {
	case 'd', 'i':
		integer, err := formatIntegerValue(value)
		if err != nil {
			return "", err
		}
		if verb == 'i' {
			spec = spec[:len(spec)-1] + "d"
		}
		return fmt.Sprintf(spec, integer), nil
	case 'o', 'u', 'x', 'X':
		integer, err := formatIntegerValue(value)
		if err != nil {
			return "", err
		}
		unsigned := uint64(integer)
		if verb == 'u' {
			spec = spec[:len(spec)-1] + "d"
		}
		return fmt.Sprintf(spec, unsigned), nil
	case 'e', 'E', 'f', 'g', 'G':
		if !value.IsNumber() {
			return "", fmt.Errorf("string.format %%%c expects number", verb)
		}
		formatted := fmt.Sprintf(spec, value.Number())
		return normalizeLuaExponent(formatted), nil
	case 'c':
		integer, err := formatIntegerValue(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(spec, rune(integer)), nil
	case 'q':
		text, err := plainString(runtime, value)
		if err != nil {
			return "", err
		}
		return quoteLuaString(text), nil
	case 's':
		text, err := plainString(runtime, value)
		if err != nil {
			return "", err
		}
		if !strings.Contains(spec, ".") && len(text) >= 100 {
			return text, nil
		}
		return fmt.Sprintf(spec, text), nil
	default:
		return "", fmt.Errorf("invalid option '%%%c' to 'format'", verb)
	}
}

func formatIntegerValue(value rt.Value) (int64, error) {
	if !value.IsNumber() {
		return 0, fmt.Errorf("integer format expects number")
	}
	return int64(value.Number()), nil
}

func isASCIIDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func quoteLuaString(text string) string {
	var builder strings.Builder
	builder.Grow(len(text) + 2)
	builder.WriteByte('"')
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"', '\\', '\n':
			builder.WriteByte('\\')
			builder.WriteByte(text[i])
		case '\r':
			builder.WriteString("\\r")
		case 0:
			builder.WriteString("\\000")
		default:
			builder.WriteByte(text[i])
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

func normalizeLuaExponent(text string) string {
	index := strings.LastIndexAny(text, "eE")
	if index < 0 || index+2 >= len(text) {
		return text
	}
	sign := text[index+1]
	if sign != '+' && sign != '-' {
		return text
	}
	digits := text[index+2:]
	if digits == "" {
		return text
	}
	for i := 0; i < len(digits); i++ {
		if !isASCIIDigit(digits[i]) {
			return text
		}
	}
	if len(digits) >= 3 {
		return text
	}
	return text[:index+2] + strings.Repeat("0", 3-len(digits)) + digits
}
