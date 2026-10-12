//go:build windows

package officialcatalog

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	securityInformationDACL         = 0x00000004
	protectedDACL                   = 0x80000000
	securityDescriptorRevision      = 1
	securityDescriptorProtectedDACL = 0x1000
	fileGenericRead                 = 0x80000000
	fileGenericWrite                = 0x40000000
	fileShareAll                    = 0x00000007
	openAlways                      = 4
	fileAttributeNormal             = 0x00000080
	lockfileExclusiveLock           = 0x00000002
	lockfileFailImmediately         = 0x00000001
	moveFileReplaceExisting         = 0x00000001
	moveFileWriteThrough            = 0x00000008
	tokenQuery                      = 0x0008
	tokenUser                       = 1
	aclSizeInformation              = 2
	accessAllowedACEType            = 0
	fileAccessMask                  = 0x001F01FF
	objectInheritACE                = 0x01
	containerInheritACE             = 0x02
)

var (
	kernel32                         = syscall.NewLazyDLL("kernel32.dll")
	advapi32                         = syscall.NewLazyDLL("advapi32.dll")
	procCreateFileW                  = kernel32.NewProc("CreateFileW")
	procReadProcessMemory            = kernel32.NewProc("ReadProcessMemory")
	procLockFileEx                   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx                 = kernel32.NewProc("UnlockFileEx")
	procMoveFileExW                  = kernel32.NewProc("MoveFileExW")
	procGetCurrentProcess            = kernel32.NewProc("GetCurrentProcess")
	procOpenProcessToken             = advapi32.NewProc("OpenProcessToken")
	procGetTokenInformation          = advapi32.NewProc("GetTokenInformation")
	procConvertSidToStringSidW       = advapi32.NewProc("ConvertSidToStringSidW")
	procConvertStringSidToSidW       = advapi32.NewProc("ConvertStringSidToSidW")
	procConvertStringSDToSD          = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procSetFileSecurityW             = advapi32.NewProc("SetFileSecurityW")
	procGetFileSecurityW             = advapi32.NewProc("GetFileSecurityW")
	procGetSecurityDescriptorControl = advapi32.NewProc("GetSecurityDescriptorControl")
	procGetSecurityDescriptorDACL    = advapi32.NewProc("GetSecurityDescriptorDacl")
	procGetACLInformation            = advapi32.NewProc("GetAclInformation")
	procGetAce                       = advapi32.NewProc("GetAce")
	procEqualSid                     = advapi32.NewProc("EqualSid")
	procLocalFree                    = kernel32.NewProc("LocalFree")
	currentSIDOnce                   sync.Once
	currentSID                       string
	currentSIDErr                    error
)

type winACLSizeInformation struct {
	AceCount      uint32
	AclBytesInUse uint32
	AclBytesFree  uint32
}

func secureCacheTree(root string) error {
	if root == "" {
		return errors.New("cache directory unavailable")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	// Do not walk mutable cache children here: another process may atomically
	// replace a snapshot while this initializer is traversing the tree. Every
	// child directory and artifact is instead verified immediately before use.
	return securePrivateDir(root)
}

func securePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("cache path is not a real directory")
	}
	return setAndVerifyPrivateACL(path, true)
}

func securePrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("cache path is not a regular file")
	}
	return setAndVerifyPrivateACL(path, false)
}

func setAndVerifyPrivateACL(path string, directory bool) error {
	sid, err := userSIDString()
	if err != nil {
		return err
	}
	flags := ""
	if directory {
		flags = "OICI"
	}
	sddl := "D:P(A;" + flags + ";FA;;;" + sid + ")"
	sddlPtr, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		return err
	}
	var sd uintptr
	var sdSize uint32
	converted, _, callErr := procConvertStringSDToSD.Call(uintptr(unsafe.Pointer(sddlPtr)), securityDescriptorRevision, uintptr(unsafe.Pointer(&sd)), uintptr(unsafe.Pointer(&sdSize)))
	if converted == 0 {
		return fmt.Errorf("convert cache security descriptor: %w", callErr)
	}
	defer procLocalFree.Call(sd)
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	set, _, callErr := procSetFileSecurityW.Call(uintptr(unsafe.Pointer(pathPtr)), securityInformationDACL|protectedDACL, sd)
	if set == 0 {
		return fmt.Errorf("set cache DACL: %w", callErr)
	}
	return verifyPrivateACL(path, directory, sid)
}

func verifyPrivateACL(path string, directory bool, sidText string) error {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var needed uint32
	_, _, callErr := procGetFileSecurityW.Call(uintptr(unsafe.Pointer(pathPtr)), securityInformationDACL, 0, 0, uintptr(unsafe.Pointer(&needed)))
	if needed == 0 {
		return fmt.Errorf("query cache DACL size: %w", callErr)
	}
	buffer := make([]byte, needed)
	got, _, callErr := procGetFileSecurityW.Call(uintptr(unsafe.Pointer(pathPtr)), securityInformationDACL, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(&needed)))
	if got == 0 {
		return fmt.Errorf("read cache DACL: %w", callErr)
	}
	var control uint16
	var revision uint32
	ok, _, callErr := procGetSecurityDescriptorControl.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&control)), uintptr(unsafe.Pointer(&revision)))
	if ok == 0 || control&securityDescriptorProtectedDACL == 0 {
		if ok == 0 {
			return callErr
		}
		return errors.New("cache DACL is not protected")
	}
	var present, defaulted int32
	var acl uintptr
	ok, _, callErr = procGetSecurityDescriptorDACL.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&acl)), uintptr(unsafe.Pointer(&defaulted)))
	if ok == 0 || present == 0 || acl == 0 {
		if ok == 0 {
			return callErr
		}
		return errors.New("cache DACL is absent")
	}
	var aclInfo winACLSizeInformation
	ok, _, callErr = procGetACLInformation.Call(acl, uintptr(unsafe.Pointer(&aclInfo)), unsafe.Sizeof(aclInfo), aclSizeInformation)
	if ok == 0 || aclInfo.AceCount != 1 {
		if ok == 0 {
			return callErr
		}
		return errors.New("cache DACL contains unexpected principals")
	}
	var ace uintptr
	ok, _, callErr = procGetAce.Call(acl, 0, uintptr(unsafe.Pointer(&ace)))
	if ok == 0 || ace == 0 {
		return callErr
	}
	// GetAce returns an address as an integer. Copy its fixed header through
	// ReadProcessMemory instead of converting that uintptr back to a Go pointer.
	var aceHeader [8]byte
	var bytesRead uintptr
	process, _, _ := procGetCurrentProcess.Call()
	ok, _, callErr = procReadProcessMemory.Call(
		process,
		ace,
		uintptr(unsafe.Pointer(&aceHeader[0])),
		uintptr(len(aceHeader)),
		uintptr(unsafe.Pointer(&bytesRead)),
	)
	if ok == 0 {
		return callErr
	}
	if bytesRead != uintptr(len(aceHeader)) {
		return errors.New("cache DACL ACE header could not be read")
	}
	if aceHeader[0] != accessAllowedACEType {
		return errors.New("cache DACL ACE type is invalid")
	}
	expectedFlags := byte(0)
	if directory {
		expectedFlags = objectInheritACE | containerInheritACE
	}
	if aceHeader[1] != expectedFlags {
		return errors.New("cache DACL inheritance flags are invalid")
	}
	mask := binary.LittleEndian.Uint32(aceHeader[4:8])
	if mask != fileAccessMask {
		return errors.New("cache DACL access mask is invalid")
	}
	sid, err := userSIDPointerFromString(sidText)
	if err != nil {
		return err
	}
	defer procLocalFree.Call(sid)
	currentACE := ace + 8
	equal, _, _ := procEqualSid.Call(sid, currentACE)
	if equal == 0 {
		return errors.New("cache DACL is not limited to the current user SID")
	}
	return nil
}

func userSIDString() (string, error) {
	currentSIDOnce.Do(func() { currentSID, currentSIDErr = queryCurrentSID() })
	return currentSID, currentSIDErr
}

func queryCurrentSID() (string, error) {
	process, _, _ := procGetCurrentProcess.Call()
	var token syscall.Handle
	ok, _, callErr := procOpenProcessToken.Call(process, tokenQuery, uintptr(unsafe.Pointer(&token)))
	if ok == 0 {
		return "", callErr
	}
	defer syscall.CloseHandle(token)
	var needed uint32
	procGetTokenInformation.Call(uintptr(token), tokenUser, 0, 0, uintptr(unsafe.Pointer(&needed)))
	if needed < 8 || needed > 1<<20 {
		return "", errors.New("current Windows user SID is unavailable")
	}
	buffer := make([]byte, needed)
	ok, _, callErr = procGetTokenInformation.Call(uintptr(token), tokenUser, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 {
		return "", callErr
	}
	sid := *(*uintptr)(unsafe.Pointer(&buffer[0]))
	var sidString *uint16
	ok, _, callErr = procConvertSidToStringSidW.Call(sid, uintptr(unsafe.Pointer(&sidString)))
	if ok == 0 {
		return "", callErr
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(sidString)))
	return syscall.UTF16ToString(unsafe.Slice(sidString, 256)), nil
}

func userSIDPointerFromString(sidText string) (uintptr, error) {
	actual, err := queryCurrentSID()
	if err != nil || actual != sidText {
		return 0, errors.New("current Windows user SID changed during ACL verification")
	}
	ptr, err := syscall.UTF16PtrFromString(sidText)
	if err != nil {
		return 0, err
	}
	var sid uintptr
	ok, _, callErr := procConvertStringSidToSidW.Call(uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&sid)))
	if ok == 0 {
		return 0, callErr
	}
	// ConvertStringSidToSid allocates with LocalAlloc; the caller frees it after
	// the ACL comparison. The pointer contains only the current account SID.
	return sid, nil
}

func atomicReplace(source, destination string) error {
	sourcePtr, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	flags := moveFileReplaceExisting | moveFileWriteThrough
	ok, _, callErr := procMoveFileExW.Call(uintptr(unsafe.Pointer(sourcePtr)), uintptr(unsafe.Pointer(destinationPtr)), uintptr(flags))
	if ok == 0 {
		return callErr
	}
	return nil
}

type fileLock struct {
	handle  syscall.Handle
	overlap syscall.Overlapped
	waited  bool
}

func acquireOSLock(ctx context.Context, path string) (osFileLock, error) {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	if err := securePrivateDir(parent); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("cache lock path is not a regular file")
		}
	} else if os.IsNotExist(err) {
		file, createErr := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if createErr != nil {
			return nil, createErr
		}
		_ = file.Close()
	} else {
		return nil, err
	}
	if err := securePrivateFile(path); err != nil {
		return nil, err
	}
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, _, callErr := procCreateFileW.Call(uintptr(unsafe.Pointer(pathPtr)), fileGenericRead|fileGenericWrite, fileShareAll, 0, openAlways, fileAttributeNormal, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return nil, callErr
	}
	lock := &fileLock{handle: syscall.Handle(h)}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	waited := false
	for {
		ok, _, lockErr := procLockFileEx.Call(uintptr(lock.handle), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(&lock.overlap)))
		if ok != 0 {
			lock.waited = waited
			return lock, nil
		}
		if errno, ok := lockErr.(syscall.Errno); !ok || (errno != syscall.Errno(33) && errno != syscall.ERROR_IO_PENDING) {
			_ = syscall.CloseHandle(lock.handle)
			return nil, lockErr
		}
		select {
		case <-ctx.Done():
			_ = syscall.CloseHandle(lock.handle)
			return nil, ctx.Err()
		case <-ticker.C:
			waited = true
		}
	}
}

func (l *fileLock) Waited() bool { return l != nil && l.waited }

func (l *fileLock) Close() error {
	if l == nil || l.handle == syscall.InvalidHandle {
		return nil
	}
	ok, _, unlockErr := procUnlockFileEx.Call(uintptr(l.handle), 0, 1, 0, uintptr(unsafe.Pointer(&l.overlap)))
	closeErr := syscall.CloseHandle(l.handle)
	l.handle = syscall.InvalidHandle
	runtime.KeepAlive(l)
	if ok == 0 {
		return unlockErr
	}
	return closeErr
}
