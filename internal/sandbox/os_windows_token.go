//go:build windows

package sandbox

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The sandbox's teeth on Windows: a command runs under a token that is the
// user's own, minus every privilege, with the Administrators group reduced to
// deny-only, and at Low integrity. A Low process cannot write to objects at
// Medium or above (files, registry keys), which is nearly everything the user
// owns. The workspace and the private temp folder are the exceptions: they are
// labelled Low (os_windows_label.go).
//
// It does not stop reading and it does not stop network access: Low integrity
// restricts writes only. See Command.

var (
	advapi32                  = windows.NewLazySystemDLL("advapi32.dll")
	procCreateRestrictedToken = advapi32.NewProc("CreateRestrictedToken")
)

// disableMaxPrivilege is DISABLE_MAX_PRIVILEGE: drop every privilege except
// SeChangeNotifyPrivilege, which ordinary programs cannot live without.
const disableMaxPrivilege = 0x1

// createRestrictedToken wraps CreateRestrictedToken, which x/sys/windows does not.
// Only the "disable" parts are used: no restricting SIDs, which would add a
// second access check and break most programs.
func createRestrictedToken(existing windows.Token, flags uint32, disable []windows.SIDAndAttributes) (windows.Token, error) {
	var disablePtr *windows.SIDAndAttributes
	if len(disable) > 0 {
		disablePtr = &disable[0]
	}
	var out windows.Token
	r1, _, e1 := procCreateRestrictedToken.Call(uintptr(existing), uintptr(flags), uintptr(len(disable)),
		uintptr(unsafe.Pointer(disablePtr)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	if r1 == 0 {
		return 0, e1
	}
	return out, nil
}

// newSandboxToken builds the token a confined command runs under.
func newSandboxToken() (windows.Token, error) {
	var cur windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY, &cur); err != nil {
		return 0, fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer cur.Close()

	admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return 0, fmt.Errorf("CreateWellKnownSid(Administrators): %w", err)
	}
	restricted, err := createRestrictedToken(cur, disableMaxPrivilege, []windows.SIDAndAttributes{{Sid: admin}})
	if err != nil {
		return 0, fmt.Errorf("CreateRestrictedToken: %w", err)
	}
	// The handle CreateRestrictedToken returns lacks TOKEN_ADJUST_DEFAULT, so the
	// integrity level cannot be set on it; a duplicate gets full access.
	var tok windows.Token
	err = windows.DuplicateTokenEx(restricted, windows.MAXIMUM_ALLOWED, nil, windows.SecurityImpersonation, windows.TokenPrimary, &tok)
	restricted.Close()
	if err != nil {
		return 0, fmt.Errorf("DuplicateTokenEx: %w", err)
	}

	low, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err != nil {
		tok.Close()
		return 0, fmt.Errorf("CreateWellKnownSid(Low): %w", err)
	}
	label := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{Sid: low, Attributes: windows.SE_GROUP_INTEGRITY}}
	size := uint32(unsafe.Sizeof(label)) + windows.GetLengthSid(low)
	if err := windows.SetTokenInformation(tok, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), size); err != nil {
		tok.Close()
		return 0, fmt.Errorf("SetTokenInformation(integrity level): %w", err)
	}
	return tok, nil
}

// One token serves every command of the process: it is only read when a command
// starts. A failure is not remembered, so a transient one does not disable the
// sandbox until restart.
var tokenCache struct {
	sync.Mutex
	tok windows.Token
}

func sandboxToken() (windows.Token, error) {
	tokenCache.Lock()
	defer tokenCache.Unlock()
	if tokenCache.tok != 0 {
		return tokenCache.tok, nil
	}
	tok, err := newSandboxToken()
	if err != nil {
		return 0, err
	}
	tokenCache.tok = tok
	return tok, nil
}
