package manifest

import (
	"bytes"
	jsonv1 "encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// UnmarshalJSON makes IsManaged default to true while retaining strict
// decoding for table fields.
func (t *Table) UnmarshalJSON(data []byte) error {
	type tableJSON Table
	decoded := tableJSON{IsManaged: true}
	if err := rejectJSONNull(data); err != nil {
		return fmt.Errorf("decode table: %w", err)
	}
	if err := jsonv2.Unmarshal(data, &decoded, jsonv2.RejectUnknownMembers(true)); err != nil {
		return fmt.Errorf("decode table: %w", err)
	}
	*t = Table(decoded)
	return nil
}

func Load(filename string) (*Manifest, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("open manifest: %w", err)
	}
	if err := rejectJSONNull(data); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	var m Manifest
	if err := jsonv2.Unmarshal(data, &m, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	Canonicalize(&m)
	if err := ValidateStructure(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func rejectJSONNull(data []byte) error {
	var value any
	if err := jsonv2.Unmarshal(data, &value); err != nil {
		return err
	}
	if path, found := firstJSONNull(value, "$"); found {
		return fmt.Errorf("%s must not be null", path)
	}
	return nil
}

func firstJSONNull(value any, path string) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return path, true
	case []any:
		for index, element := range typed {
			if foundPath, found := firstJSONNull(element, fmt.Sprintf("%s[%d]", path, index)); found {
				return foundPath, true
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if foundPath, found := firstJSONNull(typed[key], path+"."+key); found {
				return foundPath, true
			}
		}
	}
	return "", false
}

func SaveAtomic(filename string, m *Manifest) error {
	normalizeTableManagement(m)
	if err := ValidateStructure(m); err != nil {
		return fmt.Errorf("validate manifest: %w", err)
	}
	Canonicalize(m)

	var data bytes.Buffer
	encoder := jsonv1.NewEncoder(&data)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(m); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	manifestMode := os.FileMode(0o644)
	if info, err := os.Lstat(filename); err == nil {
		if info.Mode().IsRegular() {
			manifestMode = info.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect existing manifest: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".sqltom-manifest-*")
	if err != nil {
		return fmt.Errorf("create temporary manifest: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)

	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
	}()
	if _, err := temporary.Write(data.Bytes()); err != nil {
		return fmt.Errorf("write temporary manifest: %w", err)
	}
	if err := temporary.Chmod(manifestMode); err != nil {
		return fmt.Errorf("set manifest permissions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary manifest: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryName, filename); err != nil {
		return fmt.Errorf("replace manifest: %w", err)
	}
	return nil
}
