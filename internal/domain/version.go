package domain

import (
	"strconv"
	"strings"
)

// CompareVersions compares dotted version strings: -1 when a < b, 0 when
// equal, +1 when a > b. It tolerates a leading "v", missing segments
// ("1.2" == "1.2.0"), and pre-release suffixes ("1.2.3-rc1" < "1.2.3").
// Both the manager (update ordering) and the agent (self-update guard)
// share this ordering.
func CompareVersions(a, b string) int {
	aCore, aPre := splitVersion(a)
	bCore, bPre := splitVersion(b)
	if len(aCore) == 0 && len(bCore) == 0 {
		return strings.Compare(aPre, bPre)
	}
	if len(aCore) == 0 {
		return -1
	}
	if len(bCore) == 0 {
		return 1
	}
	n := max(len(aCore), len(bCore))
	for i := range n {
		av, bv := 0, 0
		if i < len(aCore) {
			av = aCore[i]
		}
		if i < len(bCore) {
			bv = bCore[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	// Numeric cores equal: a release outranks any pre-release.
	if aPre == bPre {
		return 0
	}
	if aPre == "" {
		return 1
	}
	if bPre == "" {
		return -1
	}
	return strings.Compare(aPre, bPre)
}

// splitVersion splits "v1.2.3-rc1+x" into numeric core [1 2 3] and the
// pre-release/build suffix "rc1+x". Garbage segments contribute 0.
func splitVersion(v string) ([]int, string) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if v == "" {
		return nil, ""
	}
	core, suffix, _ := strings.Cut(v, "-")
	if i := strings.Index(core, "+"); i >= 0 {
		core = core[:i]
	}
	var out []int
	for seg := range strings.SplitSeq(core, ".") {
		digits := leadingDigits(seg)
		if digits == "" {
			out = append(out, 0)
			continue
		}
		num, err := strconv.Atoi(digits)
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, num)
	}
	return out, suffix
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}
