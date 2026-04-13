package compiler

import (
	"vexlua/internal/bytecode"
	"vexlua/internal/frontend/lexer"
	"vexlua/internal/frontend/parser"
)

// Parser is the parser stage contract used by the compile driver.
type Parser interface {
	ParseChunk(name string, src []byte) (*parser.Chunk, error)
}

// Binder is the binding stage contract used by the compile driver.
type Binder interface {
	BindChunk(chunk *parser.Chunk) (*BoundChunk, error)
}

// Emitter is the emission stage contract used by the compile driver.
type Emitter interface {
	EmitChunk(chunk *BoundChunk) (*bytecode.Proto, error)
}

// Driver is the Sparkplug-style explicit compile driver for the source frontend.
type Driver struct {
	Parser  Parser
	Binder  Binder
	Emitter Emitter
}

// NewDriver constructs an explicit frontend compile driver.
func NewDriver(parserStage Parser, binderStage Binder, emitterStage Emitter) *Driver {
	return &Driver{Parser: parserStage, Binder: binderStage, Emitter: emitterStage}
}

// Compile is the stable source-to-proto entry point for the frontend pipeline.
func Compile(name string, src []byte) (*bytecode.Proto, error) {
	return DefaultDriver().Compile(name, src)
}

// DefaultDriver wires the real parser, binder, and emitter stages.
func DefaultDriver() *Driver {
	return &Driver{Parser: parser.NewParser(), Binder: NewBinder(), Emitter: NewEmitter()}
}

// Compile drives Parse -> Bind -> Emit and validates the final proto.
func (driver *Driver) Compile(name string, src []byte) (*bytecode.Proto, error) {
	if driver.Parser == nil {
		return nil, lexer.Errorf(lexer.PhaseParse, lexer.Span{}, "source frontend parser stage is not implemented")
	}
	chunk, err := driver.Parser.ParseChunk(name, src)
	if err != nil {
		return nil, err
	}
	if driver.Binder == nil {
		return nil, lexer.Errorf(lexer.PhaseBind, lexer.Span{}, "source frontend binder stage is not implemented")
	}
	bound, err := driver.Binder.BindChunk(chunk)
	if err != nil {
		return nil, err
	}
	if driver.Emitter == nil {
		return nil, lexer.Errorf(lexer.PhaseEmit, lexer.Span{}, "source frontend emitter stage is not implemented")
	}
	proto, err := driver.Emitter.EmitChunk(bound)
	if err != nil {
		return nil, err
	}
	if err := bytecode.ValidateProto(proto); err != nil {
		return nil, err
	}
	return proto, nil
}
