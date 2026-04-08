package vexlua

import (
	"errors"
	"fmt"
)

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	if e == nil {
		return "os.exit"
	}
	return fmt.Sprintf("os.exit(%d)", e.Code)
}

func ExitCode(err error) (int, bool) {
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr == nil {
		return 0, false
	}
	return exitErr.Code, true
}
