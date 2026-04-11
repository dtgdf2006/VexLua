package stubs

type ID uint32

const (
	StubInvalid ID = iota
	StubGetGlobal
	StubGetTable
	StubSetGlobal
	StubSetTable
	StubLuaCall
	StubTailCall
	StubForPrep
	StubForLoop
)

type Descriptor struct {
	ID   ID
	Name string
}

var Catalog = map[ID]Descriptor{
	StubGetGlobal: {ID: StubGetGlobal, Name: "get-global"},
	StubGetTable:  {ID: StubGetTable, Name: "get-table"},
	StubSetGlobal: {ID: StubSetGlobal, Name: "set-global"},
	StubSetTable:  {ID: StubSetTable, Name: "set-table"},
	StubLuaCall:   {ID: StubLuaCall, Name: "lua-call"},
	StubTailCall:  {ID: StubTailCall, Name: "tail-call"},
	StubForPrep:   {ID: StubForPrep, Name: "for-prep"},
	StubForLoop:   {ID: StubForLoop, Name: "for-loop"},
}

func Lookup(id ID) (Descriptor, bool) {
	descriptor, ok := Catalog[id]
	return descriptor, ok
}
