package manifests

import (
	"regexp"
	"strconv"
)

// Version is a semantic version (major.minor.patch). Pre-release and build suffixes are ignored.
type Version [3]int

var versionRe = regexp.MustCompile(`(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

// ParseVersion extracts the first version number from s. "22" parses as 22.0.0 and "v22.22.0" as 22.22.0.
func ParseVersion(s string) (Version, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	var v Version
	for i := 0; i < 3; i++ {
		if m[i+1] != "" {
			v[i], _ = strconv.Atoi(m[i+1])
		}
	}
	return v, true
}

// Compare returns -1, 0 or 1.
func (v Version) Compare(o Version) int {
	for i := 0; i < 3; i++ {
		switch {
		case v[i] < o[i]:
			return -1
		case v[i] > o[i]:
			return 1
		}
	}
	return 0
}

// Less reports v < o.
func (v Version) Less(o Version) bool { return v.Compare(o) < 0 }

// String renders x.y.z.
func (v Version) String() string {
	return strconv.Itoa(v[0]) + "." + strconv.Itoa(v[1]) + "." + strconv.Itoa(v[2])
}
