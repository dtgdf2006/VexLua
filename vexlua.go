package vexlua

import (
	"vexlua/internal/bytecode"
	"vexlua/internal/chunk51"
	bccompiler "vexlua/internal/compiler"
	"vexlua/internal/jit"
	amd64jit "vexlua/internal/jit/amd64"
	rt "vexlua/internal/runtime"
	"vexlua/internal/stdlib"
	"vexlua/internal/vm"
)

type Value = rt.Value

type Options struct {
	EnableJIT    bool
	HotThreshold uint32
}

type Engine struct {
	runtime  *rt.Runtime
	machine  *vm.VM
	compiler *bccompiler.Compiler
	sources  map[string]*bytecode.Proto
	order    []string
}

type ProgramStats struct {
	Runs         uint32
	JITCompiled  bool
	QuickenedOps int
}

const sourceCacheLimit = 64

func New() *Engine {
	return NewWithOptions(Options{
		EnableJIT:    true,
		HotThreshold: 8,
	})
}

func NewWithOptions(opts Options) *Engine {
	runtime := rt.NewRuntime()
	sourceCompiler := bccompiler.New(runtime)
	var nativeCompiler jit.Compiler
	if opts.EnableJIT {
		nativeCompiler = amd64jit.NewCompiler()
	}
	engine := &Engine{
		runtime:  runtime,
		machine:  vm.New(runtime, nativeCompiler, opts.HotThreshold),
		compiler: sourceCompiler,
		sources:  make(map[string]*bytecode.Proto, sourceCacheLimit),
		order:    make([]string, 0, sourceCacheLimit),
	}
	if err := stdlib.Register(runtime, engine.machine, engine.compiler); err != nil {
		panic(err)
	}
	return engine
}

func (e *Engine) RegisterFunc(name string, fn any) error {
	v, err := rt.WrapFunction(e.runtime, name, fn)
	if err != nil {
		return err
	}
	e.runtime.SetGlobal(name, v)
	return nil
}

func (e *Engine) RegisterObject(name string, obj any) error {
	v, err := rt.WrapObject(e.runtime, name, obj)
	if err != nil {
		return err
	}
	e.runtime.SetGlobal(name, v)
	return nil
}

func (e *Engine) RegisterTable(name string, fields map[string]any) error {
	handle := e.runtime.Heap().NewTable(len(fields))
	table := e.runtime.Heap().Table(handle)
	for key, raw := range fields {
		value, err := rt.BoxValue(e.runtime, raw)
		if err != nil {
			return err
		}
		table.SetSymbol(e.runtime.InternSymbol(key), value)
	}
	e.runtime.SetGlobal(name, rt.HandleValue(handle))
	return nil
}

func (e *Engine) Run(proto *bytecode.Proto) (result Value, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if code, ok := stdlib.RecoverExitCode(recovered); ok {
				result = rt.NilValue
				err = &ExitError{Code: code}
				return
			}
			panic(recovered)
		}
	}()
	return e.machine.Run(proto)
}

func (e *Engine) CompileString(source string) (*bytecode.Proto, error) {
	return e.compiler.CompileSource(source)
}

func (e *Engine) CompileStringNamed(source string, name string) (*bytecode.Proto, error) {
	proto, err := e.compiler.CompileSource(source)
	if err != nil {
		return nil, err
	}
	if name != "" {
		proto.Name = name
		proto.SetSourceRecursive(name)
	}
	return proto, nil
}

func (e *Engine) DoString(source string) (Value, error) {
	proto, err := e.cachedCompileString(source)
	if err != nil {
		return rt.NilValue, err
	}
	return e.Run(proto)
}

func (e *Engine) DoStringNamed(source string, name string) (Value, error) {
	proto, err := e.CompileStringNamed(source, name)
	if err != nil {
		return rt.NilValue, err
	}
	return e.Run(proto)
}

func (e *Engine) cachedCompileString(source string) (*bytecode.Proto, error) {
	if proto, ok := e.sources[source]; ok {
		return proto, nil
	}
	proto, err := e.CompileString(source)
	if err != nil {
		return nil, err
	}
	if len(e.order) == sourceCacheLimit {
		oldest := e.order[0]
		e.order = e.order[1:]
		delete(e.sources, oldest)
	}
	e.sources[source] = proto
	e.order = append(e.order, source)
	return proto, nil
}

func (e *Engine) DumpProto(proto *bytecode.Proto) ([]byte, error) {
	return chunk51.Dump(e.runtime, proto)
}

func (e *Engine) LoadProto(data []byte) (*bytecode.Proto, error) {
	return chunk51.Load(e.runtime, data)
}

func (e *Engine) Stats(proto *bytecode.Proto) ProgramStats {
	stats := e.machine.Stats(proto)
	return ProgramStats{
		Runs:         stats.Runs,
		JITCompiled:  stats.JITCompiled,
		QuickenedOps: stats.QuickenedOps,
	}
}

func (e *Engine) FormatValue(value Value) string {
	if s, ok := e.runtime.ToString(value); ok {
		return s
	}
	return value.String()
}

func (e *Engine) BuildFunctionDemo(funcName string, arg float64) *bytecode.Proto {
	fnSym := e.runtime.InternSymbol(funcName)
	p := bytecode.NewProto("function_demo", 3, 0)
	argConst := p.AddConstant(rt.NumberValue(arg))
	p.Emit(bytecode.OpLoadGlobal, 0, 0, 0, int32(fnSym))
	p.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(argConst))
	p.Emit(bytecode.OpCall, 2, 0, 1, 1)
	p.Emit(bytecode.OpReturn, 2, 0, 0, 0)
	return p
}

func (e *Engine) BuildFieldAddDemo(objectName, fieldName string, bonus float64) *bytecode.Proto {
	objSym := e.runtime.InternSymbol(objectName)
	fieldSym := e.runtime.InternSymbol(fieldName)
	p := bytecode.NewProto("field_add_demo", 4, 1)
	bonusConst := p.AddConstant(rt.NumberValue(bonus))
	p.Emit(bytecode.OpLoadGlobal, 0, 0, 0, int32(objSym))
	p.Emit(bytecode.OpGetField, 1, 0, 0, int32(fieldSym))
	p.Emit(bytecode.OpAddConst, 2, 1, 0, int32(bonusConst))
	p.Emit(bytecode.OpReturn, 2, 0, 0, 0)
	return p
}

func (e *Engine) BuildMethodDemo(objectName, methodName string, arg float64) *bytecode.Proto {
	objSym := e.runtime.InternSymbol(objectName)
	methodSym := e.runtime.InternSymbol(methodName)
	p := bytecode.NewProto("method_demo", 4, 1)
	argConst := p.AddConstant(rt.NumberValue(arg))
	p.Emit(bytecode.OpLoadGlobal, 0, 0, 0, int32(objSym))
	p.Emit(bytecode.OpGetField, 1, 0, 0, int32(methodSym))
	p.Emit(bytecode.OpLoadConst, 2, 0, 0, int32(argConst))
	p.Emit(bytecode.OpCall, 3, 1, 2, 1)
	p.Emit(bytecode.OpReturn, 3, 0, 0, 0)
	return p
}

func (e *Engine) BuildSumLoop(limit float64) *bytecode.Proto {
	p := bytecode.NewProto("sum_loop", 4, 0)
	zeroConst := p.AddConstant(rt.NumberValue(0))
	oneConst := p.AddConstant(rt.NumberValue(1))
	limitConst := p.AddConstant(rt.NumberValue(limit))
	p.Emit(bytecode.OpLoadConst, 0, 0, 0, int32(zeroConst))
	p.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(oneConst))
	p.Emit(bytecode.OpLoadConst, 2, 0, 0, int32(limitConst))
	p.Emit(bytecode.OpLoadConst, 3, 0, 0, int32(oneConst))
	p.Emit(bytecode.OpAdd, 0, 0, 1, 0)
	p.Emit(bytecode.OpAdd, 1, 1, 3, 0)
	p.Emit(bytecode.OpLessEqualJump, 1, 2, 0, 4)
	p.Emit(bytecode.OpReturn, 0, 0, 0, 0)
	return p
}
