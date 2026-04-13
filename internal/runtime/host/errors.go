package host

import (
	"errors"
	"fmt"
)

type BridgeImplementationError struct {
	message string
}

func (err *BridgeImplementationError) Error() string {
	if err == nil {
		return ""
	}
	return err.message
}

func newBridgeImplementationError(format string, args ...any) error {
	return &BridgeImplementationError{message: fmt.Sprintf(format, args...)}
}

func IsBridgeImplementationError(err error) bool {
	if err == nil {
		return false
	}
	var target *BridgeImplementationError
	return errors.As(err, &target)
}
