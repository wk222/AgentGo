package rekit

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"unicode/utf16"
)

// Manifest is the subset of a compiled AndroidManifest.xml that RE triage needs.
type Manifest struct {
	Package      string              `json:"package"`
	VersionCode  string              `json:"version_code,omitempty"`
	VersionName  string              `json:"version_name,omitempty"`
	MinSDK       string              `json:"min_sdk,omitempty"`
	TargetSDK    string              `json:"target_sdk,omitempty"`
	Permissions  []string            `json:"permissions,omitempty"`
	AppAttrs     map[string]string   `json:"application,omitempty"`
	Components   map[string][]string `json:"components,omitempty"`
	LauncherActs []string            `json:"launcher_activities,omitempty"`
}

type axmlAttr struct{ name, value string }

func readStringPool(c []byte) ([]string, error) {
	if len(c) < 28 {
		return nil, errors.New("short string pool")
	}
	n := int(le32(c[8:]))
	flags := le32(c[16:])
	start := int(le32(c[20:]))
	utf8 := flags&(1<<8) != 0
	if 28+4*n > len(c) {
		return nil, errors.New("string pool offsets overflow")
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		p := start + int(le32(c[28+4*i:]))
		if p < 0 || p >= len(c) {
			continue
		}
		if utf8 {
			// u16len (char count) then u8/u16 byte length, then bytes.
			if c[p]&0x80 != 0 {
				p++
			}
			p++
			if p >= len(c) {
				continue
			}
			bl := int(c[p])
			if c[p]&0x80 != 0 && p+1 < len(c) {
				bl = (int(c[p]&0x7f) << 8) | int(c[p+1])
				p++
			}
			p++
			if p+bl <= len(c) {
				out[i] = string(c[p : p+bl])
			}
		} else {
			if p+2 > len(c) {
				continue
			}
			l := int(le16(c[p:]))
			p += 2
			if l&0x8000 != 0 && p+2 <= len(c) {
				l = (l&0x7fff)<<16 | int(le16(c[p:]))
				p += 2
			}
			if p+2*l <= len(c) {
				u := make([]uint16, l)
				for j := 0; j < l; j++ {
					u[j] = le16(c[p+2*j:])
				}
				out[i] = string(utf16.Decode(u))
			}
		}
	}
	return out, nil
}

func attrValue(strs []string, raw uint32, dtype byte, data uint32) string {
	str := func(i uint32) string {
		if int(i) < len(strs) {
			return strs[i]
		}
		return ""
	}
	switch dtype {
	case 0x03:
		if raw != 0xffffffff {
			return str(raw)
		}
		return str(data)
	case 0x10:
		return fmt.Sprintf("%d", int32(data))
	case 0x11:
		return fmt.Sprintf("0x%x", data)
	case 0x12:
		return fmt.Sprintf("%t", data != 0)
	case 0x01:
		return fmt.Sprintf("@0x%08x", data)
	default:
		return fmt.Sprintf("0x%x", data)
	}
}

// ParseAXML decodes a binary AndroidManifest.xml (AXML) without external tools.
func ParseAXML(b []byte) (*Manifest, error) {
	if len(b) < 8 || le16(b) != 0x0003 {
		return nil, errors.New("not binary XML (AXML)")
	}
	total := int(le32(b[4:]))
	if total > len(b) || total < 8 {
		total = len(b)
	}
	m := &Manifest{Components: map[string][]string{}, AppAttrs: map[string]string{}}
	var strs []string
	var stack []string
	var curComp string
	var hasMain, hasLauncher bool
	for off := int(le16(b[2:])); off+8 <= total; {
		typ, size := le16(b[off:]), int(le32(b[off+4:]))
		if size < 8 || off+size > total {
			break
		}
		c := b[off : off+size]
		switch typ {
		case 0x0001:
			var err error
			if strs, err = readStringPool(c); err != nil {
				return nil, err
			}
		case 0x0102:
			if len(c) < 36 {
				break
			}
			body := c[16:]
			ni := le32(body[4:])
			name := ""
			if int(ni) < len(strs) {
				name = strs[ni]
			}
			aStart, aSize, aCount := int(le16(body[8:])), int(le16(body[10:])), int(le16(body[12:]))
			attrs := make([]axmlAttr, 0, aCount)
			for i := 0; i < aCount; i++ {
				p := aStart + i*aSize
				if p+20 > len(body) {
					break
				}
				a := body[p:]
				an := le32(a[4:])
				nm := ""
				if int(an) < len(strs) {
					nm = strs[an]
				}
				attrs = append(attrs, axmlAttr{nm, attrValue(strs, le32(a[8:]), a[15], le32(a[16:]))})
			}
			get := func(k string) string {
				for _, a := range attrs {
					if a.name == k {
						return a.value
					}
				}
				return ""
			}
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			switch name {
			case "manifest":
				m.Package, m.VersionCode, m.VersionName = get("package"), get("versionCode"), get("versionName")
			case "uses-sdk":
				m.MinSDK, m.TargetSDK = get("minSdkVersion"), get("targetSdkVersion")
			case "uses-permission", "uses-permission-sdk-23":
				if v := get("name"); v != "" {
					m.Permissions = append(m.Permissions, v)
				}
			case "application":
				for _, a := range attrs {
					switch a.name {
					case "name", "debuggable", "allowBackup", "usesCleartextTraffic", "extractNativeLibs", "networkSecurityConfig", "appComponentFactory":
						m.AppAttrs[a.name] = a.value
					}
				}
			case "activity", "activity-alias", "service", "receiver", "provider":
				if parent == "application" {
					curComp, hasMain, hasLauncher = get("name"), false, false
					m.Components[name] = append(m.Components[name], curComp)
				}
			case "action":
				if get("name") == "android.intent.action.MAIN" {
					hasMain = true
				}
			case "category":
				if get("name") == "android.intent.category.LAUNCHER" {
					hasLauncher = true
				}
			}
			stack = append(stack, name)
		case 0x0103:
			if n := len(stack); n > 0 {
				if stack[n-1] == "intent-filter" && hasMain && hasLauncher && curComp != "" {
					m.LauncherActs = append(m.LauncherActs, curComp)
					hasMain, hasLauncher = false, false
				}
				stack = stack[:n-1]
			}
		}
		off += size
	}
	sort.Strings(m.Permissions)
	m.Permissions = slices.Compact(m.Permissions) // merged SDK manifests repeat permissions
	for k, v := range m.Components {
		m.Components[k] = slices.Compact(v)
	}
	return m, nil
}
