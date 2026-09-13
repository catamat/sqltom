package dialect

import (
	"fmt"
	"strconv"
	"strings"
)

type Version struct {
	Major int
	Minor int
}

func RequireMinimumVersion(product, current string, minimum Version) error {
	parsed, err := parseVersionPrefix(current)
	if err != nil {
		return fmt.Errorf("read %s version %q: %w", product, current, err)
	}
	if parsed.Major < minimum.Major || parsed.Major == minimum.Major && parsed.Minor < minimum.Minor {
		return fmt.Errorf("%s version %q is not supported; minimum is %d.%d", product, current, minimum.Major, minimum.Minor)
	}
	return nil
}

func parseVersionPrefix(value string) (Version, error) {
	value = strings.TrimSpace(value)
	majorEnd := leadingDigits(value)
	if majorEnd == 0 || majorEnd == len(value) || value[majorEnd] != '.' {
		return Version{}, fmt.Errorf("expected a leading major.minor version")
	}
	minorStart := majorEnd + 1
	minorLength := leadingDigits(value[minorStart:])
	if minorLength == 0 {
		return Version{}, fmt.Errorf("expected a leading major.minor version")
	}
	major, err := strconv.Atoi(value[:majorEnd])
	if err != nil {
		return Version{}, fmt.Errorf("parse major version: %w", err)
	}
	minor, err := strconv.Atoi(value[minorStart : minorStart+minorLength])
	if err != nil {
		return Version{}, fmt.Errorf("parse minor version: %w", err)
	}
	return Version{Major: major, Minor: minor}, nil
}

func leadingDigits(value string) int {
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return index
		}
	}
	return len(value)
}
