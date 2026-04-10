//go:build !windows && !linux

package heap

import "fmt"

func nativeArenaPageSize() (uint64, error) {
	return 0, fmt.Errorf("native arena backend is not implemented on this platform")
}

func reserveNativeArena(reserveSize uint64, pageSize uint64) (platformNativeArena, error) {
	return nil, fmt.Errorf("native arena backend is not implemented on this platform")
}
