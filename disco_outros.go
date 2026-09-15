//go:build !windows

package main

import (
	"errors"
	"syscall"
)

func discoCheio(err error) bool {
	return errors.Is(err, syscall.ENOSPC)
}
