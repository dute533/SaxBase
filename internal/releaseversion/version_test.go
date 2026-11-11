package releaseversion

import (
	"strings"
	"testing"
)

func TestValidateSemVer(t *testing.T) {
	for _, value := range []string{"0.0.0", "1.2.3", "1.2.3-rc.1", "1.2.3+build.05", "1.2.3-alpha.1+linux-amd64", strings.Repeat("1", MaxLength-4) + ".0.0"} {
		if err := Validate(value); err != nil {
			t.Errorf("rejected %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "1", "1.2", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "1.2.3-", "1.2.3+", "1.2.3-alpha..1", "1.2.3+one+two", "1.2.3_build", strings.Repeat("1", MaxLength-3) + ".0.0"} {
		if err := Validate(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}
