package chunk51

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"vexlua/internal/bytecode"
	rt "vexlua/internal/runtime"
)

var (
	lua51Signature      = []byte{0x1b, 'L', 'u', 'a'}
	errLua51Unsupported = errors.New("lua51 chunk: unsupported proto")
)

const (
	lua51Version        = 0x51
	lua51Format         = 0
	lua51Little         = 1
	lua51IntSize        = 4
	lua51SizeTSize      = 8
	lua51InstrSize      = 4
	lua51NumberSize     = 8
	lua51IntegralNum    = 0
	lua51FieldsPerFlush = 50

	lua51MaxArgBx  = (1 << 18) - 1
	lua51MaxArgSBx = lua51MaxArgBx >> 1
	lua51BitRK     = 1 << 8
)

const (
	lOpMove = iota
	lOpLoadK
	lOpLoadBool
	lOpLoadNil
	lOpGetUpval
	lOpGetGlobal
	lOpGetTable
	lOpSetGlobal
	lOpSetUpval
	lOpSetTable
	lOpNewTable
	lOpSelf
	lOpAdd
	lOpSub
	lOpMul
	lOpDiv
	lOpMod
	lOpPow
	lOpUnm
	lOpNot
	lOpLen
	lOpConcat
	lOpJmp
	lOpEq
	lOpLt
	lOpLe
	lOpTest
	lOpTestSet
	lOpCall
	lOpTailCall
	lOpReturn
	lOpForLoop
	lOpForPrep
	lOpTForLoop
	lOpSetList
	lOpClose
	lOpClosure
	lOpVararg
)

const (
	luaConstNil    byte = 0
	luaConstBool   byte = 1
	luaConstNumber byte = 3
	luaConstString byte = 4
)

type lua51Header struct {
	little      bool
	intSize     int
	sizeTSize   int
	instrSize   int
	numberSize  int
	numberIsInt bool
	byteOrder   binary.ByteOrder
}

type lua51Constant struct {
	kind    byte
	boolVal bool
	numVal  float64
	strVal  string
}

type lua51Proto struct {
	Source    string
	LineStart int
	LineEnd   int
	NumUp     byte
	NumParams byte
	Vararg    byte
	MaxStack  byte
	Code      []uint32
	Constants []lua51Constant
	Children  []*lua51Proto
	LineInfo  []int
	Locals    []bytecode.LocalVar
	UpNames   []string
}

type lua51Builder struct {
	runtime     *rt.Runtime
	proto       *bytecode.Proto
	constants   []lua51Constant
	constMap    map[string]int
	code        []uint32
	lineInfo    []int
	pcMap       map[int]int
	patches     []lua51Patch
	children    []*lua51Proto
	maxStack    int
	scratchMin  int
	pendingBase int
}

type lua51Patch struct {
	codeIndex int
	targetPC  int
}

type lua51Reader struct {
	r      *bytes.Reader
	header lua51Header
}

type lua51Translator struct {
	runtime     *rt.Runtime
	proto       *bytecode.Proto
	pcMap       map[int]int
	patches     []lua51Patch
	nextIC      int
	nilConst    int
	zeroConst   int
	maxStack    int
	scratchAt   int
	pendingBase int
}

func dumpLua51(runtime *rt.Runtime, proto *bytecode.Proto) ([]byte, error) {
	lproto, err := encodeLua51Proto(runtime, proto)
	if err != nil {
		return nil, err
	}
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
	if err := writeLua51Proto(buf, lproto); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func loadLua51(runtime *rt.Runtime, data []byte) (*bytecode.Proto, error) {
	r := bytes.NewReader(data)
	header, err := readLua51Header(r)
	if err != nil {
		return nil, err
	}
	reader := &lua51Reader{r: r, header: header}
	lproto, err := reader.readProto("")
	if err != nil {
		return nil, err
	}
	return translateLua51Proto(runtime, lproto)
}

func encodeLua51Proto(runtime *rt.Runtime, proto *bytecode.Proto) (*lua51Proto, error) {
	b := &lua51Builder{
		runtime:     runtime,
		proto:       proto,
		constants:   make([]lua51Constant, 0, len(proto.Constants)+8),
		constMap:    make(map[string]int, len(proto.Constants)+8),
		code:        make([]uint32, 0, len(proto.Code)+8),
		lineInfo:    make([]int, 0, len(proto.Code)+8),
		pcMap:       make(map[int]int, len(proto.Code)),
		children:    make([]*lua51Proto, 0, len(proto.Children)),
		maxStack:    proto.MaxStack,
		scratchMin:  proto.MaxStack,
		pendingBase: -1,
	}
	for _, child := range proto.Children {
		encoded, err := encodeLua51Proto(runtime, child)
		if err != nil {
			return nil, err
		}
		b.children = append(b.children, encoded)
	}
	for pc, instr := range proto.Code {
		b.pcMap[pc] = len(b.code)
		if err := b.emitInstr(pc, instr); err != nil {
			return nil, err
		}
	}
	for _, patch := range b.patches {
		target, ok := b.pcMap[patch.targetPC]
		if !ok {
			return nil, errLua51Unsupported
		}
		sbx := target - (patch.codeIndex + 1)
		b.code[patch.codeIndex] = encodeAsBx(lOpJmp, 0, sbx)
	}
	source := proto.Source
	if source == "" {
		source = proto.Name
	}
	return &lua51Proto{
		Source:    source,
		LineStart: proto.LineDefined,
		LineEnd:   proto.LastLineDefined,
		NumUp:     byte(len(proto.Upvalues)),
		NumParams: byte(proto.NumParams),
		Vararg:    lua51VarargFlag(proto.Vararg || proto.LineDefined == 0),
		MaxStack:  byte(max(b.maxStack, 1)),
		Code:      b.code,
		Constants: b.constants,
		Children:  b.children,
		LineInfo:  append([]int(nil), b.lineInfo...),
		Locals:    append([]bytecode.LocalVar(nil), proto.LocalsDebug...),
		UpNames:   lua51UpvalueNames(proto.Upvalues),
	}, nil
}

func (b *lua51Builder) emitInstr(pc int, instr bytecode.Instr) error {
	line := b.proto.CurrentLine(pc)
	prevPending := b.pendingBase
	b.pendingBase = -1
	switch instr.Op {
	case bytecode.OpNoop:
		return nil
	case bytecode.OpLoadConst:
		idx, err := b.constantFromValue(b.proto.Constants[instr.D])
		if err != nil {
			return err
		}
		b.emitLua(line, encodeABx(lOpLoadK, int(instr.A), idx))
	case bytecode.OpMove:
		b.emitLua(line, encodeABC(lOpMove, int(instr.A), int(instr.B), 0))
	case bytecode.OpLoadUpvalue:
		b.emitLua(line, encodeABC(lOpGetUpval, int(instr.A), int(instr.B), 0))
	case bytecode.OpStoreUpvalue:
		b.emitLua(line, encodeABC(lOpSetUpval, int(instr.A), int(instr.B), 0))
	case bytecode.OpClosure:
		b.emitLua(line, encodeABx(lOpClosure, int(instr.A), int(instr.D)))
		child := b.proto.Children[instr.D]
		for _, up := range child.Upvalues {
			if up.InParentLocal {
				b.emitLua(line, encodeABC(lOpMove, 0, int(up.Index), 0))
			} else {
				b.emitLua(line, encodeABC(lOpGetUpval, 0, int(up.Index), 0))
			}
		}
	case bytecode.OpNewTable:
		b.emitLua(line, encodeABC(lOpNewTable, int(instr.A), 0, 0))
	case bytecode.OpLoadGlobal:
		idx := b.constantFromString(b.runtime.SymbolName(uint32(instr.D)))
		b.emitLua(line, encodeABx(lOpGetGlobal, int(instr.A), idx))
	case bytecode.OpStoreGlobal:
		idx := b.constantFromString(b.runtime.SymbolName(uint32(instr.D)))
		b.emitLua(line, encodeABx(lOpSetGlobal, int(instr.A), idx))
	case bytecode.OpGetField, bytecode.OpGetFieldIC:
		key := b.constantFromString(b.runtime.SymbolName(uint32(instr.D)))
		b.emitLua(line, encodeABC(lOpGetTable, int(instr.A), int(instr.B), rkConst(key)))
	case bytecode.OpSelf, bytecode.OpSelfIC:
		key := b.constantFromString(b.runtime.SymbolName(uint32(instr.D)))
		b.emitLua(line, encodeABC(lOpSelf, int(instr.A), int(instr.B), rkConst(key)))
	case bytecode.OpSetField:
		key := b.constantFromString(b.runtime.SymbolName(uint32(instr.D)))
		b.emitLua(line, encodeABC(lOpSetTable, int(instr.A), rkConst(key), int(instr.B)))
	case bytecode.OpGetTable:
		b.emitLua(line, encodeABC(lOpGetTable, int(instr.A), int(instr.B), int(instr.C)))
	case bytecode.OpSetTable:
		b.emitLua(line, encodeABC(lOpSetTable, int(instr.A), int(instr.B), int(instr.C)))
	case bytecode.OpAppendTable:
		if prevPending < 0 {
			return errLua51Unsupported
		}
		start := int(instr.B)
		if start <= 0 {
			return errLua51Unsupported
		}
		prefixInRegs := int(instr.C)
		prefix := prefixInRegs
		if prefix == 0 {
			prefix = (start - 1) % lua51FieldsPerFlush
		}
		consumerBase := prevPending - 1 - prefix
		if consumerBase < 0 {
			return errLua51Unsupported
		}
		if consumerBase != int(instr.A) {
			b.emitLua(line, encodeABC(lOpMove, consumerBase, int(instr.A), 0))
		}
		if prefixInRegs > 0 {
			for i := 0; i < prefix; i++ {
				src := int(instr.A) + 1 + i
				dst := consumerBase + 1 + i
				if src != dst {
					b.emitLua(line, encodeABC(lOpMove, dst, src, 0))
				}
			}
		} else {
			blockStart := ((start - 1) / lua51FieldsPerFlush) * lua51FieldsPerFlush
			for i := 0; i < prefix; i++ {
				idx, err := b.constantFromValue(rt.NumberValue(float64(blockStart + i + 1)))
				if err != nil {
					return err
				}
				reg := consumerBase + 1 + i
				b.emitLua(line, encodeABx(lOpLoadK, reg, idx))
				b.emitLua(line, encodeABC(lOpGetTable, reg, consumerBase, reg))
			}
		}
		return b.emitSetList(line, consumerBase, ((start-1)/lua51FieldsPerFlush)+1)
	case bytecode.OpAdd, bytecode.OpAddNum:
		b.emitLua(line, encodeABC(lOpAdd, int(instr.A), int(instr.B), int(instr.C)))
	case bytecode.OpAddConst:
		idx, err := b.constantFromValue(b.proto.Constants[instr.D])
		if err != nil {
			return err
		}
		if idx < lua51BitRK {
			b.emitLua(line, encodeABC(lOpAdd, int(instr.A), int(instr.B), rkConst(idx)))
			return nil
		}
		tmp := b.scratch(1)
		b.emitLua(line, encodeABx(lOpLoadK, tmp, idx))
		b.emitLua(line, encodeABC(lOpAdd, int(instr.A), int(instr.B), tmp))
	case bytecode.OpUnm:
		b.emitLua(line, encodeABC(lOpUnm, int(instr.A), int(instr.B), 0))
	case bytecode.OpSub:
		b.emitLua(line, encodeABC(lOpSub, int(instr.A), int(instr.B), int(instr.C)))
	case bytecode.OpMul:
		b.emitLua(line, encodeABC(lOpMul, int(instr.A), int(instr.B), int(instr.C)))
	case bytecode.OpDiv:
		b.emitLua(line, encodeABC(lOpDiv, int(instr.A), int(instr.B), int(instr.C)))
	case bytecode.OpMod:
		b.emitLua(line, encodeABC(lOpMod, int(instr.A), int(instr.B), int(instr.C)))
	case bytecode.OpPow:
		b.emitLua(line, encodeABC(lOpPow, int(instr.A), int(instr.B), int(instr.C)))
	case bytecode.OpLen:
		b.emitLua(line, encodeABC(lOpLen, int(instr.A), int(instr.B), 0))
	case bytecode.OpConcat:
		base := b.scratch(2)
		b.emitLua(line, encodeABC(lOpMove, base, int(instr.B), 0))
		b.emitLua(line, encodeABC(lOpMove, base+1, int(instr.C), 0))
		b.emitLua(line, encodeABC(lOpConcat, int(instr.A), base, base+1))
	case bytecode.OpEqual:
		b.emitCompareBool(line, lOpEq, int(instr.A), int(instr.B), int(instr.C))
	case bytecode.OpLess:
		b.emitCompareBool(line, lOpLt, int(instr.A), int(instr.B), int(instr.C))
	case bytecode.OpLessEqual:
		b.emitCompareBool(line, lOpLe, int(instr.A), int(instr.B), int(instr.C))
	case bytecode.OpNot:
		b.emitLua(line, encodeABC(lOpNot, int(instr.A), int(instr.B), 0))
	case bytecode.OpIterPairs:
		resultCount := int(instr.C)
		if resultCount <= 0 {
			return errLua51Unsupported
		}
		base := b.scratch(3 + resultCount)
		nextIdx := b.constantFromString("next")
		b.emitLua(line, encodeABx(lOpGetGlobal, base, nextIdx))
		b.emitLua(line, encodeABC(lOpMove, base+1, int(instr.B), 0))
		b.emitLua(line, encodeABC(lOpMove, base+2, int(instr.A), 0))
		b.emitLua(line, encodeABC(lOpCall, base, 3, resultCount+1))
		for i := 0; i < resultCount; i++ {
			if int(instr.A)+i != base+i {
				b.emitLua(line, encodeABC(lOpMove, int(instr.A)+i, base+i, 0))
			}
		}
	case bytecode.OpIterIPairs:
		resultCount := int(instr.C)
		if resultCount <= 0 {
			return errLua51Unsupported
		}
		base := b.scratch(max(resultCount, 2) + 1)
		oneIdx, err := b.constantFromValue(rt.NumberValue(1))
		if err != nil {
			return err
		}
		b.emitLua(line, encodeABx(lOpLoadK, base, oneIdx))
		b.emitLua(line, encodeABC(lOpAdd, base, int(instr.A), base))
		b.emitLua(line, encodeABC(lOpGetTable, base+1, int(instr.B), base))
		b.emitLua(line, encodeABC(lOpLoadNil, base+2, base+2, 0))
		b.emitLua(line, encodeABC(lOpEq, 1, base+1, base+2))
		notFoundIndex := len(b.code)
		b.emitLua(line, 0)
		if int(instr.A) != base {
			b.emitLua(line, encodeABC(lOpMove, int(instr.A), base, 0))
		}
		if resultCount > 1 && int(instr.A)+1 != base+1 {
			b.emitLua(line, encodeABC(lOpMove, int(instr.A)+1, base+1, 0))
		}
		if resultCount > 2 {
			b.emitLua(line, encodeABC(lOpLoadNil, int(instr.A)+2, int(instr.A)+resultCount-1, 0))
		}
		endIndex := len(b.code)
		b.emitLua(line, 0)
		notFoundTarget := len(b.code)
		b.emitLua(line, encodeABC(lOpLoadNil, int(instr.A), int(instr.A)+resultCount-1, 0))
		endTarget := len(b.code)
		b.code[notFoundIndex] = encodeAsBx(lOpJmp, 0, notFoundTarget-(notFoundIndex+1))
		b.code[endIndex] = encodeAsBx(lOpJmp, 0, endTarget-(endIndex+1))
	case bytecode.OpCall:
		base := b.scratch(int(instr.D) + 1)
		b.emitLua(line, encodeABC(lOpMove, base, int(instr.B), 0))
		for i := 0; i < int(instr.D); i++ {
			b.emitLua(line, encodeABC(lOpMove, base+1+i, int(instr.C)+i, 0))
		}
		b.emitLua(line, encodeABC(lOpCall, base, int(instr.D)+1, 2))
		if int(instr.A) != base {
			b.emitLua(line, encodeABC(lOpMove, int(instr.A), base, 0))
		}
	case bytecode.OpTailCall:
		argCount, _, appendPending := bytecode.UnpackCallSpec(instr.D)
		base := 0
		if appendPending {
			var err error
			base, err = b.pendingCallBase(prevPending, argCount)
			if err != nil {
				return err
			}
		} else {
			base = b.scratch(argCount + 1)
		}
		b.emitLua(line, encodeABC(lOpMove, base, int(instr.B), 0))
		for i := 0; i < argCount; i++ {
			b.emitLua(line, encodeABC(lOpMove, base+1+i, int(instr.C)+i, 0))
		}
		bVal := argCount + 1
		if appendPending {
			bVal = 0
		}
		b.emitLua(line, encodeABC(lOpTailCall, base, bVal, 0))
	case bytecode.OpCallMulti:
		argCount, resultCount, appendPending := bytecode.UnpackCallSpec(instr.D)
		base := 0
		if appendPending {
			var err error
			base, err = b.pendingCallBase(prevPending, argCount)
			if err != nil {
				return err
			}
		} else if resultCount == 0 {
			var err error
			base, err = b.pendingOutputBase(pc)
			if err != nil {
				return err
			}
		} else {
			base = b.scratch(max(argCount+1, max(resultCount, 1)))
		}
		b.emitLua(line, encodeABC(lOpMove, base, int(instr.B), 0))
		for i := 0; i < argCount; i++ {
			b.emitLua(line, encodeABC(lOpMove, base+1+i, int(instr.C)+i, 0))
		}
		cVal := 0
		if resultCount > 0 {
			cVal = resultCount + 1
		}
		bVal := argCount + 1
		if appendPending {
			bVal = 0
		}
		b.emitLua(line, encodeABC(lOpCall, base, bVal, cVal))
		if resultCount == 0 {
			b.pendingBase = base
			return nil
		}
		for i := 0; i < resultCount; i++ {
			if int(instr.A)+i != base+i {
				b.emitLua(line, encodeABC(lOpMove, int(instr.A)+i, base+i, 0))
			}
		}
	case bytecode.OpVararg:
		count := int(instr.B)
		bVal := 0
		base := int(instr.A)
		if count == 0 {
			var err error
			base, err = b.pendingOutputBase(pc)
			if err != nil {
				return err
			}
		}
		if count > 0 {
			bVal = count + 1
		}
		b.emitLua(line, encodeABC(lOpVararg, base, bVal, 0))
		if count == 0 {
			b.pendingBase = base
		}
	case bytecode.OpYield:
		yieldCount, resumeCount, appendPending := bytecode.UnpackCallSpec(instr.D)
		base := 0
		if appendPending {
			var err error
			base, err = b.pendingCallBase(prevPending, yieldCount)
			if err != nil {
				return err
			}
		} else if resumeCount == 0 {
			var err error
			base, err = b.pendingOutputBase(pc)
			if err != nil {
				return err
			}
		} else {
			base = b.scratch(max(yieldCount+1, max(resumeCount, 1)))
		}
		coroutineIdx := b.constantFromString("coroutine")
		yieldIdx := b.constantFromString("yield")
		b.emitLua(line, encodeABx(lOpGetGlobal, base, coroutineIdx))
		b.emitLua(line, encodeABC(lOpGetTable, base, base, rkConst(yieldIdx)))
		for i := 0; i < yieldCount; i++ {
			b.emitLua(line, encodeABC(lOpMove, base+1+i, int(instr.B)+i, 0))
		}
		bVal := yieldCount + 1
		if appendPending {
			bVal = 0
		}
		cVal := 0
		if resumeCount > 0 {
			cVal = resumeCount + 1
		}
		b.emitLua(line, encodeABC(lOpCall, base, bVal, cVal))
		if resumeCount == 0 {
			b.pendingBase = base
			return nil
		}
		for i := 0; i < resumeCount; i++ {
			if int(instr.A)+i != base+i {
				b.emitLua(line, encodeABC(lOpMove, int(instr.A)+i, base+i, 0))
			}
		}
	case bytecode.OpReturn:
		b.emitLua(line, encodeABC(lOpReturn, int(instr.A), 2, 0))
	case bytecode.OpReturnMulti:
		count := int(instr.B)
		if count == 0 {
			b.emitLua(line, encodeABC(lOpReturn, 0, 1, 0))
		} else {
			b.emitLua(line, encodeABC(lOpReturn, int(instr.A), count+1, 0))
		}
	case bytecode.OpReturnAppendPending:
		if prevPending < 0 {
			return errLua51Unsupported
		}
		prefixCount := int(instr.B)
		returnBase := prevPending - prefixCount
		if returnBase < 0 {
			return errLua51Unsupported
		}
		for i := 0; i < prefixCount; i++ {
			src := int(instr.A) + i
			dst := returnBase + i
			if src != dst {
				b.emitLua(line, encodeABC(lOpMove, dst, src, 0))
			}
		}
		b.emitLua(line, encodeABC(lOpReturn, returnBase, 0, 0))
	case bytecode.OpJump:
		index := len(b.code)
		b.emitLua(line, 0)
		b.patches = append(b.patches, lua51Patch{codeIndex: index, targetPC: int(instr.D)})
	case bytecode.OpJumpIfFalse:
		index := len(b.code)
		b.emitLua(line, encodeABC(lOpTest, int(instr.A), 0, 0))
		b.emitLua(line, 0)
		b.patches = append(b.patches, lua51Patch{codeIndex: index + 1, targetPC: int(instr.D)})
	case bytecode.OpJumpIfTrue:
		index := len(b.code)
		b.emitLua(line, encodeABC(lOpTest, int(instr.A), 0, 1))
		b.emitLua(line, 0)
		b.patches = append(b.patches, lua51Patch{codeIndex: index + 1, targetPC: int(instr.D)})
	case bytecode.OpLessEqualJump:
		b.emitLua(line, encodeABC(lOpLe, 1, int(instr.A), int(instr.B)))
		index := len(b.code)
		b.emitLua(line, 0)
		b.patches = append(b.patches, lua51Patch{codeIndex: index, targetPC: int(instr.D)})
	case bytecode.OpClose:
		b.emitLua(line, encodeABC(lOpClose, int(instr.A), 0, 0))
	default:
		return errLua51Unsupported
	}
	return nil
}

func (b *lua51Builder) emitLua(line int, instr uint32) {
	if line < 0 {
		line = 0
	}
	b.code = append(b.code, instr)
	b.lineInfo = append(b.lineInfo, line)
}

func (b *lua51Builder) emitCompareBool(line int, op int, target int, left int, right int) {
	b.emitLua(line, encodeABC(op, 0, left, right))
	b.emitLua(line, encodeABC(lOpLoadBool, target, 0, 1))
	b.emitLua(line, encodeABC(lOpLoadBool, target, 1, 0))
}

func (b *lua51Builder) scratch(need int) int {
	base := b.scratchMin
	if base+need > b.maxStack {
		b.maxStack = base + need
	}
	return base
}

func (b *lua51Builder) pendingOutputBase(pc int) (int, error) {
	if pc+1 >= len(b.proto.Code) {
		return b.scratch(1), nil
	}
	next := b.proto.Code[pc+1]
	switch next.Op {
	case bytecode.OpReturnAppendPending:
		return int(next.A) + int(next.B), nil
	case bytecode.OpTailCall:
		argCount, _, appendPending := bytecode.UnpackCallSpec(next.D)
		if !appendPending {
			return b.scratch(1), nil
		}
		consumerBase := b.scratch(argCount + 1)
		return consumerBase + 1 + argCount, nil
	case bytecode.OpCallMulti:
		argCount, _, appendPending := bytecode.UnpackCallSpec(next.D)
		if !appendPending {
			return b.scratch(1), nil
		}
		consumerBase := b.scratch(argCount + 1)
		return consumerBase + 1 + argCount, nil
	case bytecode.OpYield:
		yieldCount, _, appendPending := bytecode.UnpackCallSpec(next.D)
		if !appendPending {
			return b.scratch(1), nil
		}
		consumerBase := b.scratch(yieldCount + 1)
		return consumerBase + 1 + yieldCount, nil
	case bytecode.OpAppendTable:
		prefix := int(next.C)
		if prefix == 0 {
			if int(next.B) <= 0 {
				return 0, errLua51Unsupported
			}
			prefix = (int(next.B) - 1) % lua51FieldsPerFlush
		}
		consumerBase := b.scratch(prefix + 1)
		return consumerBase + 1 + prefix, nil
	default:
		return b.scratch(1), nil
	}
}

func (b *lua51Builder) pendingCallBase(pendingBase int, fixedArgs int) (int, error) {
	if pendingBase < 0 {
		return 0, errLua51Unsupported
	}
	base := pendingBase - 1 - fixedArgs
	if base < 0 {
		return 0, errLua51Unsupported
	}
	return base, nil
}

func (b *lua51Builder) emitSetList(line int, base int, block int) error {
	if block <= 0 {
		return errLua51Unsupported
	}
	if block <= 0x1FF {
		b.emitLua(line, encodeABC(lOpSetList, base, 0, block))
		return nil
	}
	b.emitLua(line, encodeABC(lOpSetList, base, 0, 0))
	b.emitLua(line, uint32(block))
	return nil
}

func (b *lua51Builder) constantFromString(value string) int {
	key := "s:" + value
	if idx, ok := b.constMap[key]; ok {
		return idx
	}
	idx := len(b.constants)
	b.constMap[key] = idx
	b.constants = append(b.constants, lua51Constant{kind: luaConstString, strVal: value})
	return idx
}

func (b *lua51Builder) constantFromValue(value rt.Value) (int, error) {
	switch value.Kind() {
	case rt.KindNil:
		return b.addConst("n:nil", lua51Constant{kind: luaConstNil}), nil
	case rt.KindBool:
		if value.Bool() {
			return b.addConst("b:true", lua51Constant{kind: luaConstBool, boolVal: true}), nil
		}
		return b.addConst("b:false", lua51Constant{kind: luaConstBool, boolVal: false}), nil
	case rt.KindNumber:
		bits := math.Float64bits(value.Number())
		return b.addConst(fmt.Sprintf("d:%x", bits), lua51Constant{kind: luaConstNumber, numVal: value.Number()}), nil
	case rt.KindHandle:
		h, _ := value.Handle()
		if h.Kind() != rt.ObjectString {
			return 0, errLua51Unsupported
		}
		s, _ := b.runtime.ToString(value)
		return b.constantFromString(s), nil
	default:
		return 0, errLua51Unsupported
	}
}

func (b *lua51Builder) addConst(key string, constant lua51Constant) int {
	if idx, ok := b.constMap[key]; ok {
		return idx
	}
	idx := len(b.constants)
	b.constMap[key] = idx
	b.constants = append(b.constants, constant)
	return idx
}

func writeLua51Proto(buf *bytes.Buffer, proto *lua51Proto) error {
	return writeLua51ProtoWithParent(buf, proto, "")
}

func writeLua51ProtoWithParent(buf *bytes.Buffer, proto *lua51Proto, parentSource string) error {
	source := proto.Source
	if source == parentSource {
		source = ""
	}
	if err := writeLua51String(buf, source); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(proto.LineStart)); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(proto.LineEnd)); err != nil {
		return err
	}
	buf.WriteByte(proto.NumUp)
	buf.WriteByte(proto.NumParams)
	buf.WriteByte(proto.Vararg)
	buf.WriteByte(proto.MaxStack)
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(proto.Code))); err != nil {
		return err
	}
	for _, instr := range proto.Code {
		if err := binary.Write(buf, binary.LittleEndian, instr); err != nil {
			return err
		}
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(proto.Constants))); err != nil {
		return err
	}
	for _, constant := range proto.Constants {
		buf.WriteByte(constant.kind)
		switch constant.kind {
		case luaConstNil:
		case luaConstBool:
			if constant.boolVal {
				buf.WriteByte(1)
			} else {
				buf.WriteByte(0)
			}
		case luaConstNumber:
			if err := binary.Write(buf, binary.LittleEndian, constant.numVal); err != nil {
				return err
			}
		case luaConstString:
			if err := writeLua51String(buf, constant.strVal); err != nil {
				return err
			}
		default:
			return errLua51Unsupported
		}
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(proto.Children))); err != nil {
		return err
	}
	for _, child := range proto.Children {
		if err := writeLua51ProtoWithParent(buf, child, proto.Source); err != nil {
			return err
		}
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(proto.LineInfo))); err != nil {
		return err
	}
	for _, line := range proto.LineInfo {
		if err := binary.Write(buf, binary.LittleEndian, uint32(line)); err != nil {
			return err
		}
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(proto.Locals))); err != nil {
		return err
	}
	for _, local := range proto.Locals {
		if err := writeLua51String(buf, local.Name); err != nil {
			return err
		}
		if err := binary.Write(buf, binary.LittleEndian, uint32(local.StartPC)); err != nil {
			return err
		}
		endPC := local.EndPC
		if endPC > 0 {
			endPC--
		}
		if err := binary.Write(buf, binary.LittleEndian, uint32(endPC)); err != nil {
			return err
		}
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(proto.UpNames))); err != nil {
		return err
	}
	for _, name := range proto.UpNames {
		if err := writeLua51String(buf, name); err != nil {
			return err
		}
	}
	return nil
}

func readLua51Header(r *bytes.Reader) (lua51Header, error) {
	sig := make([]byte, len(lua51Signature))
	if _, err := r.Read(sig); err != nil {
		return lua51Header{}, err
	}
	if !bytes.Equal(sig, lua51Signature) {
		return lua51Header{}, fmt.Errorf("invalid lua51 signature")
	}
	version, err := r.ReadByte()
	if err != nil {
		return lua51Header{}, err
	}
	if version != lua51Version {
		return lua51Header{}, fmt.Errorf("unsupported lua version 0x%x", version)
	}
	format, err := r.ReadByte()
	if err != nil {
		return lua51Header{}, err
	}
	if format != lua51Format {
		return lua51Header{}, fmt.Errorf("unsupported lua format %d", format)
	}
	endian, err := r.ReadByte()
	if err != nil {
		return lua51Header{}, err
	}
	intSize, _ := r.ReadByte()
	sizeTSize, _ := r.ReadByte()
	instrSize, _ := r.ReadByte()
	numberSize, _ := r.ReadByte()
	numIntegral, _ := r.ReadByte()
	if instrSize != 4 {
		return lua51Header{}, fmt.Errorf("unsupported instruction size %d", instrSize)
	}
	if numberSize != 8 || numIntegral != 0 {
		return lua51Header{}, fmt.Errorf("unsupported lua number format")
	}
	var order binary.ByteOrder = binary.BigEndian
	little := endian == 1
	if little {
		order = binary.LittleEndian
	}
	return lua51Header{
		little:      little,
		intSize:     int(intSize),
		sizeTSize:   int(sizeTSize),
		instrSize:   int(instrSize),
		numberSize:  int(numberSize),
		numberIsInt: numIntegral != 0,
		byteOrder:   order,
	}, nil
}

func (r *lua51Reader) readProto(parentSource string) (*lua51Proto, error) {
	source, err := r.readString()
	if err != nil {
		return nil, err
	}
	if source == "" {
		source = parentSource
	}
	lineStart, err := r.readInt()
	if err != nil {
		return nil, err
	}
	lineEnd, err := r.readInt()
	if err != nil {
		return nil, err
	}
	numUp, _ := r.r.ReadByte()
	numParams, _ := r.r.ReadByte()
	vararg, _ := r.r.ReadByte()
	maxStack, _ := r.r.ReadByte()
	codeCount, err := r.readInt()
	if err != nil {
		return nil, err
	}
	code := make([]uint32, codeCount)
	for i := range code {
		if err := binary.Read(r.r, r.header.byteOrder, &code[i]); err != nil {
			return nil, err
		}
	}
	constCount, err := r.readInt()
	if err != nil {
		return nil, err
	}
	consts := make([]lua51Constant, 0, constCount)
	for i := 0; i < constCount; i++ {
		kind, err := r.r.ReadByte()
		if err != nil {
			return nil, err
		}
		constant := lua51Constant{kind: kind}
		switch kind {
		case luaConstNil:
		case luaConstBool:
			flag, err := r.r.ReadByte()
			if err != nil {
				return nil, err
			}
			constant.boolVal = flag != 0
		case luaConstNumber:
			if err := binary.Read(r.r, r.header.byteOrder, &constant.numVal); err != nil {
				return nil, err
			}
		case luaConstString:
			value, err := r.readString()
			if err != nil {
				return nil, err
			}
			constant.strVal = value
		default:
			return nil, fmt.Errorf("unsupported lua constant kind %d", kind)
		}
		consts = append(consts, constant)
	}
	childCount, err := r.readInt()
	if err != nil {
		return nil, err
	}
	children := make([]*lua51Proto, 0, childCount)
	for i := 0; i < childCount; i++ {
		child, err := r.readProto(source)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	lineInfoCount, err := r.readInt()
	if err != nil {
		return nil, err
	}
	lineInfo := make([]int, 0, lineInfoCount)
	for i := 0; i < lineInfoCount; i++ {
		line, err := r.readInt()
		if err != nil {
			return nil, err
		}
		lineInfo = append(lineInfo, line)
	}
	locVarCount, err := r.readInt()
	if err != nil {
		return nil, err
	}
	locals := make([]bytecode.LocalVar, 0, locVarCount)
	for i := 0; i < locVarCount; i++ {
		name, err := r.readString()
		if err != nil {
			return nil, err
		}
		startPC, err := r.readInt()
		if err != nil {
			return nil, err
		}
		endPC, err := r.readInt()
		if err != nil {
			return nil, err
		}
		locals = append(locals, bytecode.LocalVar{Name: name, StartPC: startPC, EndPC: endPC + 1})
	}
	upNameCount, err := r.readInt()
	if err != nil {
		return nil, err
	}
	upNames := make([]string, 0, upNameCount)
	for i := 0; i < upNameCount; i++ {
		name, err := r.readString()
		if err != nil {
			return nil, err
		}
		upNames = append(upNames, name)
	}
	return &lua51Proto{Source: source, LineStart: lineStart, LineEnd: lineEnd, NumUp: numUp, NumParams: numParams, Vararg: vararg, MaxStack: maxStack, Code: code, Constants: consts, Children: children, LineInfo: lineInfo, Locals: locals, UpNames: upNames}, nil
}

func translateLua51Proto(runtime *rt.Runtime, proto *lua51Proto) (*bytecode.Proto, error) {
	internal := bytecode.NewProto(proto.Source, int(proto.MaxStack), 0)
	internal.Scripted = true
	internal.Source = proto.Source
	internal.NumParams = int(proto.NumParams)
	internal.Vararg = proto.Vararg != 0
	internal.LineDefined = proto.LineStart
	internal.LastLineDefined = proto.LineEnd
	internal.LocalsDebug = append(internal.LocalsDebug, proto.Locals...)
	internal.Upvalues = make([]bytecode.UpvalueDesc, int(proto.NumUp))
	for i, name := range proto.UpNames {
		if i < len(internal.Upvalues) {
			internal.Upvalues[i].Name = name
		}
	}
	for _, child := range proto.Children {
		translated, err := translateLua51Proto(runtime, child)
		if err != nil {
			return nil, err
		}
		internal.Children = append(internal.Children, translated)
	}
	translator := &lua51Translator{
		runtime:     runtime,
		proto:       internal,
		pcMap:       make(map[int]int, len(proto.Code)),
		nilConst:    internal.AddConstant(rt.NilValue),
		zeroConst:   internal.AddConstant(rt.NumberValue(0)),
		maxStack:    int(proto.MaxStack),
		scratchAt:   int(proto.MaxStack),
		pendingBase: -1,
	}
	for pc := 0; pc < len(proto.Code); pc++ {
		translator.pcMap[pc] = len(internal.Code)
		advance, err := translator.translateInstr(proto, pc)
		if err != nil {
			return nil, err
		}
		pc += advance
	}
	for _, patch := range translator.patches {
		target, ok := translator.pcMap[patch.targetPC]
		if !ok {
			if patch.targetPC == len(proto.Code) {
				target = len(internal.Code)
			} else {
				return nil, errLua51Unsupported
			}
		}
		internal.Code[patch.codeIndex].D = int32(target)
	}
	internal.MaxStack = max(translator.maxStack, 1)
	internal.InlineCaches = translator.nextIC
	return internal, nil
}

func (t *lua51Translator) translateInstr(proto *lua51Proto, pc int) (int, error) {
	instr := proto.Code[pc]
	op := int(instr & 0x3F)
	a := int((instr >> 6) & 0xFF)
	cVal := int((instr >> 14) & 0x1FF)
	bVal := int((instr >> 23) & 0x1FF)
	bx := int((instr >> 14) & 0x3FFFF)
	sbx := bx - lua51MaxArgSBx
	prevPending := t.pendingBase
	t.pendingBase = -1
	switch op {
	case lOpMove:
		t.proto.Emit(bytecode.OpMove, uint16(a), uint16(bVal), 0, 0)
	case lOpLoadK:
		idx, err := t.loadConst(proto.Constants[bx])
		if err != nil {
			return 0, err
		}
		t.proto.Emit(bytecode.OpLoadConst, uint16(a), 0, 0, int32(idx))
	case lOpLoadBool:
		idx := t.boolConst(bVal != 0)
		t.proto.Emit(bytecode.OpLoadConst, uint16(a), 0, 0, int32(idx))
		if cVal != 0 {
			index := len(t.proto.Code)
			t.proto.Emit(bytecode.OpJump, 0, 0, 0, 0)
			t.patches = append(t.patches, lua51Patch{codeIndex: index, targetPC: pc + 2})
		}
	case lOpLoadNil:
		for reg := a; reg <= bVal; reg++ {
			t.proto.Emit(bytecode.OpLoadConst, uint16(reg), 0, 0, int32(t.nilConst))
		}
	case lOpGetUpval:
		t.proto.Emit(bytecode.OpLoadUpvalue, uint16(a), uint16(bVal), 0, 0)
	case lOpSetUpval:
		t.proto.Emit(bytecode.OpStoreUpvalue, uint16(a), uint16(bVal), 0, 0)
	case lOpGetGlobal:
		name, err := t.constString(proto.Constants[bx])
		if err != nil {
			return 0, err
		}
		t.proto.Emit(bytecode.OpLoadGlobal, uint16(a), 0, 0, int32(t.runtime.InternSymbol(name)))
	case lOpSetGlobal:
		name, err := t.constString(proto.Constants[bx])
		if err != nil {
			return 0, err
		}
		t.proto.Emit(bytecode.OpStoreGlobal, uint16(a), 0, 0, int32(t.runtime.InternSymbol(name)))
	case lOpNewTable:
		t.proto.Emit(bytecode.OpNewTable, uint16(a), 0, 0, 0)
	case lOpGetTable:
		keyReg, stringSym, keyIsField, err := t.resolveRK(proto, cVal)
		if err != nil {
			return 0, err
		}
		if keyIsField {
			t.proto.Emit(bytecode.OpGetField, uint16(a), uint16(bVal), uint16(t.nextIC), int32(stringSym))
			t.nextIC++
		} else {
			t.proto.Emit(bytecode.OpGetTable, uint16(a), uint16(bVal), uint16(keyReg), 0)
		}
	case lOpSelf:
		_, stringSym, keyIsField, err := t.resolveRK(proto, cVal)
		if err != nil {
			return 0, err
		}
		if !keyIsField {
			return 0, errLua51Unsupported
		}
		t.proto.Emit(bytecode.OpSelf, uint16(a), uint16(bVal), uint16(t.nextIC), int32(stringSym))
		t.nextIC++
	case lOpSetTable:
		keyReg, stringSym, keyIsField, err := t.resolveRK(proto, bVal)
		if err != nil {
			return 0, err
		}
		valReg, _, _, err := t.resolveRK(proto, cVal)
		if err != nil {
			return 0, err
		}
		if keyIsField {
			t.proto.Emit(bytecode.OpSetField, uint16(a), uint16(valReg), 0, int32(stringSym))
		} else {
			t.proto.Emit(bytecode.OpSetTable, uint16(a), uint16(keyReg), uint16(valReg), 0)
		}
	case lOpAdd, lOpSub, lOpMul, lOpDiv, lOpMod, lOpPow:
		leftReg, _, _, err := t.resolveRK(proto, bVal)
		if err != nil {
			return 0, err
		}
		rightReg, _, _, err := t.resolveRK(proto, cVal)
		if err != nil {
			return 0, err
		}
		opMap := map[int]bytecode.Op{lOpAdd: bytecode.OpAdd, lOpSub: bytecode.OpSub, lOpMul: bytecode.OpMul, lOpDiv: bytecode.OpDiv, lOpMod: bytecode.OpMod, lOpPow: bytecode.OpPow}
		t.proto.Emit(opMap[op], uint16(a), uint16(leftReg), uint16(rightReg), 0)
	case lOpUnm:
		t.proto.Emit(bytecode.OpUnm, uint16(a), uint16(bVal), 0, 0)
	case lOpLen:
		t.proto.Emit(bytecode.OpLen, uint16(a), uint16(bVal), 0, 0)
	case lOpConcat:
		if cVal <= bVal {
			return 0, errLua51Unsupported
		}
		t.proto.Emit(bytecode.OpConcat, uint16(a), uint16(bVal), uint16(bVal+1), 0)
		for reg := bVal + 2; reg <= cVal; reg++ {
			t.proto.Emit(bytecode.OpConcat, uint16(a), uint16(a), uint16(reg), 0)
		}
	case lOpEq, lOpLt, lOpLe:
		advance, err := t.translateCompare(proto, pc, op, a, bVal, cVal)
		if err != nil {
			return 0, err
		}
		return advance, nil
	case lOpNot:
		t.proto.Emit(bytecode.OpNot, uint16(a), uint16(bVal), 0, 0)
	case lOpTest:
		if pc+1 >= len(proto.Code) || int(proto.Code[pc+1]&0x3F) != lOpJmp {
			return 0, errLua51Unsupported
		}
		targetPC := pc + 2 + decodeSBx(proto.Code[pc+1])
		if cVal == 0 {
			index := len(t.proto.Code)
			t.proto.Emit(bytecode.OpJumpIfFalse, uint16(a), 0, 0, 0)
			t.patches = append(t.patches, lua51Patch{codeIndex: index, targetPC: targetPC})
		} else {
			index := len(t.proto.Code)
			t.proto.Emit(bytecode.OpJumpIfTrue, uint16(a), 0, 0, 0)
			t.patches = append(t.patches, lua51Patch{codeIndex: index, targetPC: targetPC})
		}
		return 1, nil
	case lOpTestSet:
		if pc+1 >= len(proto.Code) || int(proto.Code[pc+1]&0x3F) != lOpJmp {
			return 0, errLua51Unsupported
		}
		targetPC := pc + 2 + decodeSBx(proto.Code[pc+1])
		continueIndex := -1
		if cVal == 0 {
			continueIndex = len(t.proto.Code)
			t.proto.Emit(bytecode.OpJumpIfTrue, uint16(bVal), 0, 0, 0)
		} else {
			continueIndex = len(t.proto.Code)
			t.proto.Emit(bytecode.OpJumpIfFalse, uint16(bVal), 0, 0, 0)
		}
		t.proto.Emit(bytecode.OpMove, uint16(a), uint16(bVal), 0, 0)
		jumpIndex := len(t.proto.Code)
		t.proto.Emit(bytecode.OpJump, 0, 0, 0, 0)
		continuePC := len(t.proto.Code)
		t.proto.Code[continueIndex].D = int32(continuePC)
		t.patches = append(t.patches, lua51Patch{codeIndex: jumpIndex, targetPC: targetPC})
		return 1, nil
	case lOpCall:
		argCount := 0
		appendPending := false
		if bVal == 0 {
			if prevPending < 0 || prevPending < a+1 {
				return 0, errLua51Unsupported
			}
			argCount = prevPending - (a + 1)
			appendPending = true
		} else {
			argCount = bVal - 1
			if argCount < 0 {
				argCount = 0
			}
		}
		switch {
		case cVal == 0 && pc+1 < len(proto.Code):
			next := proto.Code[pc+1]
			nextOp := int(next & 0x3F)
			nextA := int((next >> 6) & 0xFF)
			nextB := int((next >> 23) & 0x1FF)
			t.proto.Emit(bytecode.OpCallMulti, uint16(a), uint16(a), uint16(a+1), bytecode.PackCallCountsWithPending(argCount, 0, appendPending))
			t.pendingBase = a
			if nextOp == lOpReturn && nextB == 0 && nextA <= a {
				t.proto.Emit(bytecode.OpReturnAppendPending, uint16(nextA), uint16(a-nextA), 0, 0)
				t.pendingBase = -1
				return 1, nil
			}
		case cVal <= 2 && !appendPending:
			t.proto.Emit(bytecode.OpCall, uint16(a), uint16(a), uint16(a+1), int32(argCount))
		default:
			resultCount := cVal - 1
			if resultCount <= 0 {
				resultCount = 1
			}
			t.proto.Emit(bytecode.OpCallMulti, uint16(a), uint16(a), uint16(a+1), bytecode.PackCallCountsWithPending(argCount, resultCount, appendPending))
		}
	case lOpTailCall:
		argCount := 0
		appendPending := false
		if bVal == 0 {
			if prevPending < 0 || prevPending < a+1 {
				return 0, errLua51Unsupported
			}
			argCount = prevPending - (a + 1)
			appendPending = true
		} else {
			argCount = bVal - 1
			if argCount < 0 {
				argCount = 0
			}
		}
		t.proto.Emit(bytecode.OpTailCall, 0, uint16(a), uint16(a+1), bytecode.PackCallCountsWithPending(argCount, 0, appendPending))
	case lOpReturn:
		switch bVal {
		case 0:
			prefixCount := 0
			if prevPending >= 0 {
				if prevPending < a {
					return 0, errLua51Unsupported
				}
				prefixCount = prevPending - a
			}
			t.proto.Emit(bytecode.OpReturnAppendPending, uint16(a), uint16(prefixCount), 0, 0)
		case 1:
			t.proto.Emit(bytecode.OpReturnMulti, 0, 0, 0, 0)
		case 2:
			t.proto.Emit(bytecode.OpReturn, uint16(a), 0, 0, 0)
		default:
			t.proto.Emit(bytecode.OpReturnMulti, uint16(a), uint16(bVal-1), 0, 0)
		}
	case lOpJmp:
		index := len(t.proto.Code)
		t.proto.Emit(bytecode.OpJump, 0, 0, 0, 0)
		t.patches = append(t.patches, lua51Patch{codeIndex: index, targetPC: pc + 1 + sbx})
	case lOpForPrep:
		t.proto.Emit(bytecode.OpSub, uint16(a), uint16(a), uint16(a+2), 0)
		index := len(t.proto.Code)
		t.proto.Emit(bytecode.OpJump, 0, 0, 0, 0)
		t.patches = append(t.patches, lua51Patch{codeIndex: index, targetPC: pc + 1 + sbx})
	case lOpForLoop:
		t.translateForLoop(pc, a, sbx)
	case lOpTForLoop:
		if pc+1 >= len(proto.Code) || int(proto.Code[pc+1]&0x3F) != lOpJmp {
			return 0, errLua51Unsupported
		}
		t.proto.Emit(bytecode.OpCallMulti, uint16(a+3), uint16(a), uint16(a+1), bytecode.PackCallCounts(2, cVal))
		nilReg := t.scratch()
		t.proto.Emit(bytecode.OpLoadConst, uint16(nilReg), 0, 0, int32(t.nilConst))
		condReg := t.scratch()
		t.proto.Emit(bytecode.OpEqual, uint16(condReg), uint16(a+3), uint16(nilReg), 0)
		exitIndex := len(t.proto.Code)
		t.proto.Emit(bytecode.OpJumpIfTrue, uint16(condReg), 0, 0, 0)
		t.patches = append(t.patches, lua51Patch{codeIndex: exitIndex, targetPC: pc + 2})
		t.proto.Emit(bytecode.OpMove, uint16(a+2), uint16(a+3), 0, 0)
		loopIndex := len(t.proto.Code)
		t.proto.Emit(bytecode.OpJump, 0, 0, 0, 0)
		t.patches = append(t.patches, lua51Patch{codeIndex: loopIndex, targetPC: pc + 1 + decodeSBx(proto.Code[pc+1])})
		return 1, nil
	case lOpSetList:
		block := cVal
		advance := 0
		if block == 0 {
			if pc+1 >= len(proto.Code) {
				return 0, errLua51Unsupported
			}
			block = int(proto.Code[pc+1])
			advance = 1
		}
		startIndex := (block-1)*lua51FieldsPerFlush + 1
		if bVal == 0 {
			if prevPending < 0 || prevPending < a+1 {
				return 0, errLua51Unsupported
			}
			prefix := prevPending - (a + 1)
			t.proto.Emit(bytecode.OpAppendTable, uint16(a), uint16(startIndex), uint16(prefix), 0)
			return advance, nil
		}
		for i := 0; i < bVal; i++ {
			keyConst := t.proto.AddConstant(rt.NumberValue(float64(startIndex + i)))
			keyReg := t.scratch()
			t.proto.Emit(bytecode.OpLoadConst, uint16(keyReg), 0, 0, int32(keyConst))
			t.proto.Emit(bytecode.OpSetTable, uint16(a), uint16(keyReg), uint16(a+1+i), 0)
		}
		return advance, nil
	case lOpClose:
		t.proto.Emit(bytecode.OpClose, uint16(a), 0, 0, 0)
	case lOpClosure:
		child := t.proto.Children[bx]
		for i := 0; i < int(proto.Children[bx].NumUp); i++ {
			next := proto.Code[pc+1+i]
			nextOp := int(next & 0x3F)
			nextB := int((next >> 23) & 0x1FF)
			switch nextOp {
			case lOpMove:
				child.Upvalues[i] = bytecode.UpvalueDesc{InParentLocal: true, Index: uint16(nextB)}
			case lOpGetUpval:
				child.Upvalues[i] = bytecode.UpvalueDesc{InParentLocal: false, Index: uint16(nextB)}
			default:
				return 0, errLua51Unsupported
			}
		}
		t.proto.Emit(bytecode.OpClosure, uint16(a), 0, 0, int32(bx))
		return int(proto.Children[bx].NumUp), nil
	case lOpVararg:
		count := 0
		if bVal > 0 {
			count = bVal - 1
		}
		t.proto.Emit(bytecode.OpVararg, uint16(a), uint16(count), 0, 0)
		if bVal == 0 {
			t.pendingBase = a
			if pc+1 < len(proto.Code) {
				next := proto.Code[pc+1]
				nextOp := int(next & 0x3F)
				nextA := int((next >> 6) & 0xFF)
				nextB := int((next >> 23) & 0x1FF)
				if nextOp == lOpReturn && nextB == 0 && nextA <= a {
					t.proto.Emit(bytecode.OpReturnAppendPending, uint16(nextA), uint16(a-nextA), 0, 0)
					t.pendingBase = -1
					return 1, nil
				}
			}
		}
	default:
		return 0, errLua51Unsupported
	}
	return 0, nil
}

func lua51VarargFlag(vararg bool) byte {
	if vararg {
		return 2
	}
	return 0
}

func (t *lua51Translator) translateCompare(proto *lua51Proto, pc int, op int, a int, bVal int, cVal int) (int, error) {
	leftReg, _, _, err := t.resolveRK(proto, bVal)
	if err != nil {
		return 0, err
	}
	rightReg, _, _, err := t.resolveRK(proto, cVal)
	if err != nil {
		return 0, err
	}
	var internalOp bytecode.Op
	switch op {
	case lOpEq:
		internalOp = bytecode.OpEqual
	case lOpLt:
		internalOp = bytecode.OpLess
	case lOpLe:
		internalOp = bytecode.OpLessEqual
	default:
		return 0, errLua51Unsupported
	}
	if pc+2 < len(proto.Code) {
		first := proto.Code[pc+1]
		second := proto.Code[pc+2]
		if int(first&0x3F) == lOpLoadBool && int(second&0x3F) == lOpLoadBool {
			firstA := int((first >> 6) & 0xFF)
			firstB := int((first >> 23) & 0x1FF)
			firstC := int((first >> 14) & 0x1FF)
			secondA := int((second >> 6) & 0xFF)
			secondB := int((second >> 23) & 0x1FF)
			secondC := int((second >> 14) & 0x1FF)
			if a == 0 && firstA == secondA && firstB == 0 && firstC == 1 && secondB == 1 && secondC == 0 {
				t.proto.Emit(internalOp, uint16(firstA), uint16(leftReg), uint16(rightReg), 0)
				return 2, nil
			}
		}
	}
	if pc+1 < len(proto.Code) && int(proto.Code[pc+1]&0x3F) == lOpJmp {
		temp := t.scratch()
		t.proto.Emit(internalOp, uint16(temp), uint16(leftReg), uint16(rightReg), 0)
		index := len(t.proto.Code)
		if a == 0 {
			t.proto.Emit(bytecode.OpJumpIfFalse, uint16(temp), 0, 0, 0)
		} else {
			t.proto.Emit(bytecode.OpJumpIfTrue, uint16(temp), 0, 0, 0)
		}
		t.patches = append(t.patches, lua51Patch{codeIndex: index, targetPC: pc + 2 + decodeSBx(proto.Code[pc+1])})
		return 1, nil
	}
	return 0, errLua51Unsupported
}

func (t *lua51Translator) translateForLoop(pc int, a int, sbx int) {
	t.proto.Emit(bytecode.OpAdd, uint16(a), uint16(a), uint16(a+2), 0)
	zeroReg := t.scratch()
	t.proto.Emit(bytecode.OpLoadConst, uint16(zeroReg), 0, 0, int32(t.zeroConst))
	signReg := t.scratch()
	condReg := t.scratch()
	t.proto.Emit(bytecode.OpLess, uint16(signReg), uint16(zeroReg), uint16(a+2), 0)
	negativeJump := len(t.proto.Code)
	t.proto.Emit(bytecode.OpJumpIfFalse, uint16(signReg), 0, 0, 0)
	t.proto.Emit(bytecode.OpLessEqual, uint16(condReg), uint16(a), uint16(a+1), 0)
	exitPositive := len(t.proto.Code)
	t.proto.Emit(bytecode.OpJumpIfFalse, uint16(condReg), 0, 0, 0)
	t.proto.Emit(bytecode.OpMove, uint16(a+3), uint16(a), 0, 0)
	continuePositive := len(t.proto.Code)
	t.proto.Emit(bytecode.OpJump, 0, 0, 0, 0)
	negativeStart := len(t.proto.Code)
	t.proto.Code[negativeJump].D = int32(negativeStart)
	t.proto.Emit(bytecode.OpLessEqual, uint16(condReg), uint16(a+1), uint16(a), 0)
	exitNegative := len(t.proto.Code)
	t.proto.Emit(bytecode.OpJumpIfFalse, uint16(condReg), 0, 0, 0)
	t.proto.Emit(bytecode.OpMove, uint16(a+3), uint16(a), 0, 0)
	continueNegative := len(t.proto.Code)
	t.proto.Emit(bytecode.OpJump, 0, 0, 0, 0)
	end := len(t.proto.Code)
	t.proto.Code[exitPositive].D = int32(end)
	t.proto.Code[exitNegative].D = int32(end)
	t.patches = append(t.patches, lua51Patch{codeIndex: continuePositive, targetPC: pc + 1 + sbx})
	t.patches = append(t.patches, lua51Patch{codeIndex: continueNegative, targetPC: pc + 1 + sbx})
}

func (t *lua51Translator) resolveRK(proto *lua51Proto, rk int) (int, uint32, bool, error) {
	if rk&lua51BitRK == 0 {
		return rk, 0, false, nil
	}
	constant := proto.Constants[rk&^lua51BitRK]
	if constant.kind == luaConstString {
		return 0, t.runtime.InternSymbol(constant.strVal), true, nil
	}
	idx, err := t.loadConst(constant)
	if err != nil {
		return 0, 0, false, err
	}
	reg := t.scratch()
	t.proto.Emit(bytecode.OpLoadConst, uint16(reg), 0, 0, int32(idx))
	return reg, 0, false, nil
}

func (t *lua51Translator) loadConst(constant lua51Constant) (int, error) {
	switch constant.kind {
	case luaConstNil:
		return t.nilConst, nil
	case luaConstBool:
		return t.boolConst(constant.boolVal), nil
	case luaConstNumber:
		return t.proto.AddConstant(rt.NumberValue(constant.numVal)), nil
	case luaConstString:
		return t.proto.AddConstant(t.runtime.StringValue(constant.strVal)), nil
	default:
		return 0, errLua51Unsupported
	}
}

func (t *lua51Translator) constString(constant lua51Constant) (string, error) {
	if constant.kind != luaConstString {
		return "", errLua51Unsupported
	}
	return constant.strVal, nil
}

func (t *lua51Translator) boolConst(v bool) int {
	if v {
		return t.proto.AddConstant(rt.TrueValue)
	}
	return t.proto.AddConstant(rt.FalseValue)
}

func (t *lua51Translator) scratch() int {
	reg := t.scratchAt
	t.scratchAt++
	if t.scratchAt > t.maxStack {
		t.maxStack = t.scratchAt
	}
	return reg
}

func (r *lua51Reader) readInt() (int, error) {
	switch r.header.intSize {
	case 4:
		var value uint32
		if err := binary.Read(r.r, r.header.byteOrder, &value); err != nil {
			return 0, err
		}
		return int(value), nil
	default:
		return 0, fmt.Errorf("unsupported int size %d", r.header.intSize)
	}
}

func (r *lua51Reader) readString() (string, error) {
	var length uint64
	switch r.header.sizeTSize {
	case 4:
		var value uint32
		if err := binary.Read(r.r, r.header.byteOrder, &value); err != nil {
			return "", err
		}
		length = uint64(value)
	case 8:
		if err := binary.Read(r.r, r.header.byteOrder, &length); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unsupported size_t size %d", r.header.sizeTSize)
	}
	if length == 0 {
		return "", nil
	}
	buf := make([]byte, length)
	if _, err := r.r.Read(buf); err != nil {
		return "", err
	}
	return string(buf[:len(buf)-1]), nil
}

func writeLua51String(buf *bytes.Buffer, value string) error {
	if value == "" {
		return binary.Write(buf, binary.LittleEndian, uint64(0))
	}
	encoded := append([]byte(value), 0)
	if err := binary.Write(buf, binary.LittleEndian, uint64(len(encoded))); err != nil {
		return err
	}
	_, err := buf.Write(encoded)
	return err
}

func encodeABC(op int, a int, b int, c int) uint32 {
	return uint32(op) | uint32(a)<<6 | uint32(c)<<14 | uint32(b)<<23
}

func encodeABx(op int, a int, bx int) uint32 {
	return uint32(op) | uint32(a)<<6 | uint32(bx)<<14
}

func encodeAsBx(op int, a int, sbx int) uint32 {
	return encodeABx(op, a, sbx+lua51MaxArgSBx)
}

func decodeSBx(instr uint32) int {
	return int((instr>>14)&0x3FFFF) - lua51MaxArgSBx
}

func rkConst(index int) int {
	return index | lua51BitRK
}

func lua51UpvalueNames(upvalues []bytecode.UpvalueDesc) []string {
	if len(upvalues) == 0 {
		return nil
	}
	names := make([]string, 0, len(upvalues))
	for _, up := range upvalues {
		names = append(names, up.Name)
	}
	return names
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
