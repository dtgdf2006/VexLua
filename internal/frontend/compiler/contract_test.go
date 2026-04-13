package compiler

import (
	"testing"

	"vexlua/internal/bytecode"
	"vexlua/internal/frontend/lexer"
	"vexlua/internal/frontend/parser"
)

type stubParser struct {
	called bool
	chunk  *parser.Chunk
}

func (parserStage *stubParser) ParseChunk(_ string, _ []byte) (*parser.Chunk, error) {
	parserStage.called = true
	return parserStage.chunk, nil
}

type stubBinder struct {
	called bool
	bound  *BoundChunk
}

func (binderStage *stubBinder) BindChunk(_ *parser.Chunk) (*BoundChunk, error) {
	binderStage.called = true
	return binderStage.bound, nil
}

type stubEmitter struct {
	called bool
	proto  *bytecode.Proto
}

func (emitterStage *stubEmitter) EmitChunk(_ *BoundChunk) (*bytecode.Proto, error) {
	emitterStage.called = true
	return emitterStage.proto, nil
}

func TestCompileEntryProducesValidatedProto(t *testing.T) {
	proto, err := Compile("@phase6.lua", []byte("return 1"))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if proto == nil {
		t.Fatalf("Compile returned nil proto")
	}
	if len(proto.Code) == 0 {
		t.Fatalf("compiled proto should contain bytecode")
	}
	if err := bytecode.ValidateProto(proto); err != nil {
		t.Fatalf("compiled proto validation: %v", err)
	}
}

func TestCompileEntryStillSurfacesParserDiagnostics(t *testing.T) {
	_, err := Compile("@phase2-bad.lua", []byte("local value ="))
	if err == nil {
		t.Fatalf("Compile should surface parse diagnostics for invalid source")
	}
	diagErr, ok := err.(*lexer.DiagnosticError)
	if !ok {
		t.Fatalf("compile error type = %T, want *lexer.DiagnosticError", err)
	}
	primary, ok := diagErr.Primary()
	if !ok || primary.Phase != lexer.PhaseParse {
		t.Fatalf("primary diagnostic = %+v, %v, want parse-phase diagnostic", primary, ok)
	}
}

func TestDriverFollowsParseBindEmitPipeline(t *testing.T) {
	span := lexer.Span{Start: lexer.StartPosition(), End: lexer.Position{Offset: 1, Line: 1, Column: 2}}
	chunk := &parser.Chunk{NodeInfo: parser.NodeInfo{Span: span}, Name: "@x.lua", Block: &parser.Block{NodeInfo: parser.NodeInfo{Span: span}}}
	bound := &BoundChunk{BoundInfo: BoundInfo{Span: span}, Name: "@x.lua", Func: &BoundFunc{BoundInfo: BoundInfo{Span: span}, Body: &BoundBlock{BoundInfo: BoundInfo{Span: span}}}}
	proto := &bytecode.Proto{Source: "@x.lua", MaxStackSize: 1, Code: []bytecode.Instruction{bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0)}}
	parserStage := &stubParser{chunk: chunk}
	binderStage := &stubBinder{bound: bound}
	emitterStage := &stubEmitter{proto: proto}

	driver := NewDriver(parserStage, binderStage, emitterStage)
	got, err := driver.Compile(chunk.Name, []byte("return"))
	if err != nil {
		t.Fatalf("driver compile: %v", err)
	}
	if got != proto {
		t.Fatalf("compiled proto = %p, want %p", got, proto)
	}
	if !parserStage.called || !binderStage.called || !emitterStage.called {
		t.Fatalf("pipeline calls = parser:%v binder:%v emitter:%v, want all true", parserStage.called, binderStage.called, emitterStage.called)
	}
}

func TestProtoBuilderMapsOneToOneToBytecodeProto(t *testing.T) {
	builder := NewProtoBuilder("@builder.lua")
	builder.SetLines(10, 20)
	builder.SetSignature(2, true, 1)
	builder.SetMaxStackSize(4)
	if index := builder.AddConstant(bytecode.StringConstant("x")); index != 0 {
		t.Fatalf("first constant index = %d, want 0", index)
	}
	if index := builder.AddConstant(bytecode.StringConstant("x")); index != 0 {
		t.Fatalf("deduped constant index = %d, want 0", index)
	}
	child := &bytecode.Proto{Source: "@child.lua", MaxStackSize: 1, Code: []bytecode.Instruction{bytecode.CreateABC(bytecode.OP_RETURN, 0, 1, 0)}}
	builder.AddChildProto(child)
	builder.AddLocVar("x", 0, 1)
	builder.AddUpvalueName("uv")
	builder.EmitABx(bytecode.OP_LOADK, 0, 0, 10)
	builder.EmitABC(bytecode.OP_RETURN, 0, 2, 0, 10)

	proto, err := builder.Finish()
	if err != nil {
		t.Fatalf("builder finish: %v", err)
	}
	if proto.Source != "@builder.lua" || proto.LineDefined != 10 || proto.LastLineDef != 20 {
		t.Fatalf("proto header = %+v", proto)
	}
	if proto.NumParams != 2 || proto.IsVararg != 2 || proto.NumUpvalues != 1 || proto.MaxStackSize != 4 {
		t.Fatalf("proto signature = %+v", proto)
	}
	if len(proto.Constants) != 1 || proto.Constants[0].Text != "x" {
		t.Fatalf("proto constants = %+v", proto.Constants)
	}
	if len(proto.Protos) != 1 || proto.Protos[0] != child {
		t.Fatalf("proto children = %+v", proto.Protos)
	}
	if len(proto.LineInfo) != 2 || proto.LineInfo[0] != 10 || proto.LineInfo[1] != 10 {
		t.Fatalf("proto line info = %+v", proto.LineInfo)
	}
	if len(proto.LocVars) != 1 || proto.LocVars[0].Name != "x" {
		t.Fatalf("proto locvars = %+v", proto.LocVars)
	}
	if len(proto.UpvalueNames) != 1 || proto.UpvalueNames[0] != "uv" {
		t.Fatalf("proto upvalue names = %+v", proto.UpvalueNames)
	}
	if len(proto.Code) != 2 {
		t.Fatalf("proto code length = %d, want 2", len(proto.Code))
	}
}

func TestBoundIRSurfaceKeepsPhase0Categories(t *testing.T) {
	span := lexer.Span{Start: lexer.StartPosition(), End: lexer.Position{Offset: 1, Line: 1, Column: 2}}
	function := &BoundFunc{
		BoundInfo: BoundInfo{Span: span},
		Params:    []SymbolID{1},
		Locals:    []SymbolID{2},
		Captures:  []CaptureDesc{{Name: "uv", Symbol: 3, Source: CaptureFromLocal, Index: 0}},
		Body:      &BoundBlock{BoundInfo: BoundInfo{Span: span}, Scope: 1},
	}
	chunk := &BoundChunk{BoundInfo: BoundInfo{Span: span}, Name: "@x.lua", Func: function}
	if chunk.SpanRange() != span {
		t.Fatalf("bound chunk span = %+v, want %+v", chunk.SpanRange(), span)
	}
	expr := BoundCallExpr{BoundExprInfo: BoundExprInfo{BoundInfo: BoundInfo{Span: span}, Results: ResultTail}}
	if expr.ResultMode() != ResultTail {
		t.Fatalf("call expr result mode = %d, want tail", expr.ResultMode())
	}
	if SymbolUpvalue.String() != "upvalue" {
		t.Fatalf("symbol kind string = %q, want upvalue", SymbolUpvalue.String())
	}
	result := ExprResult{Kind: ExprResultCallResult, Info: 7, TrueJumps: JumpList{Entries: []int{1}}}
	if result.Kind != ExprResultCallResult || result.Info != 7 || len(result.TrueJumps.Entries) != 1 {
		t.Fatalf("expr result = %+v", result)
	}
	if function.Captures[0].Source != CaptureFromLocal {
		t.Fatalf("capture source = %d, want local", function.Captures[0].Source)
	}
}
