package codecache

type Block struct {
	addr uintptr
	size uintptr
	mmap []byte
}

func (block *Block) Address() uintptr {
	return block.addr
}

func (block *Block) Size() uintptr {
	return block.size
}

type Cache struct{}

func New() *Cache {
	return &Cache{}
}

func (cache *Cache) Install(code []byte) (*Block, error) {
	return allocExecutable(code)
}

func (cache *Cache) Release(block *Block) error {
	return freeExecutable(block)
}
