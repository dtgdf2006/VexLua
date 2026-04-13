package lexer

import (
	"fmt"
	"strings"
)

// Phase identifies which frontend stage produced a diagnostic.
type Phase string

const (
	PhaseLex     Phase = "lex"
	PhaseParse   Phase = "parse"
	PhaseBind    Phase = "bind"
	PhaseEmit    Phase = "emit"
	PhaseCompile Phase = "compile"
)

// Severity identifies how strongly a diagnostic should be surfaced.
type Severity uint8

const (
	SeverityError Severity = iota
	SeverityWarning
	SeverityNote
)

func (severity Severity) String() string {
	switch severity {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityNote:
		return "note"
	default:
		return fmt.Sprintf("Severity(%d)", severity)
	}
}

// Diagnostic is the frontend-wide error and warning payload shared across
// lexer, parser, binding, and emission.
type Diagnostic struct {
	Phase    Phase
	Severity Severity
	Message  string
	Span     Span
}

func (diagnostic Diagnostic) Error() string {
	var builder strings.Builder
	if diagnostic.Phase != "" {
		builder.WriteString(string(diagnostic.Phase))
		builder.WriteString(": ")
	}
	if diagnostic.Span.IsValid() {
		builder.WriteString(fmt.Sprintf("%d:%d: ", diagnostic.Span.Start.Line, diagnostic.Span.Start.Column))
	}
	builder.WriteString(diagnostic.Message)
	return builder.String()
}

// DiagnosticError is the aggregate error surface returned by the source
// frontend pipeline.
type DiagnosticError struct {
	Diagnostics []Diagnostic
}

func (err *DiagnosticError) Error() string {
	if err == nil || len(err.Diagnostics) == 0 {
		return ""
	}
	if len(err.Diagnostics) == 1 {
		return err.Diagnostics[0].Error()
	}
	parts := make([]string, 0, len(err.Diagnostics))
	for _, diagnostic := range err.Diagnostics {
		parts = append(parts, diagnostic.Error())
	}
	return strings.Join(parts, "; ")
}

// Primary returns the first diagnostic, if any.
func (err *DiagnosticError) Primary() (Diagnostic, bool) {
	if err == nil || len(err.Diagnostics) == 0 {
		return Diagnostic{}, false
	}
	return err.Diagnostics[0], true
}

// NewDiagnosticError constructs a frontend error from one or more diagnostics.
func NewDiagnosticError(diagnostics ...Diagnostic) error {
	if len(diagnostics) == 0 {
		return nil
	}
	cloned := make([]Diagnostic, len(diagnostics))
	copy(cloned, diagnostics)
	return &DiagnosticError{Diagnostics: cloned}
}

// Errorf reports a single error diagnostic.
func Errorf(phase Phase, span Span, format string, args ...any) error {
	return NewDiagnosticError(Diagnostic{
		Phase:    phase,
		Severity: SeverityError,
		Message:  fmt.Sprintf(format, args...),
		Span:     span,
	})
}
