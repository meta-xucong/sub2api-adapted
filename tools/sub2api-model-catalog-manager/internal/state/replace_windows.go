//go:build windows

package state

import (
	"syscall"
	"unsafe"
)

var (
	kernel32State = syscall.NewLazyDLL("kernel32.dll")
	moveFileExW   = kernel32State.NewProc("MoveFileExW")
)

const moveFileReplaceExisting = 0x1
const moveFileWriteThrough = 0x8

func atomicReplace(oldPath, newPath string) error {
	oldUTF16, err := syscall.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	newUTF16, err := syscall.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	r, _, callErr := moveFileExW.Call(uintptr(unsafe.Pointer(oldUTF16)), uintptr(unsafe.Pointer(newUTF16)), moveFileReplaceExisting|moveFileWriteThrough)
	if r == 0 {
		return callErr
	}
	return nil
}

func syncDirectory(string) error { return nil }
