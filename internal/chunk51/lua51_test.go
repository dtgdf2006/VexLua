package chunk51

import (
	"bytes"
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
