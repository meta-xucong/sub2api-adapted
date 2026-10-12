//go:build windows

package securecred

import (
	"errors"
	"syscall"
	"unsafe"

	"sub2api-model-catalog-manager/internal/config"
)

const credTypeGeneric = 1

type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32      = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW = advapi32.NewProc("CredReadW")
	procCredFree  = advapi32.NewProc("CredFree")
)

func readOSCredential(c config.CredentialConfig) (Value, error) {
	if c.WindowsCredentialTarget == "" || c.SystemdCredentialName != "" {
		return Value{}, ErrUnsupportedSource
	}
	target, err := syscall.UTF16PtrFromString(c.WindowsCredentialTarget)
	if err != nil {
		return Value{}, errors.New("invalid Windows credential target")
	}
	var entry *credentialW
	r, _, _ := procCredReadW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&entry)))
	if r == 0 || entry == nil {
		return Value{}, errors.New("Windows Credential Manager entry is unavailable")
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(entry)))
	if entry.UserName == nil || entry.CredentialBlob == nil || entry.CredentialBlobSize == 0 || entry.CredentialBlobSize > 16*1024 {
		return Value{}, errors.New("Windows Credential Manager entry has an invalid shape")
	}
	username := syscall.UTF16ToString(unsafe.Slice(entry.UserName, 1<<15))
	blob := unsafe.Slice(entry.CredentialBlob, int(entry.CredentialBlobSize))
	apiKey, err := decodeWindowsAPIKey(username, blob)
	if err != nil {
		return Value{}, err
	}
	return Value{APIKey: apiKey}, nil
}
