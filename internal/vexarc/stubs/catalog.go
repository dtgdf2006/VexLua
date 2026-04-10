package stubs

type ID uint32

const (
	StubInvalid ID = iota
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
	StubLuaCall:  {ID: StubLuaCall, Name: "lua-call"},
	StubTailCall: {ID: StubTailCall, Name: "tail-call"},
	StubForPrep:  {ID: StubForPrep, Name: "for-prep"},
	StubForLoop:  {ID: StubForLoop, Name: "for-loop"},
}

func Lookup(id ID) (Descriptor, bool) {
	descriptor, ok := Catalog[id]
	return descriptor, ok
}
