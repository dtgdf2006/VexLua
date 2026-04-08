package chunk51

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"vexlua/internal/bytecode"
	bccompiler "vexlua/internal/compiler"
	rt "vexlua/internal/runtime"
)

func buildLua51ChunkForTest(t *testing.T, proto *lua51Proto) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	buf.Write(lua51Signature)
	buf.WriteByte(lua51Version)
	buf.WriteByte(lua51Format)
	buf.WriteByte(lua51Little)
	buf.WriteByte(lua51IntSize)
	buf.WriteByte(lua51SizeTSize)
	buf.WriteByte(lua51InstrSize)
	buf.WriteByte(lua51NumberSize)
	buf.WriteByte(lua51IntegralNum)
	if err := writeLua51Proto(buf, proto); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func parseLua51ChunkForTest(t *testing.T, data []byte) *lua51Proto {
	t.Helper()
	r := bytes.NewReader(data)
	header, err := readLua51Header(r)
	if err != nil {
		t.Fatal(err)
	}
	reader := &lua51Reader{r: r, header: header}
	proto, err := reader.readProto("")
	if err != nil {
		t.Fatal(err)
	}
	return proto
}

func containsInternalOp(proto *bytecode.Proto, op bytecode.Op) bool {
	for _, instr := range proto.Code {
		if instr.Op == op {
			return true
		}
	}
	return false
}

func containsLuaOp(code []uint32, op int) bool {
	for _, instr := range code {
		if int(instr&0x3F) == op {
			return true
		}
	}
	return false
}

func findLuaOp(code []uint32, op int) (uint32, bool) {
	for _, instr := range code {
		if int(instr&0x3F) == op {
			return instr, true
		}
	}
	return 0, false
}

func compileLua51SourceForTest(t *testing.T, source string) *lua51Proto {
	t.Helper()
	runtime := rt.NewRuntime()
	proto, err := bccompiler.New(runtime).CompileSource(source)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeLua51Proto(runtime, proto)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func luaOp(instr uint32) int {
	return int(instr & 0x3F)
}

func luaB(instr uint32) int {
	return int((instr >> 23) & 0x1FF)
}

func luaC(instr uint32) int {
	return int((instr >> 14) & 0x1FF)
}

func detectLuac51ForTest(t *testing.T) string {
	t.Helper()
	candidates := make([]string, 0, 8)
	if env := strings.TrimSpace(os.Getenv("VEXLUA_LUAC51_BIN")); env != "" {
		candidates = append(candidates, env)
	}
	if env := strings.TrimSpace(os.Getenv("VEXLUA_LUA51_BIN")); env != "" {
		dir := filepath.Dir(env)
		candidates = append(candidates,
			filepath.Join(dir, "luac5.1.exe"),
			filepath.Join(dir, "luac.exe"),
			filepath.Join(dir, "luac5.1"),
			filepath.Join(dir, "luac"),
		)
	}
	candidates = append(candidates, "luac5.1", "luac51", "luac5_1", "luac")
	for _, candidate := range candidates {
		path := candidate
		if _, err := os.Stat(candidate); err != nil {
			resolved, lookErr := exec.LookPath(candidate)
			if lookErr != nil {
				continue
			}
			path = resolved
		}
		cmd := exec.Command(path, "-v")
		output, err := cmd.CombinedOutput()
		version := strings.TrimSpace(string(output))
		if err != nil && version == "" {
			continue
		}
		if strings.Contains(version, "Lua 5.1") {
			return path
		}
	}
	t.Skip("skipping luac golden test: no Lua 5.1 luac executable found")
	return ""
}

func disassembleWithLuacForTest(t *testing.T, luacBin string, filePath string) string {
	t.Helper()
	cmd := exec.Command(luacBin, "-l", filePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("luac disassembly failed for %s: %v\n%s", filePath, err, strings.TrimSpace(string(output)))
	}
	return strings.ReplaceAll(string(output), "\r\n", "\n")
}

func extractLuacFunctionSignatures(listing string) [][]string {
	lines := strings.Split(listing, "\n")
	signatures := make([][]string, 0, 4)
	var current []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "main <") || strings.HasPrefix(trimmed, "function <") {
			if current != nil {
				signatures = append(signatures, current)
			}
			current = []string{}
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 3 || current == nil {
			continue
		}
		opcode := fields[2]
		switch opcode {
		case "CALL":
			if len(fields) >= 6 {
				current = append(current, "CALL B="+fields[4]+" C="+fields[5])
			}
		case "TAILCALL":
			if len(fields) >= 6 {
				current = append(current, "TAILCALL B="+fields[4]+" C="+fields[5])
			}
		case "RETURN":
			if len(fields) >= 5 {
				current = append(current, "RETURN B="+fields[4])
			}
		case "SETLIST":
			if len(fields) >= 6 {
				current = append(current, "SETLIST B="+fields[4]+" C="+fields[5])
			}
		case "VARARG":
			if len(fields) >= 5 {
				current = append(current, "VARARG B="+fields[4])
			}
		}
	}
	if current != nil {
		signatures = append(signatures, current)
	}
	for i := range signatures {
		signatures[i] = normalizeLuacSignatureForCompare(signatures[i])
	}
	return signatures
}

func normalizeLuacSignatureForCompare(signature []string) []string {
	if len(signature) > 1 && signature[len(signature)-1] == "RETURN B=1" {
		trimmed := append([]string(nil), signature[:len(signature)-1]...)
		return trimmed
	}
	return signature
}

func pickLuacSignatureForTest(t *testing.T, signatures [][]string, predicate func([]string) bool, label string) []string {
	t.Helper()
	for _, signature := range signatures {
		if predicate(signature) {
			return signature
		}
	}
	t.Fatalf("no matching luac signature found for %s: %#v", label, signatures)
	return nil
}

func containsSignatureEntry(signature []string, entry string) bool {
	for _, item := range signature {
		if item == entry {
			return true
		}
	}
	return false
}

func dumpLua51SourceForGoldenTest(t *testing.T, source string, chunkPath string) {
	t.Helper()
	runtime := rt.NewRuntime()
	proto, err := bccompiler.New(runtime).CompileSource(source)
	if err != nil {
		t.Fatal(err)
	}
	proto.Name = "@golden.lua"
	proto.SetSourceRecursive("@golden.lua")
	data, err := Dump(runtime, proto)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chunkPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLua51SupportsOpcodeGaps(t *testing.T) {
	t.Run("unm and close", func(t *testing.T) {
		runtime := rt.NewRuntime()
		proto, err := Load(runtime, buildLua51ChunkForTest(t, &lua51Proto{
			Source:   "unm_close.lua",
			MaxStack: 3,
			Code: []uint32{
				encodeABx(lOpLoadK, 0, 0),
				encodeABC(lOpUnm, 1, 0, 0),
				encodeABC(lOpClose, 1, 0, 0),
				encodeABC(lOpReturn, 1, 2, 0),
			},
			Constants: []lua51Constant{{kind: luaConstNumber, numVal: 3}},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !containsInternalOp(proto, bytecode.OpUnm) {
			t.Fatal("expected imported proto to contain OpUnm")
		}
		if !containsInternalOp(proto, bytecode.OpClose) {
			t.Fatal("expected imported proto to contain OpClose")
		}
	})

	t.Run("setlist multret prefix", func(t *testing.T) {
		runtime := rt.NewRuntime()
		proto, err := Load(runtime, buildLua51ChunkForTest(t, &lua51Proto{
			Source:   "setlist.lua",
			Vararg:   lua51VarargFlag(true),
			MaxStack: 4,
			Code: []uint32{
				encodeABC(lOpNewTable, 0, 0, 0),
				encodeABx(lOpLoadK, 1, 0),
				encodeABC(lOpVararg, 2, 0, 0),
				encodeABC(lOpSetList, 0, 0, 1),
				encodeABC(lOpReturn, 0, 1, 0),
			},
			Constants: []lua51Constant{{kind: luaConstNumber, numVal: 11}},
		}))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, instr := range proto.Code {
			if instr.Op == bytecode.OpAppendTable {
				found = true
				if instr.A != 0 || instr.B != 1 || instr.C != 1 {
					t.Fatalf("append table = %+v, want A=0 B=1 C=1", instr)
				}
			}
		}
		if !found {
			t.Fatal("expected imported proto to contain OpAppendTable")
		}
	})

	t.Run("tforloop", func(t *testing.T) {
		runtime := rt.NewRuntime()
		proto, err := Load(runtime, buildLua51ChunkForTest(t, &lua51Proto{
			Source:   "tfor.lua",
			MaxStack: 6,
			Code: []uint32{
				encodeABC(lOpTForLoop, 0, 0, 2),
				encodeAsBx(lOpJmp, 0, -1),
				encodeABC(lOpReturn, 0, 1, 0),
			},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !containsInternalOp(proto, bytecode.OpCallMulti) {
			t.Fatal("expected imported TFORLOOP to become OpCallMulti")
		}
		if !containsInternalOp(proto, bytecode.OpJumpIfTrue) {
			t.Fatal("expected imported TFORLOOP to emit a nil-exit jump")
		}
	})
}

func TestEncodeLua51SupportsInternalOpcodeGaps(t *testing.T) {
	runtime := rt.NewRuntime()

	t.Run("append table with pending multret", func(t *testing.T) {
		proto := bytecode.NewProto("append", 4, 0)
		proto.Scripted = true
		proto.Vararg = true
		valueConst := proto.AddConstant(rt.NumberValue(11))
		keyConst := proto.AddConstant(rt.NumberValue(1))
		proto.Emit(bytecode.OpNewTable, 0, 0, 0, 0)
		proto.Emit(bytecode.OpLoadConst, 1, 0, 0, int32(valueConst))
		proto.Emit(bytecode.OpLoadConst, 2, 0, 0, int32(keyConst))
		proto.Emit(bytecode.OpSetTable, 0, 2, 1, 0)
		proto.Emit(bytecode.OpVararg, 0, 0, 0, 0)
		proto.Emit(bytecode.OpAppendTable, 0, 2, 0, 0)
		proto.Emit(bytecode.OpReturnMulti, 0, 0, 0, 0)

		encoded, err := encodeLua51Proto(runtime, proto)
		if err != nil {
			t.Fatal(err)
		}
		if !containsLuaOp(encoded.Code, lOpSetList) {
			t.Fatal("expected exported proto to contain OP_SETLIST")
		}
	})

	t.Run("pending tailcall", func(t *testing.T) {
		proto := bytecode.NewProto("tail", 2, 0)
		proto.Scripted = true
		proto.Vararg = true
		proto.Emit(bytecode.OpLoadGlobal, 0, 0, 0, int32(runtime.InternSymbol("f")))
		proto.Emit(bytecode.OpVararg, 0, 0, 0, 0)
		proto.Emit(bytecode.OpTailCall, 0, 0, 1, bytecode.PackCallCountsWithPending(0, 0, true))

		encoded, err := encodeLua51Proto(runtime, proto)
		if err != nil {
			t.Fatal(err)
		}
		instr, ok := findLuaOp(encoded.Code, lOpTailCall)
		if !ok {
			t.Fatal("expected exported proto to contain OP_TAILCALL")
		}
		if int((instr>>23)&0x1FF) != 0 {
			t.Fatalf("tailcall B field = %d, want 0 for open-arg tailcall", (instr>>23)&0x1FF)
		}
	})

	t.Run("yield with pending args", func(t *testing.T) {
		proto := bytecode.NewProto("yield", 1, 0)
		proto.Scripted = true
		proto.Vararg = true
		proto.Emit(bytecode.OpVararg, 0, 0, 0, 0)
		proto.Emit(bytecode.OpYield, 0, 0, 0, bytecode.PackCallCountsWithPending(0, 1, true))
		proto.Emit(bytecode.OpReturn, 0, 0, 0, 0)

		encoded, err := encodeLua51Proto(runtime, proto)
		if err != nil {
			t.Fatal(err)
		}
		if !containsLuaOp(encoded.Code, lOpGetGlobal) || !containsLuaOp(encoded.Code, lOpCall) {
			t.Fatal("expected exported yield lowering to contain GETGLOBAL and CALL")
		}
	})

	t.Run("unm and close", func(t *testing.T) {
		proto := bytecode.NewProto("unm_close", 2, 0)
		proto.Scripted = true
		proto.Emit(bytecode.OpUnm, 0, 1, 0, 0)
		proto.Emit(bytecode.OpClose, 1, 0, 0, 0)
		proto.Emit(bytecode.OpReturnMulti, 0, 0, 0, 0)

		encoded, err := encodeLua51Proto(runtime, proto)
		if err != nil {
			t.Fatal(err)
		}
		if !containsLuaOp(encoded.Code, lOpUnm) {
			t.Fatal("expected exported proto to contain OP_UNM")
		}
		if !containsLuaOp(encoded.Code, lOpClose) {
			t.Fatal("expected exported proto to contain OP_CLOSE")
		}
	})

	t.Run("table tail multret keeps open setlist adjacency", func(t *testing.T) {
		encoded := compileLua51SourceForTest(t, `
return function()
	local function triple()
		return 2, 3, 4
	end
	local t = {x = 5, 1, triple()}
	return t.x + t[1] * 10000 + t[2] * 1000 + t[3] * 100 + t[4] * 10
end
`)
		if len(encoded.Children) == 0 {
			t.Fatal("expected encoded root proto to contain child function")
		}
		code := encoded.Children[0].Code
		found := false
		for i := 0; i+1 < len(code); i++ {
			if luaOp(code[i]) == lOpCall && luaC(code[i]) == 0 && luaOp(code[i+1]) == lOpSetList && luaB(code[i+1]) == 0 {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("expected exported table multret lowering to keep CALL followed immediately by open SETLIST")
		}
	})

	t.Run("call arg multret keeps open tailcall adjacency", func(t *testing.T) {
		encoded := compileLua51SourceForTest(t, `
return function()
	local function triple()
		return 2, 3, 4
	end
	local function pack(a, b, c, d)
		return a * 1000 + b * 100 + c * 10 + d
	end
	return pack(1, triple())
end
`)
		if len(encoded.Children) == 0 {
			t.Fatal("expected encoded root proto to contain child function")
		}
		code := encoded.Children[0].Code
		found := false
		for i := 0; i+2 < len(code); i++ {
			if luaOp(code[i]) == lOpCall && luaC(code[i]) == 0 && luaOp(code[i+1]) == lOpTailCall && luaB(code[i+1]) == 0 && luaOp(code[i+2]) == lOpReturn && luaB(code[i+2]) == 0 {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("expected exported call multret lowering to keep CALL, TAILCALL, RETURN as one open-call chain")
		}
	})

	t.Run("vararg spread keeps open vararg call chain", func(t *testing.T) {
		encoded := compileLua51SourceForTest(t, `
return function(...)
	local function spread(...)
		return ...
	end
	local function pack(a, b, c, d)
		return a * 1000 + b * 100 + c * 10 + d
	end
	return pack(1, spread(...))
end
`)
		if len(encoded.Children) == 0 {
			t.Fatal("expected encoded root proto to contain child function")
		}
		code := encoded.Children[0].Code
		found := false
		for i := 0; i+3 < len(code); i++ {
			if luaOp(code[i]) == lOpVararg && luaB(code[i]) == 0 && luaOp(code[i+1]) == lOpCall && luaB(code[i+1]) == 0 && luaC(code[i+1]) == 0 && luaOp(code[i+2]) == lOpTailCall && luaB(code[i+2]) == 0 && luaOp(code[i+3]) == lOpReturn && luaB(code[i+3]) == 0 {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("expected exported vararg spread lowering to keep VARARG, CALL, TAILCALL, RETURN contiguous")
		}
	})
}

func TestDumpLua51MatchesLuacSample(t *testing.T) {
	runtime := rt.NewRuntime()
	proto, err := bccompiler.New(runtime).CompileSource("return function(v) return v + 1 end\n")
	if err != nil {
		t.Fatal(err)
	}
	proto.Name = "@input.lua"
	proto.SetSourceRecursive("@input.lua")

	data, err := Dump(runtime, proto)
	if err != nil {
		t.Fatal(err)
	}

	want := []byte{
		0x1B, 0x4C, 0x75, 0x61, 0x51, 0x00, 0x01, 0x04, 0x08, 0x04, 0x08, 0x00,
		0x0B, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x40, 0x69, 0x6E, 0x70,
		0x75, 0x74, 0x2E, 0x6C, 0x75, 0x61, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x02, 0x03, 0x00, 0x00, 0x00, 0x24,
		0x00, 0x00, 0x00, 0x1E, 0x00, 0x00, 0x01, 0x1E, 0x00, 0x80, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x02, 0x03, 0x00, 0x00, 0x00, 0x4C, 0x00, 0x40, 0x00, 0x5E,
		0x00, 0x00, 0x01, 0x1E, 0x00, 0x80, 0x00, 0x01, 0x00, 0x00, 0x00, 0x03,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF0, 0x3F, 0x00, 0x00, 0x00, 0x00,
		0x03, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x76, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00, 0x01, 0x00,
		0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	if !bytes.Equal(data, want) {
		gotProto := parseLua51ChunkForTest(t, data)
		wantProto := parseLua51ChunkForTest(t, want)
		gotChild := gotProto.Children[0]
		wantChild := wantProto.Children[0]
		t.Fatalf(
			"dump mismatch\n got: % X\nwant: % X\n got source=%q child lineinfo=%d locals=%d upnames=%d\nwant source=%q child lineinfo=%d locals=%d upnames=%d",
			data,
			want,
			gotProto.Source,
			len(gotChild.LineInfo),
			len(gotChild.Locals),
			len(gotChild.UpNames),
			wantProto.Source,
			len(wantChild.LineInfo),
			len(wantChild.Locals),
			len(wantChild.UpNames),
		)
	}
}

func TestDumpLua51MatchesOfficialLuacGoldenSignatures(t *testing.T) {
	luacBin := detectLuac51ForTest(t)
	cases := []struct {
		name      string
		source    string
		predicate func([]string) bool
	}{
		{
			name: "table_tail_multret",
			source: `
return function()
	local function triple()
		return 2, 3, 4
	end
	local t = {x = 5, 1, triple()}
	return t.x + t[1] * 10000 + t[2] * 1000 + t[3] * 100 + t[4] * 10
end
`,
			predicate: func(signature []string) bool {
				return containsSignatureEntry(signature, "SETLIST B=0 C=1")
			},
		},
		{
			name: "call_arg_multret",
			source: `
return function()
	local function triple()
		return 2, 3, 4
	end
	local function pack(a, b, c, d)
		return a * 1000 + b * 100 + c * 10 + d
	end
	return pack(1, triple())
end
`,
			predicate: func(signature []string) bool {
				return containsSignatureEntry(signature, "CALL B=1 C=0") && containsSignatureEntry(signature, "TAILCALL B=0 C=0")
			},
		},
		{
			name: "vararg_spread",
			source: `
return function(...)
	local function spread(...)
		return ...
	end
	local function pack(a, b, c, d)
		return a * 1000 + b * 100 + c * 10 + d
	end
	return pack(1, spread(...))
end
`,
			predicate: func(signature []string) bool {
				return containsSignatureEntry(signature, "VARARG B=0") && containsSignatureEntry(signature, "CALL B=0 C=0") && containsSignatureEntry(signature, "TAILCALL B=0 C=0")
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tempDir := t.TempDir()
			sourcePath := filepath.Join(tempDir, testCase.name+".lua")
			chunkPath := filepath.Join(tempDir, testCase.name+".luac")
			if err := os.WriteFile(sourcePath, []byte(testCase.source), 0o644); err != nil {
				t.Fatal(err)
			}
			officialListing := disassembleWithLuacForTest(t, luacBin, sourcePath)
			dumpLua51SourceForGoldenTest(t, testCase.source, chunkPath)
			vexListing := disassembleWithLuacForTest(t, luacBin, chunkPath)
			officialSignature := pickLuacSignatureForTest(t, extractLuacFunctionSignatures(officialListing), testCase.predicate, testCase.name+" official")
			vexSignature := pickLuacSignatureForTest(t, extractLuacFunctionSignatures(vexListing), testCase.predicate, testCase.name+" vex")
			if !reflect.DeepEqual(vexSignature, officialSignature) {
				t.Fatalf("luac golden signature mismatch\nofficial=%v\nvex=%v\n--- official ---\n%s\n--- vex ---\n%s", officialSignature, vexSignature, strings.TrimSpace(officialListing), strings.TrimSpace(vexListing))
			}
		})
	}
}
