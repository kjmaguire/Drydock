// Package winident is drydock.exe's Windows identity (docs/windows-identity.md): the icon, the version resource and the
// application manifest that build/windows/winres.json defines and go-winres embeds, and the check that a built executable
// carries all three. CI runs the check on the real drydock.exe (cmd/drydock-winident); the tests here run it on Linux against
// the committed definition and against a small Windows executable they build and patch.
package winident

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

// IconResourceID is the icon group Wails gives every window (application.NewIconFromResource(module, 3) in
// webview_window_windows.go of Wails v3.0.0-beta.34) and that app/icon_windows.go loads at the window's DPI. It is also the
// executable's only icon group, so Explorer, the Start menu, shortcuts and the taskbar of a pinned app show it.
const IconResourceID = 3

// IconSizes are the sizes the icon carries: the shell's small and large icons at 100 to 250 percent scale, the extra-large
// views and the 256 pixel one (stored as PNG).
var IconSizes = []int{16, 20, 24, 32, 40, 48, 64, 96, 128, 256}

// DefinitionDir is where the definition lives, relative to the repository root.
const DefinitionDir = "build/windows"

// Win10SupportedOS is the compatibility ID of Windows 10, which Windows 11 shares.
const Win10SupportedOS = "{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"

// The version resource's text the definition must carry (the version strings come from the build).
var wantStrings = map[string]string{
	version.FileDescription:  "Drydock",
	version.ProductName:      "Drydock",
	version.InternalName:     "drydock",
	version.OriginalFilename: "drydock.exe",
}

// Definition is build/windows/winres.json read back: where the icon and the manifest are and what the version resource says.
type Definition struct {
	Dir          string
	IconPath     string
	ManifestPath string
	Version      version.Info
}

// LoadDefinition reads <root>/build/windows/winres.json. It accepts only what Drydock uses: one icon group with ID 3, the
// manifest as a file with ID 1 and one version resource with ID 1.
func LoadDefinition(root string) (*Definition, error) {
	dir := filepath.Join(root, filepath.FromSlash(DefinitionDir))
	b, err := os.ReadFile(filepath.Join(dir, "winres.json"))
	if err != nil {
		return nil, err
	}
	var raw map[string]map[string]map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("winres.json: %w", err)
	}
	d := &Definition{Dir: dir}
	one := func(typ, id string) (json.RawMessage, error) {
		res := raw[typ]
		if len(res) != 1 || res[id] == nil || len(res[id]) != 1 {
			return nil, fmt.Errorf("winres.json: %s must hold exactly one resource, %s, in one language", typ, id)
		}
		for _, v := range res[id] {
			return v, nil
		}
		return nil, nil
	}
	file := func(typ, id string) (string, error) {
		v, err := one(typ, id)
		if err != nil {
			return "", err
		}
		var name string
		if err := json.Unmarshal(v, &name); err != nil || name == "" {
			return "", fmt.Errorf("winres.json: %s %s must name a file", typ, id)
		}
		return filepath.Join(dir, filepath.FromSlash(name)), nil
	}
	if d.IconPath, err = file("RT_GROUP_ICON", fmt.Sprintf("#%d", IconResourceID)); err != nil {
		return nil, err
	}
	if d.ManifestPath, err = file("RT_MANIFEST", "#1"); err != nil {
		return nil, err
	}
	v, err := one("RT_VERSION", "#1")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(v, &d.Version); err != nil {
		return nil, fmt.Errorf("winres.json: RT_VERSION: %w", err)
	}
	for k := range raw {
		if k != "RT_GROUP_ICON" && k != "RT_MANIFEST" && k != "RT_VERSION" {
			return nil, fmt.Errorf("winres.json: unexpected resource type %s", k)
		}
	}
	return d, nil
}

// DefaultVersion is the version the definition carries for a build that is given none (ProductVersion).
func (d *Definition) DefaultVersion() string {
	return d.Version.Table().GetMainTranslation()[version.ProductVersion]
}

// Resources builds the resource set the definition describes, as go-winres does from the same file, with the build's version
// when one is given. The tests use it to patch an executable; the build itself runs go-winres.
func (d *Definition) Resources(buildVersion string) (*winres.ResourceSet, error) {
	rs := &winres.ResourceSet{}
	f, err := os.Open(d.IconPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	icon, err := winres.LoadICO(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", d.IconPath, err)
	}
	if err := rs.SetIconTranslation(winres.ID(IconResourceID), 0, icon); err != nil {
		return nil, err
	}
	m, err := os.ReadFile(d.ManifestPath)
	if err != nil {
		return nil, err
	}
	if err := rs.Set(winres.RT_MANIFEST, winres.ID(1), 0x0409, m); err != nil {
		return nil, err
	}
	// a copy: the version's string tables are maps, and the definition stays as the file says
	var vi version.Info
	j, err := json.Marshal(&d.Version)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(j, &vi); err != nil {
		return nil, err
	}
	if buildVersion != "" {
		vi.SetFileVersion(buildVersion)
		vi.SetProductVersion(buildVersion)
	}
	rs.SetVersionInfo(vi)
	return rs, nil
}

// IconEntry is one image of an icon file or icon group.
type IconEntry struct {
	Size     int // pixels, width and height
	BitCount int
	PNG      bool
}

// ParseICO lists the images of an .ico file.
func ParseICO(b []byte) ([]IconEntry, error) {
	if len(b) < 6 || binary.LittleEndian.Uint16(b[0:]) != 0 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		return nil, errors.New("not an icon file")
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	if len(b) < 6+16*n {
		return nil, errors.New("icon directory cut off")
	}
	var out []IconEntry
	for i := range n {
		e := b[6+16*i:]
		size, off := binary.LittleEndian.Uint32(e[8:]), binary.LittleEndian.Uint32(e[12:])
		if uint64(off)+uint64(size) > uint64(len(b)) {
			return nil, fmt.Errorf("image %d lies outside the file", i)
		}
		ie, err := iconImage(e[0], e[1], b[off:off+size])
		if err != nil {
			return nil, fmt.Errorf("image %d: %w", i, err)
		}
		out = append(out, ie)
	}
	return out, nil
}

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// iconImage reads one image: a PNG, or a BMP (BITMAPINFOHEADER with twice the height, then the pixels and the AND mask).
func iconImage(w, h byte, data []byte) (IconEntry, error) {
	size := int(w)
	if size == 0 {
		size = 256
	}
	if int(h) != int(w) {
		return IconEntry{}, errors.New("not square")
	}
	if bytes.HasPrefix(data, pngMagic) {
		if len(data) < 24 || !bytes.Equal(data[12:16], []byte("IHDR")) {
			return IconEntry{}, errors.New("PNG without a header")
		}
		pw, ph := binary.BigEndian.Uint32(data[16:]), binary.BigEndian.Uint32(data[20:])
		if int(pw) != size || int(ph) != size {
			return IconEntry{}, fmt.Errorf("PNG is %dx%d, the directory says %d", pw, ph, size)
		}
		return IconEntry{Size: size, BitCount: 32, PNG: true}, nil
	}
	if len(data) < 40 || binary.LittleEndian.Uint32(data) != 40 {
		return IconEntry{}, errors.New("neither PNG nor BMP")
	}
	bw, bh := int32(binary.LittleEndian.Uint32(data[4:])), int32(binary.LittleEndian.Uint32(data[8:]))
	bits := int(binary.LittleEndian.Uint16(data[14:]))
	if int(bw) != size || int(bh) != 2*size {
		return IconEntry{}, fmt.Errorf("BMP is %dx%d, the directory says %d", bw, bh/2, size)
	}
	maskStride := (size + 31) / 32 * 4
	if want := 40 + size*size*bits/8 + maskStride*size; bits == 32 && len(data) < want {
		return IconEntry{}, fmt.Errorf("BMP has %d bytes, %d expected", len(data), want)
	}
	return IconEntry{Size: size, BitCount: bits}, nil
}

// CheckIconEntries says what is wrong with an icon's images: every size of IconSizes once, 32-bit, the 256 one as PNG and the
// others as BMP (what every Windows component reads).
func CheckIconEntries(entries []IconEntry) []string {
	var problems []string
	var sizes []int
	for _, e := range entries {
		sizes = append(sizes, e.Size)
		if e.BitCount != 32 {
			problems = append(problems, fmt.Sprintf("the %d pixel image is %d-bit, not 32-bit", e.Size, e.BitCount))
		}
		if e.Size >= 256 && !e.PNG {
			problems = append(problems, "the 256 pixel image is not PNG")
		}
		if e.Size < 256 && e.PNG {
			problems = append(problems, fmt.Sprintf("the %d pixel image is PNG, not BMP", e.Size))
		}
	}
	slices.Sort(sizes)
	if !slices.Equal(sizes, IconSizes) {
		problems = append(problems, fmt.Sprintf("the icon has the sizes %v, not %v", sizes, IconSizes))
	}
	return problems
}

// manifestXML is the part of an application manifest the check reads.
type manifestXML struct {
	XMLName    xml.Name `xml:"assembly"`
	TrustLevel []struct {
		Level    string `xml:"level,attr"`
		UIAccess string `xml:"uiAccess,attr"`
	} `xml:"trustInfo>security>requestedPrivileges>requestedExecutionLevel"`
	SupportedOS []struct {
		ID string `xml:"Id,attr"`
	} `xml:"compatibility>application>supportedOS"`
	Settings struct {
		DPIAware       string `xml:"dpiAware"`
		DPIAwareness   string `xml:"dpiAwareness"`
		LongPathAware  string `xml:"longPathAware"`
		ActiveCodePage string `xml:"activeCodePage"`
	} `xml:"application>windowsSettings"`
	Dependencies []struct {
		Name    string `xml:"name,attr"`
		Version string `xml:"version,attr"`
		Token   string `xml:"publicKeyToken,attr"`
	} `xml:"dependency>dependentAssembly>assemblyIdentity"`
}

// CheckManifest says what is wrong with an application manifest: Drydock's runs as the invoker, is per-monitor (v2) DPI
// aware, declares Windows 10 and 11, uses the version 6 common controls, is long-path aware and runs with UTF-8 as its code
// page.
func CheckManifest(b []byte) []string {
	var m manifestXML
	if err := xml.Unmarshal(b, &m); err != nil {
		return []string{"the manifest is not XML: " + err.Error()}
	}
	var problems []string
	if len(m.TrustLevel) != 1 || m.TrustLevel[0].Level != "asInvoker" || m.TrustLevel[0].UIAccess != "false" {
		problems = append(problems, "the manifest does not ask for asInvoker without uiAccess")
	}
	if !slices.ContainsFunc(m.SupportedOS, func(o struct {
		ID string `xml:"Id,attr"`
	}) bool {
		return strings.EqualFold(o.ID, Win10SupportedOS)
	}) {
		problems = append(problems, "the manifest does not declare Windows 10 and 11 (supportedOS "+Win10SupportedOS+")")
	}
	if aw := strings.Fields(strings.ReplaceAll(strings.ToLower(m.Settings.DPIAwareness), ",", " ")); len(aw) == 0 || aw[0] != "permonitorv2" {
		problems = append(problems, "the manifest's dpiAwareness does not start with PerMonitorV2")
	}
	if strings.TrimSpace(strings.ToLower(m.Settings.DPIAware)) != "true/pm" {
		problems = append(problems, "the manifest's dpiAware fallback is not true/pm")
	}
	if strings.TrimSpace(m.Settings.LongPathAware) != "true" {
		problems = append(problems, "the manifest is not longPathAware")
	}
	if !strings.EqualFold(strings.TrimSpace(m.Settings.ActiveCodePage), "UTF-8") {
		problems = append(problems, "the manifest's activeCodePage is not UTF-8")
	}
	if !slices.ContainsFunc(m.Dependencies, func(d struct {
		Name    string `xml:"name,attr"`
		Version string `xml:"version,attr"`
		Token   string `xml:"publicKeyToken,attr"`
	}) bool {
		return d.Name == "Microsoft.Windows.Common-Controls" && d.Version == "6.0.0.0" && d.Token == "6595b64144ccf1df"
	}) {
		problems = append(problems, "the manifest does not depend on Common Controls 6.0.0.0")
	}
	return problems
}

// NumericVersion is the four-part version Windows shows for a version string: 1.2.3-beta.4 is 1.2.3.0, as go-winres reads it.
func NumericVersion(v string) [4]uint16 {
	var vi version.Info
	vi.SetFileVersion(v)
	return vi.FileVersion
}

// CheckVersionInfo says what is wrong with a version resource: the names Drydock's carries, a copyright and a company, and
// the given version both as text and as numbers.
func CheckVersionInfo(vi *version.Info, want string) []string {
	var problems []string
	strs := vi.Table().GetMainTranslation()
	for k, v := range wantStrings {
		if strs[k] != v {
			problems = append(problems, fmt.Sprintf("the version resource's %s is %q, not %q", k, strs[k], v))
		}
	}
	for _, k := range []string{version.CompanyName, version.LegalCopyright} {
		if strings.TrimSpace(strs[k]) == "" {
			problems = append(problems, "the version resource has no "+k)
		}
	}
	for _, k := range []string{version.FileVersion, version.ProductVersion} {
		if strs[k] != want {
			problems = append(problems, fmt.Sprintf("the version resource's %s is %q, not %q", k, strs[k], want))
		}
	}
	n := NumericVersion(want)
	if vi.FileVersion != n || vi.ProductVersion != n {
		problems = append(problems, fmt.Sprintf("the version resource's numbers are %v and %v, not %v", vi.FileVersion, vi.ProductVersion, n))
	}
	return problems
}

// Report is what CheckEXE found.
type Report struct {
	IconGroup string      // the first icon group's ID
	Icon      []IconEntry // its images
	Strings   map[string]string
	Fixed     [4]uint16
	Manifest  bool
	Problems  []string
}

// CheckEXE reads an executable's resources and says what is missing or wrong: its first icon group must be IconResourceID with
// every size, its version resource must carry want, and its manifest must pass CheckManifest.
func CheckEXE(exe io.ReadSeeker, want string) (*Report, error) {
	rs, err := winres.LoadFromEXE(exe)
	switch {
	case errors.Is(err, winres.ErrNoResources):
		rs = &winres.ResourceSet{} // no .rsrc section at all: every check below says what is missing
	case err != nil:
		return nil, err
	}
	r := &Report{}
	// the first icon group in the order Windows stores them (names, then IDs ascending) is the one Explorer shows
	var group []byte
	rs.WalkType(winres.RT_GROUP_ICON, func(id winres.Identifier, _ uint16, data []byte) bool {
		r.IconGroup = fmt.Sprint(id)
		if id == winres.ID(IconResourceID) {
			group = data
		}
		return false
	})
	switch {
	case r.IconGroup == "":
		r.Problems = append(r.Problems, "the executable has no icon")
	case group == nil:
		r.Problems = append(r.Problems, fmt.Sprintf("the first icon group is %s, not %d (the one Wails and Explorer use)", r.IconGroup, IconResourceID))
	default:
		entries, err := groupEntries(rs, group)
		if err != nil {
			r.Problems = append(r.Problems, "the icon group: "+err.Error())
		} else {
			r.Icon = entries
			r.Problems = append(r.Problems, CheckIconEntries(entries)...)
		}
	}
	var vdata []byte
	rs.WalkType(winres.RT_VERSION, func(_ winres.Identifier, _ uint16, data []byte) bool {
		vdata = data
		return false
	})
	if vdata == nil {
		r.Problems = append(r.Problems, "the executable has no version resource")
	} else if vi, err := version.FromBytes(vdata); err != nil {
		r.Problems = append(r.Problems, "the version resource cannot be read: "+err.Error())
	} else {
		r.Strings = map[string]string(vi.Table().GetMainTranslation())
		r.Fixed = vi.FileVersion
		r.Problems = append(r.Problems, CheckVersionInfo(vi, want)...)
	}
	var mdata []byte
	rs.WalkType(winres.RT_MANIFEST, func(id winres.Identifier, _ uint16, data []byte) bool {
		if id == winres.ID(1) {
			mdata = data
		}
		return mdata == nil
	})
	if mdata == nil {
		r.Problems = append(r.Problems, "the executable has no application manifest (RT_MANIFEST 1)")
	} else {
		r.Manifest = true
		r.Problems = append(r.Problems, CheckManifest(mdata)...)
	}
	return r, nil
}

// groupEntries reads an RT_GROUP_ICON directory (an icon file's header whose entries name RT_ICON resources instead of
// offsets) and the images it names.
func groupEntries(rs *winres.ResourceSet, group []byte) ([]IconEntry, error) {
	if len(group) < 6 || binary.LittleEndian.Uint16(group[2:]) != 1 {
		return nil, errors.New("not an icon directory")
	}
	n := int(binary.LittleEndian.Uint16(group[4:]))
	if len(group) < 6+14*n {
		return nil, errors.New("directory cut off")
	}
	var out []IconEntry
	for i := range n {
		e := group[6+14*i:]
		id := binary.LittleEndian.Uint16(e[12:])
		var data []byte
		rs.WalkType(winres.RT_ICON, func(rid winres.Identifier, _ uint16, d []byte) bool {
			if rid == winres.ID(id) {
				data = d
			}
			return data == nil
		})
		if data == nil {
			return nil, fmt.Errorf("image %d names icon %d, which is not there", i, id)
		}
		ie, err := iconImage(e[0], e[1], data)
		if err != nil {
			return nil, fmt.Errorf("icon %d: %w", id, err)
		}
		out = append(out, ie)
	}
	return out, nil
}
