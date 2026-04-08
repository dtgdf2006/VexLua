package stdlib

import (
	"bufio"
	"fmt"
	gio "io"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"

	rt "vexlua/internal/runtime"
	"vexlua/internal/vm"
)

const defaultLuaFileBufferSize = 4096

type luaFile struct {
	file        *os.File
	name        string
	reader      *bufio.Reader
	writer      *bufio.Writer
	readSource  gio.Reader
	writeTarget gio.Writer
	seeker      gio.Seeker
	closeRaw    func() error
	standard    bool
	noclose     bool
	closedFlag  bool
	bufferMode  string
	bufferSize  int
}

type ioState struct {
	input  rt.Value
	output rt.Value
	stderr rt.Value
	meta   rt.Value
}

func registerIO(runtime *rt.Runtime, machine *vm.VM) error {
	ioHandle := runtime.Heap().NewTable(16)
	ioTable := runtime.Heap().Table(ioHandle)
	methodsHandle := runtime.Heap().NewTable(8)
	methodsTable := runtime.Heap().Table(methodsHandle)
	metaHandle := runtime.Heap().NewTable(4)
	metaTable := runtime.Heap().Table(metaHandle)
	metaValue := rt.HandleValue(metaHandle)
	metaTable.SetSymbol(runtime.InternSymbol("__index"), rt.HandleValue(methodsHandle))
	metaTable.SetSymbol(runtime.InternSymbol("__tostring"), runtime.NewHostFunction("io.file.__tostring", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("file tostring expects 1 argument")
		}
		file, err := checkLuaFile(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		if file.closed() {
			return runtime.StringValue("file (closed)"), nil
		}
		if file.file != nil {
			return runtime.StringValue(fmt.Sprintf("file (%p)", file.file)), nil
		}
		return runtime.StringValue(fmt.Sprintf("file (%p)", file)), nil
	}))
	state := &ioState{meta: metaValue}
	newFileValue := func(file *luaFile, env rt.Value) rt.Value {
		return runtime.NewUserdataValueWithEnv(file, metaValue, env)
	}
	state.input = newFileValue(newLuaFile(os.Stdin, "stdin", true, false, true, true), rt.HandleValue(runtime.GlobalsHandle()))
	state.output = newFileValue(newLuaFile(os.Stdout, "stdout", false, true, true, true), rt.HandleValue(runtime.GlobalsHandle()))
	state.stderr = newFileValue(newLuaFile(os.Stderr, "stderr", false, true, true, true), rt.HandleValue(runtime.GlobalsHandle()))

	closeFile := func(value rt.Value) ([]rt.Value, error) {
		file, err := checkLuaFile(runtime, value)
		if err != nil {
			return nil, err
		}
		if file.noclose {
			return []rt.Value{rt.NilValue, runtime.StringValue("cannot close standard file")}, nil
		}
		if err := file.close(); err != nil {
			return failureValues(runtime, err), nil
		}
		return []rt.Value{rt.TrueValue}, nil
	}
	flushFile := func(value rt.Value) ([]rt.Value, error) {
		file, err := checkLuaFile(runtime, value)
		if err != nil {
			return nil, err
		}
		if err := file.flush(); err != nil {
			return failureValues(runtime, err), nil
		}
		return []rt.Value{rt.TrueValue}, nil
	}
	readFile := func(value rt.Value, specs []rt.Value) ([]rt.Value, error) {
		file, err := checkLuaFile(runtime, value)
		if err != nil {
			return nil, err
		}
		results, eof, err := readBySpecs(runtime, file, specs)
		if err != nil {
			return nil, err
		}
		if eof {
			return []rt.Value{rt.NilValue}, nil
		}
		return results, nil
	}
	writeFile := func(value rt.Value, data []rt.Value) ([]rt.Value, error) {
		file, err := checkLuaFile(runtime, value)
		if err != nil {
			return nil, err
		}
		if err := file.write(runtime, data); err != nil {
			return failureValues(runtime, err), nil
		}
		return []rt.Value{value}, nil
	}
	makeLineIter := func(value rt.Value, autoClose bool) rt.Value {
		return runtime.NewHostFunctionMulti("io.lines.iter", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
			file, err := checkLuaFile(runtime, value)
			if err != nil {
				return nil, err
			}
			results, eof, err := readBySpecs(runtime, file, nil)
			if err != nil {
				return nil, err
			}
			if eof {
				if autoClose {
					_ = file.close()
				}
				return []rt.Value{rt.NilValue}, nil
			}
			return results, nil
		})
	}

	methodsTable.SetSymbol(runtime.InternSymbol("close"), runtime.NewHostFunctionMulti("file:close", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("file:close expects self")
		}
		return closeFile(args[0])
	}))
	methodsTable.SetSymbol(runtime.InternSymbol("flush"), runtime.NewHostFunctionMulti("file:flush", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("file:flush expects self")
		}
		return flushFile(args[0])
	}))
	methodsTable.SetSymbol(runtime.InternSymbol("lines"), runtime.NewHostFunction("file:lines", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("file:lines expects self")
		}
		if _, err := checkLuaFile(runtime, args[0]); err != nil {
			return rt.NilValue, err
		}
		return makeLineIter(args[0], false), nil
	}))
	methodsTable.SetSymbol(runtime.InternSymbol("read"), runtime.NewHostFunctionMulti("file:read", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("file:read expects self")
		}
		return readFile(args[0], args[1:])
	}))
	methodsTable.SetSymbol(runtime.InternSymbol("seek"), runtime.NewHostFunctionMulti("file:seek", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 || len(args) > 3 {
			return nil, fmt.Errorf("file:seek expects self, [whence], [offset]")
		}
		file, err := checkLuaFile(runtime, args[0])
		if err != nil {
			return nil, err
		}
		whence := gio.SeekCurrent
		if len(args) > 1 && args[1].Kind() != rt.KindNil {
			name, ok := runtime.ToString(args[1])
			if !ok {
				return nil, fmt.Errorf("file:seek whence expects string")
			}
			switch name {
			case "set":
				whence = gio.SeekStart
			case "cur":
				whence = gio.SeekCurrent
			case "end":
				whence = gio.SeekEnd
			default:
				return nil, fmt.Errorf("invalid whence %q", name)
			}
		}
		offset := int64(0)
		if len(args) > 2 && args[2].Kind() != rt.KindNil {
			if !args[2].IsNumber() {
				return nil, fmt.Errorf("file:seek offset expects number")
			}
			offset = int64(args[2].Number())
		}
		position, err := file.seek(whence, offset)
		if err != nil {
			return failureValues(runtime, err), nil
		}
		return []rt.Value{rt.NumberValue(float64(position))}, nil
	}))
	methodsTable.SetSymbol(runtime.InternSymbol("setvbuf"), runtime.NewHostFunction("file:setvbuf", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return rt.NilValue, fmt.Errorf("file:setvbuf expects self, mode, [size]")
		}
		file, err := checkLuaFile(runtime, args[0])
		if err != nil {
			return rt.NilValue, err
		}
		mode, ok := runtime.ToString(args[1])
		if !ok {
			return rt.NilValue, fmt.Errorf("file:setvbuf mode expects string")
		}
		size := defaultLuaFileBufferSize
		if len(args) > 2 && args[2].Kind() != rt.KindNil {
			if !args[2].IsNumber() {
				return rt.NilValue, fmt.Errorf("file:setvbuf size expects number")
			}
			size = int(args[2].Number())
			if size < 0 {
				return rt.NilValue, fmt.Errorf("file:setvbuf size must be non-negative")
			}
		}
		switch mode {
		case "no", "full", "line":
			if err := file.setvbuf(mode, size); err != nil {
				return rt.NilValue, err
			}
			return rt.TrueValue, nil
		default:
			return rt.NilValue, fmt.Errorf("invalid buffering mode %q", mode)
		}
	}))
	methodsTable.SetSymbol(runtime.InternSymbol("write"), runtime.NewHostFunctionMulti("file:write", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("file:write expects self")
		}
		return writeFile(args[0], args[1:])
	}))

	ioTable.SetSymbol(runtime.InternSymbol("close"), runtime.NewHostFunctionMulti("io.close", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) > 1 {
			return nil, fmt.Errorf("io.close expects 0 or 1 argument")
		}
		target := state.output
		if len(args) == 1 {
			target = args[0]
		}
		return closeFile(target)
	}))
	ioTable.SetSymbol(runtime.InternSymbol("flush"), runtime.NewHostFunctionMulti("io.flush", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("io.flush expects no arguments")
		}
		return flushFile(state.output)
	}))
	ioTable.SetSymbol(runtime.InternSymbol("input"), runtime.NewHostFunction("io.input", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) == 0 {
			return state.input, nil
		}
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("io.input expects 0 or 1 argument")
		}
		if name, ok := runtime.ToString(args[0]); ok {
			file, err := openLuaFile(name, "r")
			if err != nil {
				return rt.NilValue, err
			}
			state.input = newFileValue(file, machine.CurrentEnv())
			return state.input, nil
		}
		if _, err := checkLuaFile(runtime, args[0]); err != nil {
			return rt.NilValue, err
		}
		state.input = args[0]
		return state.input, nil
	}))
	ioTable.SetSymbol(runtime.InternSymbol("lines"), runtime.NewHostFunction("io.lines", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) > 1 {
			return rt.NilValue, fmt.Errorf("io.lines expects 0 or 1 argument")
		}
		if len(args) == 0 {
			if _, err := checkLuaFile(runtime, state.input); err != nil {
				return rt.NilValue, err
			}
			return makeLineIter(state.input, false), nil
		}
		name, ok := runtime.ToString(args[0])
		if !ok {
			return rt.NilValue, fmt.Errorf("io.lines expects string filename")
		}
		file, err := openLuaFile(name, "r")
		if err != nil {
			return rt.NilValue, err
		}
		value := newFileValue(file, machine.CurrentEnv())
		return makeLineIter(value, true), nil
	}))
	ioTable.SetSymbol(runtime.InternSymbol("open"), runtime.NewHostFunctionMulti("io.open", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 || len(args) > 2 {
			return nil, fmt.Errorf("io.open expects filename and optional mode")
		}
		name, ok := runtime.ToString(args[0])
		if !ok {
			return nil, fmt.Errorf("io.open expects string filename")
		}
		mode := "r"
		if len(args) > 1 && args[1].Kind() != rt.KindNil {
			text, ok := runtime.ToString(args[1])
			if !ok {
				return nil, fmt.Errorf("io.open mode expects string")
			}
			mode = text
		}
		file, err := openLuaFile(name, mode)
		if err != nil {
			return failureValues(runtime, err), nil
		}
		return []rt.Value{newFileValue(file, machine.CurrentEnv())}, nil
	}))
	ioTable.SetSymbol(runtime.InternSymbol("output"), runtime.NewHostFunction("io.output", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) == 0 {
			return state.output, nil
		}
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("io.output expects 0 or 1 argument")
		}
		if name, ok := runtime.ToString(args[0]); ok {
			file, err := openLuaFile(name, "w")
			if err != nil {
				return rt.NilValue, err
			}
			state.output = newFileValue(file, machine.CurrentEnv())
			return state.output, nil
		}
		if _, err := checkLuaFile(runtime, args[0]); err != nil {
			return rt.NilValue, err
		}
		state.output = args[0]
		return state.output, nil
	}))
	ioTable.SetSymbol(runtime.InternSymbol("popen"), runtime.NewHostFunctionMulti("io.popen", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) == 0 || len(args) > 2 {
			return nil, fmt.Errorf("io.popen expects command and optional mode")
		}
		command, ok := runtime.ToString(args[0])
		if !ok {
			return nil, fmt.Errorf("io.popen expects string command")
		}
		mode := "r"
		if len(args) > 1 && args[1].Kind() != rt.KindNil {
			text, ok := runtime.ToString(args[1])
			if !ok {
				return nil, fmt.Errorf("io.popen mode expects string")
			}
			mode = text
		}
		file, err := openLuaPipe(command, mode)
		if err != nil {
			return failureValues(runtime, err), nil
		}
		return []rt.Value{newFileValue(file, machine.CurrentEnv())}, nil
	}))
	ioTable.SetSymbol(runtime.InternSymbol("read"), runtime.NewHostFunctionMulti("io.read", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		return readFile(state.input, args)
	}))
	ioTable.SetSymbol(runtime.InternSymbol("tmpfile"), runtime.NewHostFunctionMulti("io.tmpfile", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("io.tmpfile expects no arguments")
		}
		file, err := os.CreateTemp("", "vexlua-io-*")
		if err != nil {
			return failureValues(runtime, err), nil
		}
		return []rt.Value{newFileValue(newLuaFile(file, file.Name(), true, true, false, false), machine.CurrentEnv())}, nil
	}))
	ioTable.SetSymbol(runtime.InternSymbol("type"), runtime.NewHostFunction("io.type", func(runtime *rt.Runtime, args []rt.Value) (rt.Value, error) {
		if len(args) != 1 {
			return rt.NilValue, fmt.Errorf("io.type expects 1 argument")
		}
		file, ok := luaFileFromValue(runtime, args[0])
		if !ok {
			return rt.NilValue, nil
		}
		if file.closed() {
			return runtime.StringValue("closed file"), nil
		}
		return runtime.StringValue("file"), nil
	}))
	ioTable.SetSymbol(runtime.InternSymbol("write"), runtime.NewHostFunctionMulti("io.write", func(runtime *rt.Runtime, args []rt.Value) ([]rt.Value, error) {
		return writeFile(state.output, args)
	}))
	ioTable.SetSymbol(runtime.InternSymbol("stdin"), state.input)
	ioTable.SetSymbol(runtime.InternSymbol("stdout"), state.output)
	ioTable.SetSymbol(runtime.InternSymbol("stderr"), state.stderr)

	runtime.SetGlobal("io", rt.HandleValue(ioHandle))
	return nil
}

func newLuaFile(file *os.File, name string, readable bool, writable bool, standard bool, noclose bool) *luaFile {
	bufferMode := "full"
	if standard && writable {
		bufferMode = "no"
	}
	luaFile := &luaFile{
		file:       file,
		name:       name,
		seeker:     file,
		closeRaw:   file.Close,
		standard:   standard,
		noclose:    noclose,
		bufferMode: bufferMode,
		bufferSize: defaultLuaFileBufferSize,
	}
	if readable {
		luaFile.readSource = file
	}
	if writable {
		luaFile.writeTarget = file
	}
	return luaFile
}

func luaFileFromValue(runtime *rt.Runtime, value rt.Value) (*luaFile, bool) {
	h, ok := value.Handle()
	if !ok || h.Kind() != rt.ObjectUserdata {
		return nil, false
	}
	userdata := runtime.Heap().Userdata(h)
	file, ok := userdata.Value.(*luaFile)
	return file, ok
}

func checkLuaFile(runtime *rt.Runtime, value rt.Value) (*luaFile, error) {
	file, ok := luaFileFromValue(runtime, value)
	if !ok {
		return nil, fmt.Errorf("expected file handle")
	}
	if file.closed() {
		return nil, fmt.Errorf("attempt to use a closed file")
	}
	return file, nil
}

func openLuaFile(name string, mode string) (*luaFile, error) {
	cleanMode := strings.ReplaceAll(mode, "b", "")
	flag := 0
	readable := false
	writable := false
	switch cleanMode {
	case "", "r":
		flag = os.O_RDONLY
		readable = true
	case "w":
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		writable = true
	case "a":
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		writable = true
	case "r+":
		flag = os.O_RDWR
		readable = true
		writable = true
	case "w+":
		flag = os.O_CREATE | os.O_RDWR | os.O_TRUNC
		readable = true
		writable = true
	case "a+":
		flag = os.O_CREATE | os.O_RDWR | os.O_APPEND
		readable = true
		writable = true
	default:
		return nil, fmt.Errorf("invalid file mode %q", mode)
	}
	file, err := os.OpenFile(name, flag, 0o666)
	if err != nil {
		return nil, err
	}
	return newLuaFile(file, name, readable, writable, false, false), nil
}

func openLuaPipe(command string, mode string) (*luaFile, error) {
	cleanMode := strings.ReplaceAll(mode, "b", "")
	if cleanMode == "" {
		cleanMode = "r"
	}
	cmd := shellCommand(command)
	switch cleanMode {
	case "r":
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &luaFile{
			name:       command,
			readSource: pipe,
			closeRaw: func() error {
				closeErr := pipe.Close()
				waitErr := cmd.Wait()
				if closeErr != nil {
					return closeErr
				}
				return waitErr
			},
			bufferMode: "full",
			bufferSize: defaultLuaFileBufferSize,
		}, nil
	case "w":
		pipe, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &luaFile{
			name:        command,
			writeTarget: pipe,
			closeRaw: func() error {
				closeErr := pipe.Close()
				waitErr := cmd.Wait()
				if closeErr != nil {
					return closeErr
				}
				return waitErr
			},
			bufferMode: "full",
			bufferSize: defaultLuaFileBufferSize,
		}, nil
	default:
		return nil, fmt.Errorf("invalid popen mode %q", mode)
	}
}

func shellCommand(command string) *exec.Cmd {
	if goruntime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-c", command)
}

func (f *luaFile) closed() bool {
	return f == nil || f.closedFlag
}

func (f *luaFile) close() error {
	if f.closed() {
		return nil
	}
	if f.noclose {
		return fmt.Errorf("cannot close standard file")
	}
	if err := f.flush(); err != nil {
		return err
	}
	err := error(nil)
	if f.closeRaw != nil {
		err = f.closeRaw()
	}
	f.file = nil
	f.reader = nil
	f.writer = nil
	f.readSource = nil
	f.writeTarget = nil
	f.seeker = nil
	f.closeRaw = nil
	f.closedFlag = true
	return err
}

func (f *luaFile) flush() error {
	if f.closed() {
		return fmt.Errorf("attempt to use a closed file")
	}
	if f.writer == nil {
		return nil
	}
	return f.writer.Flush()
}

func (f *luaFile) prepareRead() error {
	if f.closed() {
		return fmt.Errorf("attempt to use a closed file")
	}
	if f.readSource == nil {
		return fmt.Errorf("file is not open for reading")
	}
	if f.writer != nil {
		if err := f.writer.Flush(); err != nil {
			return err
		}
	}
	if f.reader == nil {
		f.reader = bufio.NewReaderSize(f.readSource, f.readBufferSize())
	}
	return nil
}

func (f *luaFile) prepareWrite() error {
	if f.closed() {
		return fmt.Errorf("attempt to use a closed file")
	}
	if f.writeTarget == nil {
		return fmt.Errorf("file is not open for writing")
	}
	if f.reader != nil {
		if unread := f.reader.Buffered(); unread > 0 {
			if f.seeker == nil {
				return fmt.Errorf("file is not seekable")
			}
			if _, err := f.seeker.Seek(int64(-unread), gio.SeekCurrent); err != nil {
				return err
			}
		}
		f.reader = nil
	}
	if f.bufferMode == "no" {
		f.writer = nil
		return nil
	}
	if f.writer == nil {
		f.writer = bufio.NewWriterSize(f.writeTarget, f.effectiveBufferSize())
	}
	return nil
}

func (f *luaFile) seek(whence int, offset int64) (int64, error) {
	if f.closed() {
		return 0, fmt.Errorf("attempt to use a closed file")
	}
	if f.seeker == nil {
		return 0, fmt.Errorf("file is not seekable")
	}
	if f.reader != nil {
		if unread := f.reader.Buffered(); unread > 0 {
			if _, err := f.seeker.Seek(int64(-unread), gio.SeekCurrent); err != nil {
				return 0, err
			}
		}
		f.reader = nil
	}
	if f.writer != nil {
		if err := f.writer.Flush(); err != nil {
			return 0, err
		}
	}
	position, err := f.seeker.Seek(offset, whence)
	if err != nil {
		return 0, err
	}
	f.reader = nil
	f.writer = nil
	return position, nil
}

func (f *luaFile) write(runtime *rt.Runtime, values []rt.Value) error {
	if err := f.prepareWrite(); err != nil {
		return err
	}
	flushAfterWrite := f.bufferMode == "no"
	for _, value := range values {
		text, ok := concatString(runtime, value)
		if !ok {
			return fmt.Errorf("string or number expected")
		}
		if f.bufferMode == "no" {
			if _, err := gio.WriteString(f.writeTarget, text); err != nil {
				return err
			}
			continue
		}
		if _, err := f.writer.WriteString(text); err != nil {
			return err
		}
		if f.bufferMode == "line" && strings.Contains(text, "\n") {
			flushAfterWrite = true
		}
	}
	if f.writer != nil && flushAfterWrite {
		return f.writer.Flush()
	}
	return nil
}

func (f *luaFile) setvbuf(mode string, size int) error {
	if f.closed() {
		return fmt.Errorf("attempt to use a closed file")
	}
	if size == 0 {
		size = defaultLuaFileBufferSize
	}
	if err := f.flush(); err != nil {
		return err
	}
	f.bufferMode = mode
	f.bufferSize = size
	f.reader = nil
	f.writer = nil
	return nil
}

func (f *luaFile) effectiveBufferSize() int {
	if f.bufferSize > 0 {
		return f.bufferSize
	}
	return defaultLuaFileBufferSize
}

func (f *luaFile) readBufferSize() int {
	if f.bufferMode == "no" {
		return 1
	}
	return f.effectiveBufferSize()
}

func readBySpecs(runtime *rt.Runtime, file *luaFile, specs []rt.Value) ([]rt.Value, bool, error) {
	if len(specs) == 0 {
		value, ok, err := readLineValue(runtime, file)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, true, nil
		}
		return []rt.Value{value}, false, nil
	}
	results := make([]rt.Value, 0, len(specs))
	for _, spec := range specs {
		value, ok, err := readSpec(runtime, file, spec)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			if len(results) == 0 {
				return nil, true, nil
			}
			results = append(results, rt.NilValue)
			return results, false, nil
		}
		results = append(results, value)
	}
	return results, false, nil
}

func readSpec(runtime *rt.Runtime, file *luaFile, spec rt.Value) (rt.Value, bool, error) {
	if spec.IsNumber() {
		return readCountValue(runtime, file, int(spec.Number()))
	}
	text, ok := runtime.ToString(spec)
	if !ok {
		return rt.NilValue, false, fmt.Errorf("read format expects string or number")
	}
	switch text {
	case "*l":
		return readLineValue(runtime, file)
	case "*a":
		return readAllValue(runtime, file)
	case "*n":
		return readNumberValue(runtime, file)
	default:
		return rt.NilValue, false, fmt.Errorf("unsupported read format %q", text)
	}
}

func readLineValue(runtime *rt.Runtime, file *luaFile) (rt.Value, bool, error) {
	if err := file.prepareRead(); err != nil {
		return rt.NilValue, false, err
	}
	line, err := file.reader.ReadString('\n')
	if err != nil {
		if err == gio.EOF {
			if len(line) == 0 {
				return rt.NilValue, false, nil
			}
		} else {
			return rt.NilValue, false, err
		}
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return runtime.StringValue(line), true, nil
}

func readAllValue(runtime *rt.Runtime, file *luaFile) (rt.Value, bool, error) {
	if err := file.prepareRead(); err != nil {
		return rt.NilValue, false, err
	}
	data, err := gio.ReadAll(file.reader)
	if err != nil {
		return rt.NilValue, false, err
	}
	return runtime.StringValue(string(data)), true, nil
}

func readNumberValue(runtime *rt.Runtime, file *luaFile) (rt.Value, bool, error) {
	if err := file.prepareRead(); err != nil {
		return rt.NilValue, false, err
	}
	var number float64
	if _, err := fmt.Fscan(file.reader, &number); err != nil {
		if err == gio.EOF {
			return rt.NilValue, false, nil
		}
		return rt.NilValue, false, nil
	}
	return rt.NumberValue(number), true, nil
}

func readCountValue(runtime *rt.Runtime, file *luaFile, count int) (rt.Value, bool, error) {
	if count < 0 {
		return rt.NilValue, false, fmt.Errorf("read count must be non-negative")
	}
	if err := file.prepareRead(); err != nil {
		return rt.NilValue, false, err
	}
	if count == 0 {
		return runtime.StringValue(""), true, nil
	}
	buf := make([]byte, count)
	n, err := gio.ReadFull(file.reader, buf)
	if err != nil {
		if err == gio.EOF && n == 0 {
			return rt.NilValue, false, nil
		}
		if err == gio.EOF || err == gio.ErrUnexpectedEOF {
			return runtime.StringValue(string(buf[:n])), true, nil
		}
		return rt.NilValue, false, err
	}
	return runtime.StringValue(string(buf)), true, nil
}
