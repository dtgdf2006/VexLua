package compiler

import "vexlua/internal/frontend/lexer"

// SymbolID is the stable identifier used by the bound IR.
type SymbolID uint32

const InvalidSymbolID SymbolID = 0

// ScopeID identifies a lexical scope after binding.
type ScopeID uint32

const InvalidScopeID ScopeID = 0

// SymbolKind classifies parser names after Lua 5.1 name resolution.
type SymbolKind uint8

const (
	SymbolInvalid SymbolKind = iota
	SymbolParam
	SymbolLocal
	SymbolUpvalue
	SymbolGlobal
)

func (kind SymbolKind) String() string {
	switch kind {
	case SymbolParam:
		return "param"
	case SymbolLocal:
		return "local"
	case SymbolUpvalue:
		return "upvalue"
	case SymbolGlobal:
		return "global"
	default:
		return "invalid"
	}
}

// Symbol is the binder-owned metadata for a resolved name.
type Symbol struct {
	ID       SymbolID
	Kind     SymbolKind
	Name     string
	DeclSpan lexer.Span
	Scope    ScopeID
}

// NameRef is the resolved name reference used by bound expressions and targets.
type NameRef struct {
	Kind       SymbolKind
	Symbol     SymbolID
	GlobalName string
}

// CaptureSource mirrors Lua 5.1's upvaldesc source classification.
type CaptureSource uint8

const (
	CaptureFromLocal CaptureSource = iota
	CaptureFromUpvalue
)

// CaptureDesc freezes the bound upvalue payload that later lowers into CLOSURE capture opcodes.
type CaptureDesc struct {
	Name   string
	Symbol SymbolID
	Source CaptureSource
	Index  int
}
