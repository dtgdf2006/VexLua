package lexer

// Position identifies a byte offset and its 1-based line and column.
type Position struct {
	Offset int
	Line   int
	Column int
}

// StartPosition returns the canonical start of a source buffer.
func StartPosition() Position {
	return Position{Offset: 0, Line: 1, Column: 1}
}

// IsValid reports whether the position carries concrete source coordinates.
func (position Position) IsValid() bool {
	return position.Offset >= 0 && position.Line > 0 && position.Column > 0
}

// Span is a half-open source interval: [Start, End).
type Span struct {
	Start Position
	End   Position
}

// IsValid reports whether the span has valid endpoints in forward order.
func (span Span) IsValid() bool {
	return span.Start.IsValid() && span.End.IsValid() && span.End.Offset >= span.Start.Offset
}

// Len returns the byte width of the span.
func (span Span) Len() int {
	if !span.IsValid() {
		return 0
	}
	return span.End.Offset - span.Start.Offset
}

// MergeSpans returns the smallest span that covers both inputs.
func MergeSpans(left Span, right Span) Span {
	if !left.IsValid() {
		return right
	}
	if !right.IsValid() {
		return left
	}
	merged := Span{Start: left.Start, End: left.End}
	if right.Start.Offset < merged.Start.Offset {
		merged.Start = right.Start
	}
	if right.End.Offset > merged.End.Offset {
		merged.End = right.End
	}
	return merged
}
