//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// A Low-integrity process may only write where the object's label says Low.
// ensureLowLabel puts that label on a directory, once: the label is inherited by
// everything created inside later, and Windows pushes it onto what is already
// there in the same call (measured: 3050 files and folders in about 0.6 s).
//
// Labelling changes the directory permanently; nothing in AgentGo takes the
// label off again. Undo by hand with:  icacls <folder> /setintegritylevel M /T
// Because the label stays, a command in one workspace can still write into
// another workspace that was confined before.

// lowLabelSDDL is a mandatory label ACE: Low, inherited by files and folders,
// "no write up" (the default policy).
const lowLabelSDDL = "S:(ML;OICI;NW;;;LW)"

// maxLabelObjects bounds how many files and folders a first-time label may
// touch. A var only so tests can lower it.
var maxLabelObjects = 400000

var errTooManyObjects = errors.New("too many files and folders")

// hasLowLabel reports whether dir already carries a Low label (own or inherited).
func hasLowLabel(dir string) bool {
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	s := sd.String()
	return strings.Contains(s, "(ML;") && strings.Contains(s, ";;;LW)")
}

// ensureLowLabel makes dir writable for the sandboxed token. Every doubt is an
// error: the caller then refuses to run the command rather than run it
// unconfined.
func ensureLowLabel(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("sandbox: write root %q is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	if why := tooWide(dir); why != "" {
		return fmt.Errorf("sandbox: will not open %s for writing: %s", dir, why)
	}
	st, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("sandbox: write root %s: %w", dir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("sandbox: write root %s is not a folder", dir)
	}
	if hasLowLabel(dir) {
		return nil
	}
	if err := checkLocalNTFS(dir); err != nil {
		return fmt.Errorf("sandbox: write root %s: %w", dir, err)
	}
	if err := countWithin(dir, maxLabelObjects); err != nil {
		if errors.Is(err, errTooManyObjects) {
			return fmt.Errorf("sandbox: %s holds more than %d files and folders; marking them all would take too long. Use a smaller workspace folder", dir, maxLabelObjects)
		}
		return fmt.Errorf("sandbox: write root %s: %w", dir, err)
	}
	sd, err := windows.SecurityDescriptorFromString(lowLabelSDDL)
	if err != nil {
		return fmt.Errorf("sandbox: label descriptor: %w", err)
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		return fmt.Errorf("sandbox: label descriptor: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("sandbox: cannot mark %s as writable for confined commands (%w). Windows requires the \"take ownership\" permission on that folder and what is in it; folders under your user profile have it. To grant it, run: icacls \"%s\" /grant \"%%USERNAME%%:(OI)(CI)F\" /T", dir, err, dir)
		}
		return fmt.Errorf("sandbox: cannot mark %s as writable for confined commands: %w", dir, err)
	}
	if !hasLowLabel(dir) {
		return fmt.Errorf("sandbox: marking %s as writable for confined commands had no effect", dir)
	}
	return nil
}

// tooWide names the reason dir must never be opened for sandboxed writes, or "".
// The label is permanent and covers the whole tree, so the places where that
// would be a disaster are refused outright.
func tooWide(dir string) string {
	if vol := filepath.VolumeName(dir); strings.EqualFold(dir, vol) || strings.EqualFold(dir, vol+`\`) {
		return "it is the root of a drive"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && within(home, dir) {
		return "it is your user profile folder or contains it"
	}
	for _, env := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
		if sys := os.Getenv(env); sys != "" && within(dir, sys) {
			return "it is inside a Windows system folder"
		}
	}
	return ""
}

// within reports whether path is root itself or lies under it (case-insensitive,
// as NTFS paths are).
func within(path, root string) bool {
	p, r := strings.ToLower(filepath.Clean(path)), strings.TrimRight(strings.ToLower(filepath.Clean(root)), `\`)
	return p == r || strings.HasPrefix(p, r+`\`)
}

// checkLocalNTFS refuses network shares, optical drives, and volumes that cannot
// hold ACLs (FAT, exFAT): labels need an ACL-capable local disk.
func checkLocalNTFS(dir string) error {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	var vol [windows.MAX_PATH + 1]uint16
	if err := windows.GetVolumePathName(p, &vol[0], uint32(len(vol))); err != nil {
		return fmt.Errorf("cannot tell which volume it is on: %w", err)
	}
	switch windows.GetDriveType(&vol[0]) {
	case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE:
	default:
		return errors.New("it is not on a local disk")
	}
	var flags uint32
	if err := windows.GetVolumeInformation(&vol[0], nil, 0, nil, nil, &flags, nil, 0); err != nil {
		return fmt.Errorf("cannot read the volume's properties: %w", err)
	}
	if flags&windows.FILE_PERSISTENT_ACLS == 0 {
		return errors.New("its disk does not support permissions (it is not NTFS)")
	}
	return nil
}

// countWithin walks dir and fails with errTooManyObjects past limit entries.
// Folders that cannot be listed are skipped: whatever is unreachable now is
// unreachable to the label as well.
func countWithin(dir string, limit int) error {
	n := 0
	return filepath.WalkDir(dir, func(_ string, _ fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		n++
		if n > limit {
			return errTooManyObjects
		}
		return nil
	})
}
