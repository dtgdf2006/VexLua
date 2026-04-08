package stdlib

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	rt "vexlua/internal/runtime"
	"vexlua/internal/vm"
)

func bind(runtime *rt.Runtime, name string, fn any) error {
	value, err := rt.WrapFunction(runtime, name, fn)
	if err != nil {
		return err
	}
	runtime.SetGlobal(name, value)
	return nil
}

func setTableFunc(runtime *rt.Runtime, table *rt.Table, name string, fn any) error {
	value, err := rt.WrapFunction(runtime, name, fn)
	if err != nil {
		return err
	}
	table.SetSymbol(runtime.InternSymbol(name), value)
	return nil
}

func isTruthy(value rt.Value) bool {
	if value.Kind() == rt.KindNil {
		return false
	}
	if value.Kind() == rt.KindBool {
		return value.Bool()
	}
	return true
}

func typeName(runtime *rt.Runtime, value rt.Value) string {
	switch value.Kind() {
	case rt.KindNil:
		return "nil"
	case rt.KindBool:
		return "boolean"
	case rt.KindNumber:
		return "number"
	case rt.KindHandle:
		h, _ := value.Handle()
		switch h.Kind() {
		case rt.ObjectString:
			return "string"
		case rt.ObjectTable:
			return "table"
		case rt.ObjectHostFunction, rt.ObjectLuaClosure:
			return "function"
		case rt.ObjectThread:
			return "thread"
		case rt.ObjectUserdata, rt.ObjectHostProxy:
			return "userdata"
		default:
			return "userdata"
		}
	default:
		return "unknown"
	}
}

func typeString(runtime *rt.Runtime, value rt.Value) string {
	if s, ok := runtime.ToString(value); ok {
		return s
	}
	return value.String()
}

func asCoroutine(runtime *rt.Runtime, value rt.Value) (*vm.Coroutine, error) {
	h, ok := value.Handle()
	if !ok {
		return nil, fmt.Errorf("expected coroutine")
	}
	switch h.Kind() {
	case rt.ObjectThread:
		thread := runtime.Heap().Thread(h)
		co, ok := thread.State.(*vm.Coroutine)
		if !ok {
			return nil, fmt.Errorf("expected coroutine, got %T", thread.State)
		}
		return co, nil
	case rt.ObjectHostProxy:
		proxy := runtime.Heap().HostProxy(h)
		co, ok := proxy.Subject.(*vm.Coroutine)
		if !ok {
			return nil, fmt.Errorf("expected coroutine, got %T", proxy.Subject)
		}
		return co, nil
	default:
		return nil, fmt.Errorf("expected coroutine")
	}
}

func coroutineValue(runtime *rt.Runtime, co *vm.Coroutine) rt.Value {
	if co == nil {
		return rt.NilValue
	}
	if co.Proxy().Kind() != rt.KindNil {
		return co.Proxy()
	}
	value := runtime.NewThreadValue(co)
	co.SetProxy(value)
	return value
}

func asTable(runtime *rt.Runtime, value rt.Value) (*rt.Table, error) {
	h, ok := value.Handle()
	if !ok || h.Kind() != rt.ObjectTable {
		return nil, fmt.Errorf("expected table")
	}
	return runtime.Heap().Table(h), nil
}

func rawTableGet(runtime *rt.Runtime, target rt.Value, key rt.Value) (rt.Value, bool, error) {
	table, err := asTable(runtime, target)
	if err != nil {
		return rt.NilValue, false, err
	}
	if name, ok := runtime.ToString(key); ok {
		value, _, found := table.GetSymbol(runtime.InternSymbol(name))
		return value, found, nil
	}
	value, found := table.RawGet(key)
	return value, found, nil
}

func rawTableSet(runtime *rt.Runtime, target rt.Value, key rt.Value, value rt.Value) error {
	table, err := asTable(runtime, target)
	if err != nil {
		return err
	}
	if name, ok := runtime.ToString(key); ok {
		table.SetSymbol(runtime.InternSymbol(name), value)
		return nil
	}
	table.RawSet(key, value)
	return nil
}

type luaError struct {
	value rt.Value
	text  string
}

type exitSignal struct {
	code int
}

func (e *luaError) Error() string {
	return e.text
}

func raiseValueError(runtime *rt.Runtime, value rt.Value) error {
	text, err := plainString(runtime, value)
	if err != nil {
		return err
	}
	return &luaError{value: value, text: text}
}

func errorToValue(runtime *rt.Runtime, err error) rt.Value {
	var raised *luaError
	if ok := errorAs(err, &raised); ok {
		return raised.value
	}
	return runtime.StringValue(err.Error())
}

func failureValues(runtime *rt.Runtime, err error) []rt.Value {
	values := []rt.Value{rt.NilValue, runtime.StringValue(err.Error())}
	if code, ok := failureCode(err); ok {
		values = append(values, rt.NumberValue(float64(code)))
	}
	return values
}

func failureCode(err error) (int, bool) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ProcessState != nil {
			return exitErr.ProcessState.ExitCode(), true
		}
		return 1, true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return int(errno), true
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && errors.As(pathErr.Err, &errno) {
		return int(errno), true
	}
	return 0, false
}

func errorAs(err error, target **luaError) bool {
	if err == nil {
		return false
	}
	if raised, ok := err.(*luaError); ok {
		*target = raised
		return true
	}
	return false
}

func raiseExit(code int) {
	panic(exitSignal{code: code})
}

func RecoverExitCode(value any) (int, bool) {
	switch sig := value.(type) {
	case exitSignal:
		return sig.code, true
	case *exitSignal:
		if sig == nil {
			return 0, false
		}
		return sig.code, true
	default:
		return 0, false
	}
}

func luaToString(runtime *rt.Runtime, machine *vm.VM, value rt.Value) (string, error) {
	if meta, ok := runtime.GetMetafield(value, "__tostring"); ok {
		results, err := machine.CallValue(meta, []rt.Value{value})
		if err != nil {
			return "", err
		}
		if len(results) == 0 {
			return "", fmt.Errorf("__tostring must return string")
		}
		text, ok := runtime.ToString(results[0])
		if !ok {
			return "", fmt.Errorf("__tostring must return string")
		}
		return text, nil
	}
	return plainString(runtime, value)
}

func plainString(runtime *rt.Runtime, value rt.Value) (string, error) {
	if s, ok := runtime.ToString(value); ok {
		return s, nil
	}
	return value.String(), nil
}

func rawMetafield(runtime *rt.Runtime, meta rt.Value, name string) (rt.Value, bool) {
	h, ok := meta.Handle()
	if !ok || h.Kind() != rt.ObjectTable {
		return rt.NilValue, false
	}
	value, _, found := runtime.Heap().Table(h).GetSymbol(runtime.InternSymbol(name))
	return value, found
}

func luaStringRange(length int, start int, finish int) (int, int) {
	if start < 0 {
		start = length + start + 1
	}
	if finish < 0 {
		finish = length + finish + 1
	}
	if start < 1 {
		start = 1
	}
	if finish > length {
		finish = length
	}
	return start, finish
}

func looksLikeChunkData(source string) bool {
	if len(source) >= 4 && source[0] == 0x1b && source[1] == 'L' && source[2] == 'u' && source[3] == 'a' {
		return true
	}
	return strings.HasPrefix(source, "VXL51\x00")
}

func luaStringStart(length int, start int) int {
	if start < 0 {
		start = length + start + 1
	}
	if start < 1 {
		start = 1
	}
	if start > length+1 {
		start = length + 1
	}
	return start
}

func concatString(runtime *rt.Runtime, value rt.Value) (string, bool) {
	if s, ok := runtime.ToString(value); ok {
		return s, true
	}
	if value.IsNumber() {
		return rt.FormatNumber(value.Number()), true
	}
	return "", false
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func ensurePackageTables(runtime *rt.Runtime) (rt.Value, *rt.Table, *rt.Table, error) {
	packageValue, packageTable, err := ensureGlobalTable(runtime, "package")
	if err != nil {
		return rt.NilValue, nil, nil, err
	}
	_, loadedTable, err := ensureSubtable(runtime, packageTable, "loaded")
	if err != nil {
		return rt.NilValue, nil, nil, err
	}
	return packageValue, packageTable, loadedTable, nil
}

func ensureGlobalTable(runtime *rt.Runtime, name string) (rt.Value, *rt.Table, error) {
	sym := runtime.InternSymbol(name)
	if value, _, found := runtime.Globals().GetSymbol(sym); found {
		h, ok := value.Handle()
		if !ok || h.Kind() != rt.ObjectTable {
			return rt.NilValue, nil, fmt.Errorf("global %s is not a table", name)
		}
		return value, runtime.Heap().Table(h), nil
	}
	handle := runtime.Heap().NewTable(8)
	value := rt.HandleValue(handle)
	runtime.SetGlobalSymbol(sym, value)
	return value, runtime.Heap().Table(handle), nil
}

func ensureSubtable(runtime *rt.Runtime, parent *rt.Table, name string) (rt.Value, *rt.Table, error) {
	sym := runtime.InternSymbol(name)
	if value, _, found := parent.GetSymbol(sym); found {
		h, ok := value.Handle()
		if !ok || h.Kind() != rt.ObjectTable {
			return rt.NilValue, nil, fmt.Errorf("field %s is not a table", name)
		}
		return value, runtime.Heap().Table(h), nil
	}
	handle := runtime.Heap().NewTable(8)
	value := rt.HandleValue(handle)
	parent.SetSymbol(sym, value)
	return value, runtime.Heap().Table(handle), nil
}

func ensureModuleTable(runtime *rt.Runtime, loadedTable *rt.Table, name string) (rt.Value, *rt.Table, error) {
	sym := runtime.InternSymbol(name)
	if value, _, found := loadedTable.GetSymbol(sym); found {
		h, ok := value.Handle()
		if !ok || h.Kind() != rt.ObjectTable {
			return rt.NilValue, nil, fmt.Errorf("module %s is not a table", name)
		}
		return value, runtime.Heap().Table(h), nil
	}
	handle := runtime.Heap().NewTable(8)
	value := rt.HandleValue(handle)
	loadedTable.SetSymbol(sym, value)
	if err := setModuleGlobal(runtime, name, value); err != nil {
		return rt.NilValue, nil, err
	}
	return value, runtime.Heap().Table(handle), nil
}

func setModuleGlobal(runtime *rt.Runtime, name string, value rt.Value) error {
	parts := strings.Split(name, ".")
	if len(parts) == 1 {
		runtime.SetGlobal(name, value)
		return nil
	}
	_, table, err := ensureGlobalTable(runtime, parts[0])
	if err != nil {
		return err
	}
	for _, part := range parts[1 : len(parts)-1] {
		_, next, err := ensureSubtable(runtime, table, part)
		if err != nil {
			return err
		}
		table = next
	}
	table.SetSymbol(runtime.InternSymbol(parts[len(parts)-1]), value)
	return nil
}

func modulePackageName(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 {
		return ""
	}
	return name[:idx+1]
}

const (
	packagePathSeparator = ";"
	packagePathMark      = "?"
	packageExecDirMark   = "!"
	packageIgnoreMark    = "-"
)

func defaultLuaPackagePath() string {
	sep := string(os.PathSeparator)
	return "." + sep + packagePathMark + ".lua" + packagePathSeparator + "." + sep + packagePathMark + sep + "init.lua"
}

func defaultCPackagePath() string {
	sep := string(os.PathSeparator)
	ext := ".so"
	if os.PathSeparator == '\\' {
		ext = ".dll"
	}
	return "." + sep + packagePathMark + ext + packagePathSeparator + "." + sep + "loadall" + ext
}

func packageConfigString() string {
	return string(os.PathSeparator) + "\n" + packagePathSeparator + "\n" + packagePathMark + "\n" + packageExecDirMark + "\n" + packageIgnoreMark
}

func configuredPackagePath(envName string, defaultValue string) string {
	value := os.Getenv(envName)
	if value == "" {
		value = defaultValue
	} else {
		value = strings.ReplaceAll(value, packagePathSeparator+packagePathSeparator, packagePathSeparator+defaultValue+packagePathSeparator)
	}
	executable, err := os.Executable()
	if err != nil {
		return value
	}
	execDir := filepath.Dir(executable)
	return strings.ReplaceAll(value, packageExecDirMark, execDir)
}

func packageSearchPath(pathValue string, moduleName string) (string, string) {
	modulePath := strings.ReplaceAll(moduleName, ".", string(os.PathSeparator))
	var messages strings.Builder
	for _, template := range strings.Split(pathValue, packagePathSeparator) {
		template = strings.TrimSpace(template)
		if template == "" {
			continue
		}
		filename := strings.ReplaceAll(template, packagePathMark, modulePath)
		if isReadableFile(filename) {
			return filename, messages.String()
		}
		messages.WriteString("\n\tno file '")
		messages.WriteString(filename)
		messages.WriteString("'")
	}
	return "", messages.String()
}

func isReadableFile(filename string) bool {
	info, err := os.Stat(filename)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func packageLoaderFuncName(moduleName string) string {
	if index := strings.Index(moduleName, packageIgnoreMark); index >= 0 {
		moduleName = moduleName[index+1:]
	}
	moduleName = strings.ReplaceAll(moduleName, ".", "_")
	return "luaopen_" + moduleName
}
