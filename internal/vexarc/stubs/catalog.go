package stubs

type ID uint32

const (
	StubInvalid ID = iota
	StubGetGlobal
	StubGetTable
	StubSetGlobal
	StubSetTable
	StubGetUpvalue
	StubSetUpvalue
	StubLuaCall
	StubTailCall
	StubForPrep
	StubForLoop
	StubSelf
	StubArithmetic
	StubUnaryTest
	StubLen
	StubCompare
)

type Descriptor struct {
	ID   ID
	Name string
}

var Catalog = map[ID]Descriptor{
	StubGetGlobal:  {ID: StubGetGlobal, Name: "get-global"},
	StubGetTable:   {ID: StubGetTable, Name: "get-table"},
	StubSetGlobal:  {ID: StubSetGlobal, Name: "set-global"},
	StubSetTable:   {ID: StubSetTable, Name: "set-table"},
	StubGetUpvalue: {ID: StubGetUpvalue, Name: "get-upvalue"},
	StubSetUpvalue: {ID: StubSetUpvalue, Name: "set-upvalue"},
	StubLuaCall:    {ID: StubLuaCall, Name: "lua-call"},
	StubTailCall:   {ID: StubTailCall, Name: "tail-call"},
	StubForPrep:    {ID: StubForPrep, Name: "for-prep"},
	StubForLoop:    {ID: StubForLoop, Name: "for-loop"},
	StubSelf:       {ID: StubSelf, Name: "self"},
	StubArithmetic: {ID: StubArithmetic, Name: "arithmetic"},
	StubUnaryTest:  {ID: StubUnaryTest, Name: "unary-test"},
	StubLen:        {ID: StubLen, Name: "len"},
	StubCompare:    {ID: StubCompare, Name: "compare"},
}

func Lookup(id ID) (Descriptor, bool) {
	descriptor, ok := Catalog[id]
	return descriptor, ok
}
