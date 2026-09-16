//go:build windows

package main

import (
	"errors"
	"syscall"
)

// ERROR_DISK_FULL (112) e ERROR_HANDLE_DISK_FULL (39): os dois que o Windows
// devolve quando a escrita não cabe.
func discoCheio(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == 112 || errno == 39)
}
