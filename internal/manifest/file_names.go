package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	maxPortableComponentBytes = 255
	generatedGoSuffixBytes    = len(".go")
	maxGoFileStemBytes        = maxPortableComponentBytes - generatedGoSuffixBytes
	stableHashHexBytes        = 16
)

var (
	knownGOOS = map[string]struct{}{
		"aix": {}, "android": {}, "darwin": {}, "dragonfly": {},
		"freebsd": {}, "hurd": {}, "illumos": {}, "ios": {}, "js": {},
		"linux": {}, "nacl": {}, "netbsd": {}, "openbsd": {}, "plan9": {},
		"solaris": {}, "wasip1": {}, "windows": {}, "zos": {},
	}
	knownGOARCH = map[string]struct{}{
		"386": {}, "amd64": {}, "amd64p32": {}, "arm": {}, "armbe": {},
		"arm64": {}, "arm64be": {}, "loong64": {}, "mips": {}, "mipsle": {},
		"mips64": {}, "mips64le": {}, "mips64p32": {}, "mips64p32le": {},
		"ppc": {}, "ppc64": {}, "ppc64le": {}, "riscv": {}, "riscv64": {},
		"s390": {}, "s390x": {}, "sparc": {}, "sparc64": {}, "wasm": {},
	}
)

func sanitizeGoFileComponent(value string) (string, error) {
	var output strings.Builder
	previousDot := false
	for _, r := range norm.NFC.String(value) {
		if !isGoImportPathRune(r) {
			continue
		}
		if r == '.' && (output.Len() == 0 || previousDot) {
			continue
		}
		output.WriteRune(r)
		previousDot = r == '.'
	}

	result := strings.TrimLeft(output.String(), "._-")
	result = strings.TrimRight(result, ".")
	if result == "" {
		return "", fmt.Errorf("cannot derive a Go output name from %q", value)
	}
	result = makeNonReservedWindowsName(result)
	result = makeNonWindowsShortName(result)
	if isGoSpecialDirectory(result) {
		result += "_"
	}
	for isIgnoredOrConditionalGoSource(result) {
		result = neutralizeGoSourceName(result)
	}
	if len(result) > maxGoFileStemBytes {
		result = truncateASCIIWithHash(result, value, maxGoFileStemBytes)
	}
	if err := validateGoFileComponent(result); err != nil {
		return "", fmt.Errorf("cannot derive a Go output name from %q: %w", value, err)
	}
	return result, nil
}

func validateGoFileComponent(value string) error {
	if value == "" || value == "." || value == ".." {
		return fmt.Errorf("file name is empty or unsafe")
	}
	if filepath.IsAbs(value) || filepath.Base(value) != value {
		return fmt.Errorf("must be a single relative path component")
	}
	if len(value) > maxGoFileStemBytes {
		return fmt.Errorf("is too long: generated .go filename exceeds %d bytes", maxPortableComponentBytes)
	}
	if strings.HasPrefix(value, ".") || strings.HasPrefix(value, "_") {
		return fmt.Errorf("must not start with a dot or underscore because Go tooling ignores it")
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("must not start with a dash in a Go import path")
	}
	if strings.HasSuffix(value, ".") {
		return fmt.Errorf("must not end in a dot")
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("must not contain consecutive dots")
	}
	for _, r := range value {
		if !isGoImportPathRune(r) {
			return fmt.Errorf("contains a character that is invalid in a Go import path")
		}
	}
	if isReservedWindowsName(value) {
		return fmt.Errorf("is a reserved Windows device name")
	}
	if hasWindowsShortNameSuffix(value) {
		return fmt.Errorf("looks like a reserved Windows short name")
	}
	if isGoSpecialDirectory(value) {
		return fmt.Errorf("directory name %q is treated specially by Go tooling", value)
	}
	if isIgnoredOrConditionalGoSource(value) {
		return fmt.Errorf("generated source filename %q is ignored or build-constrained by Go tooling", value+".go")
	}
	return nil
}

func isGoImportPathRune(r rune) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	return strings.ContainsRune("-._~+", r)
}

func isGoSpecialDirectory(value string) bool {
	return strings.EqualFold(value, "vendor") || strings.EqualFold(value, "testdata")
}

func hasWindowsShortNameSuffix(value string) bool {
	stem, _ := splitFirstDot(value)
	tilde := strings.LastIndexByte(stem, '~')
	if tilde < 0 || tilde == len(stem)-1 {
		return false
	}
	for _, r := range stem[tilde+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func makeNonWindowsShortName(value string) string {
	if !hasWindowsShortNameSuffix(value) {
		return value
	}
	stem, suffix := splitFirstDot(value)
	return stem + "_" + suffix
}

func isIgnoredOrConditionalGoSource(value string) bool {
	if strings.HasSuffix(value, "_test") {
		return true
	}
	base, _ := splitFirstDot(value)
	base = strings.TrimSuffix(base, "_test")
	parts := strings.Split(base, "_")
	if len(parts) < 2 {
		return false
	}
	last := parts[len(parts)-1]
	if _, exists := knownGOOS[last]; exists {
		return true
	}
	if _, exists := knownGOARCH[last]; exists {
		return true
	}
	return false
}

func neutralizeGoSourceName(value string) string {
	if strings.HasSuffix(value, "_test") {
		return value + "_"
	}
	base, suffix := splitFirstDot(value)
	return base + "_" + suffix
}

func splitFirstDot(value string) (string, string) {
	if dot := strings.IndexByte(value, '.'); dot >= 0 {
		return value[:dot], value[dot:]
	}
	return value, ""
}

func truncateASCIIWithHash(value, hashInput string, limit int) string {
	hashSuffix := "_" + stableHash(hashInput)
	prefixLimit := limit - len(hashSuffix)
	if prefixLimit < 1 {
		return hashSuffix[len(hashSuffix)-limit:]
	}
	return value[:prefixLimit] + hashSuffix
}

func buildManifestFilename(databaseName string) (string, error) {
	if strings.TrimSpace(databaseName) == "" {
		return "", fmt.Errorf("database name is empty")
	}

	changed := !utf8.ValidString(databaseName)
	var stem strings.Builder
	for _, r := range databaseName {
		if isPortableManifestRune(r) {
			stem.WriteRune(r)
		} else {
			changed = true
		}
	}

	const prefix = "sqltom_"
	const extension = ".json"
	cleanStem := stem.String()
	if !portableComponentFits(prefix+cleanStem+extension, maxPortableComponentBytes, maxPortableComponentBytes) {
		changed = true
	}
	hashSuffix := ""
	if changed {
		hashSuffix = "_" + stableHash(databaseName)
	}
	maxStemBytes := maxPortableComponentBytes - len(prefix) - len(extension) - len(hashSuffix)
	maxStemUTF16 := maxPortableComponentBytes - len(prefix) - len(extension) - len(hashSuffix)
	cleanStem = truncateUnicode(cleanStem, maxStemBytes, maxStemUTF16)
	filename := prefix + cleanStem + hashSuffix + extension
	if !portableComponentFits(filename, maxPortableComponentBytes, maxPortableComponentBytes) {
		return "", fmt.Errorf("cannot derive a portable manifest filename from database %q", databaseName)
	}
	return filename, nil
}

func isPortableManifestRune(r rune) bool {
	if unicode.IsControl(r) || r == 0 {
		return false
	}
	return !strings.ContainsRune(`<>\":/\\|?*`, r)
}

func portableComponentFits(value string, maxBytes, maxUTF16 int) bool {
	if len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	units := 0
	for _, r := range value {
		length := utf16.RuneLen(r)
		if length < 0 {
			return false
		}
		units += length
	}
	return units <= maxUTF16
}

func truncateUnicode(value string, maxBytes, maxUTF16 int) string {
	bytesUsed := 0
	unitsUsed := 0
	for index, r := range value {
		byteLength := utf8.RuneLen(r)
		unitLength := utf16.RuneLen(r)
		if byteLength < 0 || unitLength < 0 || bytesUsed+byteLength > maxBytes || unitsUsed+unitLength > maxUTF16 {
			return value[:index]
		}
		bytesUsed += byteLength
		unitsUsed += unitLength
	}
	return value
}

func stableHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:stableHashHexBytes]
}
