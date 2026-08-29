package upgrade

import (
	"strconv"
	"strings"
)

// parsed is a version broken into its comparable parts.
type parsed struct {
	nums []int  // dot-separated numeric fields, e.g. 0.6.0 -> [0 6 0]
	pre  string // pre-release suffix after the first '-', "" when none
	// ahead is true for a `git describe` version like v0.6.0-11-g660745b,
	// which means "11 commits past v0.6.0" -- newer than the tag, not older.
	// Semver would read that suffix as a pre-release and sort it *before*
	// v0.6.0, which is how a developer building from a checkout ends up being
	// told to upgrade to the release they are already ahead of.
	ahead bool
}

// parseVersion splits a version string into comparable parts. Reports false
// for anything it cannot make sense of ("dev", ""), which callers treat as
// "no opinion" rather than guessing.
func parseVersion(v string) (parsed, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return parsed{}, false
	}

	// Build metadata never affects precedence.
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}

	core, pre := v, ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core, pre = v[:i], v[i+1:]
	}

	fields := strings.Split(core, ".")
	nums := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return parsed{}, false
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return parsed{}, false
	}

	return parsed{nums: nums, pre: pre, ahead: isGitDescribeSuffix(pre)}, true
}

// isGitDescribeSuffix reports whether a pre-release suffix is what `git
// describe --tags --dirty` appends to the nearest tag: "<commits>-g<sha>",
// optionally "-dirty". The Makefile stamps builds with exactly that.
func isGitDescribeSuffix(pre string) bool {
	pre = strings.TrimSuffix(pre, "-dirty")
	i := strings.Index(pre, "-g")
	if i <= 0 {
		return false
	}
	if _, err := strconv.Atoi(pre[:i]); err != nil {
		return false
	}
	sha := pre[i+2:]
	if sha == "" {
		return false
	}
	for _, r := range sha {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// compare orders two versions: -1 if a < b, 0 if equal, +1 if a > b.
// Unparseable versions are handled by the callers, not here.
func compare(a, b parsed) int {
	n := max(len(a.nums), len(b.nums))
	for i := range n {
		x, y := 0, 0
		if i < len(a.nums) {
			x = a.nums[i]
		}
		if i < len(b.nums) {
			y = b.nums[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}

	// Same numeric core. A `git describe` build sits after its tag; a real
	// pre-release ("1.0.0-rc1") sits before it.
	rank := func(p parsed) int {
		switch {
		case p.ahead:
			return 1
		case p.pre != "":
			return -1
		default:
			return 0
		}
	}
	if ra, rb := rank(a), rank(b); ra != rb {
		if ra < rb {
			return -1
		}
		return 1
	}
	return strings.Compare(a.pre, b.pre)
}

// IsNewer reports whether latest is a strictly newer release than current.
// Returns false when either side is unparseable: a build that cannot say what
// version it is has no business claiming it is out of date.
func IsNewer(current, latest string) bool {
	c, okC := parseVersion(current)
	l, okL := parseVersion(latest)
	if !okC || !okL {
		return false
	}
	return compare(c, l) < 0
}
