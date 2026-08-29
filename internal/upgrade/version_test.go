package upgrade

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"patch bump", "v0.6.0", "v0.6.1", true},
		{"minor bump", "0.6.9", "0.7.0", true},
		{"major bump", "v0.9.9", "v1.0.0", true},
		{"same version", "v0.6.0", "v0.6.0", false},
		{"already ahead", "v0.7.0", "v0.6.0", false},
		{"v prefix on one side only", "0.6.0", "v0.6.1", true},
		{"double digit fields sort numerically, not lexically", "v0.9.0", "v0.10.0", true},
		{"missing patch field", "v0.6", "v0.6.1", true},

		// A release beats its own pre-releases, and loses to nothing else.
		{"rc is older than the release", "v1.0.0-rc1", "v1.0.0", true},
		{"release is not older than its rc", "v1.0.0", "v1.0.0-rc1", false},

		// `make build` stamps git describe output. Such a build is *past* the
		// tag it names, so it must not be told to upgrade to that tag.
		{"git describe build is ahead of its tag", "v0.6.0-11-g660745b", "v0.6.0", false},
		{"dirty git describe build is ahead of its tag", "v0.6.0-11-g660745b-dirty", "v0.6.0", false},
		{"git describe build is behind a later tag", "v0.6.0-11-g660745b", "v0.7.0", true},

		// `go install` stamps a pseudo-version, which is a genuine pre-release
		// of a version that does not exist yet.
		{"go install pseudo-version", "v0.6.1-0.20260828080851-660745bbaa5e", "v0.6.0", false},
		{"go install pseudo-version behind a real release", "v0.6.1-0.20260828080851-660745bbaa5e", "v0.6.1", true},

		// Nothing we cannot read is ever reported as out of date.
		{"dev build", "dev", "v0.6.0", false},
		{"empty current", "", "v0.6.0", false},
		{"unparseable latest", "v0.6.0", "not-a-version", false},
		{"empty latest", "v0.6.0", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNewer(tt.current, tt.latest); got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestParseVersionRejects(t *testing.T) {
	for _, v := range []string{"", "dev", "v", "latest", "1.x.0", "v1.2.3.beta"} {
		if _, ok := parseVersion(v); ok {
			t.Errorf("parseVersion(%q) accepted a version it cannot compare", v)
		}
	}
}

func TestIsGitDescribeSuffix(t *testing.T) {
	yes := []string{"11-g660745b", "0-gabc1234", "3-g660745b-dirty"}
	no := []string{"rc1", "beta", "g660745b", "11-660745b", "11-gzzz", "0.20260828080851-660745bbaa5e"}

	for _, s := range yes {
		if !isGitDescribeSuffix(s) {
			t.Errorf("isGitDescribeSuffix(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isGitDescribeSuffix(s) {
			t.Errorf("isGitDescribeSuffix(%q) = true, want false", s)
		}
	}
}
