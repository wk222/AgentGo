package rekit

import (
	"archive/zip"
	"bytes"
	"io"
	"path"
	"sort"
	"strings"
)

// APKInfo is a static, no-execution summary of an APK/XAPK/zip container.
type APKInfo struct {
	Container      string              `json:"container"`
	Entries        int                 `json:"entries"`
	UncompressedMB float64             `json:"uncompressed_mb"`
	Manifest       *Manifest           `json:"manifest,omitempty"`
	ManifestErr    string              `json:"manifest_error,omitempty"`
	DexFiles       []string            `json:"dex_files,omitempty"`
	NativeLibs     map[string][]string `json:"native_libs_by_abi,omitempty"`
	Engines        []string            `json:"engines,omitempty"`
	Packers        []string            `json:"packers,omitempty"`
	Signature      []string            `json:"signature_files,omitempty"`
	TopDirs        map[string]int      `json:"top_dirs,omitempty"`
	Largest        []EntryBrief        `json:"largest_entries,omitempty"`
	MaskedManaged  []XORMatch2         `json:"masked_managed_assemblies,omitempty"`
	InnerAPKs      []string            `json:"inner_apks,omitempty"`
	Inner          map[string]*APKInfo `json:"inner_apk_details,omitempty"`
}

// EntryBrief is a name/size pair.
type EntryBrief struct {
	Name string  `json:"name"`
	MB   float64 `json:"mb"`
}

// XORMatch2 ties a zip entry to its single-byte XOR hypotheses.
type XORMatch2 struct {
	Entry string     `json:"entry"`
	Head  string     `json:"head_hex"`
	Hits  []XORMatch `json:"hypotheses"`
}

var packerMarkers = map[string]string{
	"libdexhelper": "Bangcle/梆梆 (libDexHelper)", "libsecexe": "Bangcle/梆梆 (libsecexe)", "libsecmain": "Bangcle/梆梆 (libsecmain)",
	"libjiagu": "360加固", "libshella": "腾讯乐固 (libshella)", "libtprt": "腾讯乐固 (libtprt)",
	"libbaiduprotect": "百度加固", "libsagittarius": "百度加固 (sagittarius)",
	"libexec.so": "爱加密 (libexec)", "ijiami": "爱加密 (ijiami)", "libnesec": "网易易盾 (libnesec)",
	"libdexjni": "VMP dex-jni (libdexjni)", "libmobisec": "阿里聚安全 (libmobisec)", "libapp-protect": "通付盾/其他 (libapp-protect)",
}

// InspectAPK reads the central directory (cheap even for multi-GB archives) and the few
// entries that carry decisive facts (manifest, managed assemblies' first bytes).
func InspectAPK(file string, listLimit int) (*APKInfo, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	info, err := inspectZip(&zr.Reader, listLimit)
	if err != nil {
		return nil, err
	}
	// XAPK/APKS bundles: look inside each inner apk (one level, <=256 MiB each, in memory).
	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".apk") || strings.Contains(f.Name, "/") || f.UncompressedSize64 > 256<<20 {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		if izr, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil {
			if sub, err := inspectZip(izr, listLimit); err == nil {
				if info.Inner == nil {
					info.Inner = map[string]*APKInfo{}
				}
				info.Inner[f.Name] = sub
			}
		}
	}
	return info, nil
}

func inspectZip(zr *zip.Reader, listLimit int) (*APKInfo, error) {
	info := &APKInfo{Container: "zip", NativeLibs: map[string][]string{}, TopDirs: map[string]int{}}
	var total uint64
	var sizes []EntryBrief
	engines, packers := map[string]bool{}, map[string]bool{}
	var manifest *zip.File
	for _, f := range zr.File {
		info.Entries++
		total += f.UncompressedSize64
		name := f.Name
		low := strings.ToLower(name)
		if i := strings.IndexByte(name, '/'); i > 0 {
			info.TopDirs[name[:i]]++
		} else {
			info.TopDirs["(root)"]++
		}
		sizes = append(sizes, EntryBrief{name, float64(f.UncompressedSize64) / (1 << 20)})
		base := path.Base(low)
		switch {
		case name == "AndroidManifest.xml":
			manifest = f
		case strings.HasSuffix(low, ".dex") && !strings.Contains(name, "/"):
			info.DexFiles = append(info.DexFiles, name)
		case strings.HasPrefix(name, "lib/") && strings.HasSuffix(low, ".so"):
			parts := strings.Split(name, "/")
			if len(parts) >= 3 {
				info.NativeLibs[parts[1]] = append(info.NativeLibs[parts[1]], parts[len(parts)-1])
			}
		case strings.HasPrefix(low, "meta-inf/") && (strings.HasSuffix(low, ".rsa") || strings.HasSuffix(low, ".dsa") || strings.HasSuffix(low, ".ec") || strings.HasSuffix(low, ".sf") || strings.HasSuffix(low, "cert.sf")):
			info.Signature = append(info.Signature, name)
		case strings.HasSuffix(low, ".apk") && !strings.Contains(name, "/"):
			info.InnerAPKs = append(info.InnerAPKs, name)
		}
		switch {
		case base == "libunity.so" || strings.HasPrefix(low, "assets/bin/data/"):
			engines["Unity"] = true
		}
		switch base {
		case "libil2cpp.so", "global-metadata.dat":
			engines["Unity IL2CPP"] = true
		case "libmono.so", "libmonobdwgc-2.0.so":
			engines["Unity Mono"] = true
		case "libflutter.so":
			engines["Flutter"] = true
		case "libreactnativejni.so", "index.android.bundle":
			engines["React Native"] = true
		case "libcocos2djs.so", "libcocos2dlua.so", "libcocos2dcpp.so", "libcocos.so":
			engines["Cocos2d-x"] = true
		case "libgodot_android.so":
			engines["Godot"] = true
		case "libue4.so":
			engines["Unreal"] = true
		case "libluajit.so", "liblua.so", "libxlua.so", "libtolua.so":
			engines["Lua runtime ("+base+")"] = true
		}
		if strings.HasSuffix(low, ".lua") || strings.HasSuffix(low, ".luac") {
			engines["Lua scripts"] = true
		}
		for k, v := range packerMarkers {
			if strings.Contains(base, k) {
				packers[v] = true
			}
		}
		if strings.Contains(low, "/managed/") && strings.HasSuffix(low, ".dll") && f.UncompressedSize64 >= 64 {
			if rc, err := f.Open(); err == nil {
				head := make([]byte, 512)
				n, _ := io.ReadFull(rc, head)
				rc.Close()
				head = head[:n]
				if !bytes.HasPrefix(head, []byte("MZ")) {
					if hits := ProbeSingleByteXOR(head); len(hits) > 0 {
						info.MaskedManaged = append(info.MaskedManaged, XORMatch2{name, hexPreview(head, 8), hits})
					}
				}
			}
		}
	}
	info.UncompressedMB = round2(float64(total) / (1 << 20))
	sort.Slice(sizes, func(i, j int) bool { return sizes[i].MB > sizes[j].MB })
	if len(sizes) > 8 {
		sizes = sizes[:8]
	}
	for i := range sizes {
		sizes[i].MB = round2(sizes[i].MB)
	}
	info.Largest = sizes
	for k := range engines {
		info.Engines = append(info.Engines, k)
	}
	for k := range packers {
		info.Packers = append(info.Packers, k)
	}
	sort.Strings(info.Engines)
	sort.Strings(info.Packers)
	sort.Strings(info.DexFiles)
	for abi := range info.NativeLibs {
		sort.Strings(info.NativeLibs[abi])
		if listLimit > 0 && len(info.NativeLibs[abi]) > listLimit {
			info.NativeLibs[abi] = info.NativeLibs[abi][:listLimit]
		}
	}
	if len(info.NativeLibs) == 0 {
		info.NativeLibs = nil
	}

	switch {
	case manifest != nil && len(info.DexFiles) > 0:
		info.Container = "apk"
	case manifest != nil:
		info.Container = "apk-or-aab-resources"
	case len(info.InnerAPKs) > 0:
		info.Container = "xapk-or-apks-bundle"
	case info.TopDirs["Payload"] > 0:
		info.Container = "ipa"
	case info.TopDirs["META-INF"] > 0 && info.TopDirs["(root)"] == 0:
		info.Container = "jar"
	}
	if manifest != nil {
		if rc, err := manifest.Open(); err == nil {
			data, _ := io.ReadAll(io.LimitReader(rc, 8<<20))
			rc.Close()
			if m, err := ParseAXML(data); err == nil {
				info.Manifest = m
			} else {
				info.ManifestErr = err.Error()
			}
		}
	}
	return info, nil
}

// Summary is a compact map for embedding in a Triage.
func (a *APKInfo) Summary() map[string]any {
	m := map[string]any{"container": a.Container, "entries": a.Entries, "uncompressed_mb": a.UncompressedMB}
	if a.Manifest != nil {
		m["package"], m["version_code"], m["version_name"] = a.Manifest.Package, a.Manifest.VersionCode, a.Manifest.VersionName
	}
	if len(a.Engines) > 0 {
		m["engines"] = a.Engines
	}
	if len(a.Packers) > 0 {
		m["packers"] = a.Packers
	}
	if len(a.MaskedManaged) > 0 {
		m["masked_managed_assemblies"] = len(a.MaskedManaged)
	}
	abis := make([]string, 0, len(a.NativeLibs))
	for k := range a.NativeLibs {
		abis = append(abis, k)
	}
	sort.Strings(abis)
	if len(abis) > 0 {
		m["abis"] = abis
	}
	return m
}
