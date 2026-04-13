package compiler

import "vexlua/internal/bytecode"

// ProtoBuilder is the stable contract between the future emitter and bytecode.Proto.
// Each mutator maps directly onto one or more bytecode.Proto fields.
type ProtoBuilder struct {
	source       string
	lineDefined  int
	lastLineDef  int
	numUpvalues  uint8
	numParams    uint8
	isVararg     uint8
	maxStackSize uint8
	code         []bytecode.Instruction
	constants    []bytecode.Constant
	protos       []*bytecode.Proto
	lineInfo     []int
	locvars      []bytecode.LocVar
	upvalueNames []string
	constIndex   map[bytecode.Constant]int
}

// NewProtoBuilder creates a builder for one function-like output proto.
func NewProtoBuilder(source string) *ProtoBuilder {
	return &ProtoBuilder{source: source, constIndex: make(map[bytecode.Constant]int)}
}

// SetSource maps to bytecode.Proto.Source.
func (builder *ProtoBuilder) SetSource(source string) {
	builder.source = source
}

// SetLines maps to bytecode.Proto.LineDefined and bytecode.Proto.LastLineDef.
func (builder *ProtoBuilder) SetLines(lineDefined int, lastLineDef int) {
	builder.lineDefined = lineDefined
	builder.lastLineDef = lastLineDef
}

// SetSignature maps to bytecode.Proto.NumParams, IsVararg, and NumUpvalues.
func (builder *ProtoBuilder) SetSignature(numParams uint8, hasVararg bool, numUpvalues uint8) {
	builder.numParams = numParams
	if hasVararg {
		builder.isVararg = 2
	} else {
		builder.isVararg = 0
	}
	builder.numUpvalues = numUpvalues
}

// SetMaxStackSize maps to bytecode.Proto.MaxStackSize.
func (builder *ProtoBuilder) SetMaxStackSize(maxStackSize uint8) {
	builder.maxStackSize = maxStackSize
}

// AddConstant appends or reuses a constant in bytecode.Proto.Constants.
func (builder *ProtoBuilder) AddConstant(constant bytecode.Constant) int {
	if index, ok := builder.constIndex[constant]; ok {
		return index
	}
	index := len(builder.constants)
	builder.constants = append(builder.constants, constant)
	builder.constIndex[constant] = index
	return index
}

// AddChildProto appends a child into bytecode.Proto.Protos.
func (builder *ProtoBuilder) AddChildProto(child *bytecode.Proto) int {
	index := len(builder.protos)
	builder.protos = append(builder.protos, child)
	return index
}

// AddLocVar appends a debug local into bytecode.Proto.LocVars.
func (builder *ProtoBuilder) AddLocVar(name string, startPC int, endPC int) int {
	index := len(builder.locvars)
	builder.locvars = append(builder.locvars, bytecode.LocVar{Name: name, StartPC: startPC, EndPC: endPC})
	return index
}

// AddUpvalueName appends a debug upvalue name into bytecode.Proto.UpvalueNames.
func (builder *ProtoBuilder) AddUpvalueName(name string) int {
	index := len(builder.upvalueNames)
	builder.upvalueNames = append(builder.upvalueNames, name)
	return index
}

// EmitInstruction appends one instruction and its line mapping.
func (builder *ProtoBuilder) EmitInstruction(instruction bytecode.Instruction, line int) int {
	pc := len(builder.code)
	builder.code = append(builder.code, instruction)
	builder.lineInfo = append(builder.lineInfo, line)
	return pc
}

// EmitABC appends an ABC instruction into bytecode.Proto.Code.
func (builder *ProtoBuilder) EmitABC(opcode bytecode.Opcode, a int, b int, c int, line int) int {
	return builder.EmitInstruction(bytecode.CreateABC(opcode, a, b, c), line)
}

// EmitABx appends an ABx instruction into bytecode.Proto.Code.
func (builder *ProtoBuilder) EmitABx(opcode bytecode.Opcode, a int, bx int, line int) int {
	return builder.EmitInstruction(bytecode.CreateABx(opcode, a, bx), line)
}

// EmitAsBx appends an AsBx instruction into bytecode.Proto.Code.
func (builder *ProtoBuilder) EmitAsBx(opcode bytecode.Opcode, a int, sbx int, line int) int {
	return builder.EmitInstruction(bytecode.CreateAsBx(opcode, a, sbx), line)
}

// Snapshot returns the current bytecode.Proto image without validation.
func (builder *ProtoBuilder) Snapshot() *bytecode.Proto {
	proto := &bytecode.Proto{
		Source:       builder.source,
		LineDefined:  builder.lineDefined,
		LastLineDef:  builder.lastLineDef,
		NumUpvalues:  builder.numUpvalues,
		NumParams:    builder.numParams,
		IsVararg:     builder.isVararg,
		MaxStackSize: builder.maxStackSize,
		Code:         append([]bytecode.Instruction(nil), builder.code...),
		Constants:    append([]bytecode.Constant(nil), builder.constants...),
		Protos:       append([]*bytecode.Proto(nil), builder.protos...),
		LineInfo:     append([]int(nil), builder.lineInfo...),
		LocVars:      append([]bytecode.LocVar(nil), builder.locvars...),
		UpvalueNames: append([]string(nil), builder.upvalueNames...),
	}
	return proto
}

// Finish validates the output and returns the final bytecode.Proto.
func (builder *ProtoBuilder) Finish() (*bytecode.Proto, error) {
	proto := builder.Snapshot()
	if err := bytecode.ValidateProto(proto); err != nil {
		return nil, err
	}
	return proto, nil
}
