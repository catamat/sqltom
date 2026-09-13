package manifest

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// FileCollisionKey approximates the file-name equivalence used by common
// case-insensitive filesystems while also collapsing canonically equivalent
// Unicode spellings.
func FileCollisionKey(value string) string {
	folded := cases.Fold().String(norm.NFC.String(value))
	return norm.NFC.String(folded)
}

func isReservedWindowsName(value string) bool {
	stem := value
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	stem = strings.ToUpper(stem)
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		return stem[3] >= '1' && stem[3] <= '9'
	}
	return false
}

func makeNonReservedWindowsName(value string) string {
	if !isReservedWindowsName(value) {
		return value
	}
	if dot := strings.IndexByte(value, '.'); dot >= 0 {
		return value[:dot] + "_" + value[dot:]
	}
	return value + "_"
}
