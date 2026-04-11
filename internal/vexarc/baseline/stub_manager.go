package baseline

import (
	"fmt"

	"vexlua/internal/vexarc/abi"
	"vexlua/internal/vexarc/amd64"
	"vexlua/internal/vexarc/codecache"
	"vexlua/internal/vexarc/stubs"
)

type stubManager struct {
	cache              *codecache.Cache
	deoptBlock         *codecache.Block
	legacyDispatchBody *codecache.Block
	stubBlocks         map[stubs.ID]*codecache.Block
	stubBodies         map[stubs.ID]*codecache.Block
	nativeEntries      []*codecache.Block
	nativeBodies       []*codecache.Block
}

func newStubManager(cache *codecache.Cache) (*stubManager, error) {
	if cache == nil {
		return nil, fmt.Errorf("stub manager requires a code cache")
	}
	manager := &stubManager{
		cache:      cache,
		stubBlocks: make(map[stubs.ID]*codecache.Block),
		stubBodies: make(map[stubs.ID]*codecache.Block),
	}
	deoptBlock, err := cache.Install(buildExitStub(compiledStatusDeopt, 0))
	if err != nil {
		return nil, err
	}
	manager.deoptBlock = deoptBlock
	legacyDispatchBody, err := cache.Install(buildBuiltinReturnBody(abi.BuiltinResultDispatchToRuntime, 0))
	if err != nil {
		_ = manager.Release()
		return nil, err
	}
	manager.legacyDispatchBody = legacyDispatchBody
	for _, id := range []stubs.ID{
		stubs.StubGetGlobal,
		stubs.StubGetTable,
		stubs.StubSetGlobal,
		stubs.StubSetTable,
		stubs.StubGetUpvalue,
		stubs.StubSetUpvalue,
		stubs.StubLuaCall,
		stubs.StubTailCall,
		stubs.StubForPrep,
		stubs.StubForLoop,
	} {
		var block *codecache.Block
		switch id {
		case stubs.StubGetGlobal:
			bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(buildGetGlobalBuiltinBody(), uint32(id))
			if err != nil {
				_ = manager.Release()
				return nil, err
			}
			manager.stubBodies[id] = bodyBlock
			block = entryBlock
		case stubs.StubGetTable:
			bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(buildGetTableBuiltinBody(), uint32(id))
			if err != nil {
				_ = manager.Release()
				return nil, err
			}
			manager.stubBodies[id] = bodyBlock
			block = entryBlock
		case stubs.StubSetGlobal:
			bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(buildSetGlobalBuiltinBody(), uint32(id))
			if err != nil {
				_ = manager.Release()
				return nil, err
			}
			manager.stubBodies[id] = bodyBlock
			block = entryBlock
		case stubs.StubSetTable:
			bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(buildSetTableBuiltinBody(), uint32(id))
			if err != nil {
				_ = manager.Release()
				return nil, err
			}
			manager.stubBodies[id] = bodyBlock
			block = entryBlock
		case stubs.StubGetUpvalue:
			bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(buildGetUpvalueBuiltinBody(), 0)
			if err != nil {
				_ = manager.Release()
				return nil, err
			}
			manager.stubBodies[id] = bodyBlock
			block = entryBlock
		case stubs.StubSetUpvalue:
			bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(buildSetUpvalueBuiltinBody(), 0)
			if err != nil {
				_ = manager.Release()
				return nil, err
			}
			manager.stubBodies[id] = bodyBlock
			block = entryBlock
		case stubs.StubForPrep:
			bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(buildForPrepBuiltinBody(), 0)
			if err != nil {
				_ = manager.Release()
				return nil, err
			}
			manager.stubBodies[id] = bodyBlock
			block = entryBlock
		case stubs.StubForLoop:
			bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(buildForLoopBuiltinBody(), 0)
			if err != nil {
				_ = manager.Release()
				return nil, err
			}
			manager.stubBodies[id] = bodyBlock
			block = entryBlock
		case stubs.StubLuaCall:
			bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(buildLuaCallBuiltinBody(), uint32(id))
			if err != nil {
				_ = manager.Release()
				return nil, err
			}
			manager.stubBodies[id] = bodyBlock
			block = entryBlock
		case stubs.StubTailCall:
			bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(buildTailCallBuiltinBody(), uint32(id))
			if err != nil {
				_ = manager.Release()
				return nil, err
			}
			manager.stubBodies[id] = bodyBlock
			block = entryBlock
		default:
			block, err = cache.Install(buildBuiltinEntryThunk(legacyDispatchBody.Address(), uint32(id)))
		}
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
	for id, block := range manager.stubBodies {
		if err := manager.cache.Release(block); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(manager.stubBodies, id)
	}
	for _, block := range manager.nativeEntries {
		if err := manager.cache.Release(block); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	manager.nativeEntries = nil
	for _, block := range manager.nativeBodies {
		if err := manager.cache.Release(block); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	manager.nativeBodies = nil
	if manager.legacyDispatchBody != nil {
		if err := manager.cache.Release(manager.legacyDispatchBody); err != nil && firstErr == nil {
			firstErr = err
		}
		manager.legacyDispatchBody = nil
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

func (manager *stubManager) InstallNativeBuiltin(body []byte) (uintptr, error) {
	if manager == nil || manager.cache == nil {
		return 0, fmt.Errorf("stub manager is not initialized")
	}
	bodyBlock, entryBlock, err := manager.installManagedNativeBuiltin(body, 0)
	if err != nil {
		return 0, err
	}
	manager.nativeBodies = append(manager.nativeBodies, bodyBlock)
	manager.nativeEntries = append(manager.nativeEntries, entryBlock)
	return entryBlock.Address(), nil
}

func (manager *stubManager) installManagedNativeBuiltin(body []byte, dispatchAux uint32) (*codecache.Block, *codecache.Block, error) {
	if len(body) == 0 {
		return nil, nil, fmt.Errorf("native builtin body cannot be empty")
	}
	bodyBlock, err := manager.cache.Install(body)
	if err != nil {
		return nil, nil, err
	}
	entryBlock, err := manager.cache.Install(buildBuiltinEntryThunk(bodyBlock.Address(), dispatchAux))
	if err != nil {
		_ = manager.cache.Release(bodyBlock)
		return nil, nil, err
	}
	return bodyBlock, entryBlock, nil
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
