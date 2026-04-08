package frontend

import "testing"

func TestParseScientificNotationAssignment(t *testing.T) {
	chunk, err := Parse("delta = 1e-6\nreturn delta")
	if err != nil {
		t.Fatalf("parse scientific notation: %v", err)
	}
	if len(chunk.Statements) != 2 {
		t.Fatalf("statement count = %d, want 2", len(chunk.Statements))
	}
	assign, ok := chunk.Statements[0].(*AssignStmt)
	if !ok {
		t.Fatalf("first statement type = %T, want *AssignStmt", chunk.Statements[0])
	}
	if len(assign.Values) != 1 {
		t.Fatalf("assignment value count = %d, want 1", len(assign.Values))
	}
	number, ok := assign.Values[0].(*NumberExpr)
	if !ok {
		t.Fatalf("assignment value type = %T, want *NumberExpr", assign.Values[0])
	}
	if number.Value != 1e-6 {
		t.Fatalf("assignment value = %v, want %v", number.Value, 1e-6)
	}
}

func TestParseReferenceBisectScript(t *testing.T) {
	source := `delta=1e-6

function bisect(f,a,b,fa,fb)
 local c=(a+b)/2
 io.write(n," c=",c," a=",a," b=",b,"\n")
 if c==a or c==b or math.abs(a-b)<delta then return c,b-a end
 n=n+1
 local fc=f(c)
 if fa*fc<0 then return bisect(f,a,c,fa,fc) else return bisect(f,c,b,fc,fb) end
end

function solve(f,a,b)
 n=0
 local z,e=bisect(f,a,b,f(a),f(b))
 io.write(string.format("after %d steps, root is %.17g with error %.1e, f=%.1e\n",n,z,e,f(z)))
end

function f(x)
 return x*x*x-x-1
end

solve(f,1,2)
`
	if _, err := Parse(source); err != nil {
		t.Fatalf("parse bisect.lua: %v", err)
	}
}

func TestParseCallWithStringArgumentShorthand(t *testing.T) {
	chunk, err := Parse(`return os.getenv"USER"`)
	if err != nil {
		t.Fatalf("parse string call shorthand: %v", err)
	}
	if len(chunk.Statements) != 1 {
		t.Fatalf("statement count = %d, want 1", len(chunk.Statements))
	}
	ret, ok := chunk.Statements[0].(*ReturnStmt)
	if !ok {
		t.Fatalf("statement type = %T, want *ReturnStmt", chunk.Statements[0])
	}
	if len(ret.Values) != 1 {
		t.Fatalf("return value count = %d, want 1", len(ret.Values))
	}
	call, ok := ret.Values[0].(*CallExpr)
	if !ok {
		t.Fatalf("return value type = %T, want *CallExpr", ret.Values[0])
	}
	if len(call.Args) != 1 {
		t.Fatalf("arg count = %d, want 1", len(call.Args))
	}
	arg, ok := call.Args[0].(*StringExpr)
	if !ok {
		t.Fatalf("arg type = %T, want *StringExpr", call.Args[0])
	}
	if arg.Value != "USER" {
		t.Fatalf("arg value = %q, want %q", arg.Value, "USER")
	}
}
