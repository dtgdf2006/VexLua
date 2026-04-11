package baseline

import (
	"fmt"

	"vexlua/internal/vexarc/amd64"
	"vexlua/internal/vexarc/codecache"
	"vexlua/internal/vexarc/stubs"
)

type stubManager struct {
	cache      *codecache.Cache
	deoptBlock *codecache.Block
	stubBlocks map[stubs.ID]*codecache.Block
}

func newStubManager(cache *codecache.Cache) (*stubManager, error) {
	if cache == nil {
		return nil, fmt.Errorf("stub manager requires a code cache")
	}
	manager := &stubManager{
		cache:      cache,
		stubBlocks: make(map[stubs.ID]*codecache.Block),
	}
	deoptBlock, err := cache.Install(buildExitStub(compiledStatusDeopt, 0))
	if err != nil {
		return nil, err
	}
	manager.deoptBlock = deoptBlock
	for _, id := range []stubs.ID{
		stubs.StubGetGlobal,
		stubs.StubGetTable,
		stubs.StubSetGlobal,
		stubs.StubSetTable,
		stubs.StubLuaCall,
		stubs.StubTailCall,
		stubs.StubForPrep,
		stubs.StubForLoop,
	} {
		block, err := cache.Install(buildExitStub(compiledStatusStub, uint32(id)))
		if err != nil {
			_ = manager.Release()
			return nil, err
		}
		manager.stubBlocks[id] = block
	}
	return manager, nil
}

func (manager *stubManager) Release() error {
	if manager == nil || manager.cache == nil {
		return nil
	}
	var firstErr error
	for id, block := range manager.stubBlocks {
		if err := manager.cache.Release(block); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(manager.stubBlocks, id)
	}
	if manager.deoptBlock != nil {
		if err := manager.cache.Release(manager.deoptBlock); err != nil && firstErr == nil {
			firstErr = err
		}
		manager.deoptBlock = nil
	}
	return firstErr
}

func (manager *stubManager) StubEntry(id stubs.ID) (uintptr, error) {
	if manager == nil {
		return 0, fmt.Errorf("stub manager is nil")
	}
	block, ok := manager.stubBlocks[id]
	if !ok || block == nil {
		return 0, fmt.Errorf("unknown stub entry %d", id)
	}
	return block.Address(), nil
}

func (manager *stubManager) DeoptEntry() (uintptr, error) {
	if manager == nil || manager.deoptBlock == nil {
		return 0, fmt.Errorf("deopt entry is not installed")
	}
	return manager.deoptBlock.Address(), nil
}

func buildExitStub(status uint32, aux uint32) []byte {
	assembler := amd64.NewAssembler(16)
	if status == 0 {
		assembler.XorRegReg(amd64.RegRAX, amd64.RegRAX)
	} else {
		assembler.MoveRegImm32(amd64.RegRAX, status)
	}
	if aux == 0 {
		assembler.XorRegReg(amd64.RegRDX, amd64.RegRDX)
	} else {
		assembler.MoveRegImm32(amd64.RegRDX, aux)
	}
	assembler.Ret()
	return assembler.Buffer().Bytes()
}
