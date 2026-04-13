package compiler

// Scope records one lexical scope after binding.
type Scope struct {
	ID          ScopeID
	Parent      ScopeID
	Symbols     []SymbolID
	HasUpvalues bool
}
