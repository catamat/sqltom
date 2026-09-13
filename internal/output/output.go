// Package output installs generated directory trees without overwriting output
// that is not owned by sqltom.
package output

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	markerName    = ".sqltom-output"
	markerContent = "sqltom managed output v1\n"
	lockName      = ".sqltom-render.lock"
	lockOwnerName = "owner"
)

type committedError struct {
	output string
	err    error
}

func (e *committedError) Error() string {
	return fmt.Sprintf("new output is already active at %q; post-commit finalization failed: %v", e.output, e.err)
}

func (e *committedError) Unwrap() error {
	return e.err
}

// OutputCommitted reports that the generated directory is already active even
// though Build returned an error while performing post-commit cleanup.
func (e *committedError) OutputCommitted() bool {
	return true
}

// Build creates a private staging directory next to outputFolder, invokes build
// to populate it, and replaces outputFolder only after build succeeds. A
// non-empty existing output is replaced only when it carries sqltom's ownership
// marker. The callback must treat stagingDir as its output root and should check
// ctx during long-running work.
func Build(ctx context.Context, outputFolder string, build func(ctx context.Context, stagingDir string) error) (buildErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if build == nil {
		return fmt.Errorf("output build callback is nil")
	}
	outputPath, err := safePath(outputFolder)
	if err != nil {
		return err
	}
	if err := rejectManagedAncestor(outputPath); err != nil {
		return err
	}

	parent := filepath.Dir(outputPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create output parent: %w", err)
	}
	verifiedOutputPath, err := safePath(outputPath)
	if err != nil {
		return err
	}
	if verifiedOutputPath != outputPath {
		return fmt.Errorf("output path changed while preparing it: %q became %q", outputPath, verifiedOutputPath)
	}
	if err := rejectManagedAncestor(outputPath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	releaseLock, err := acquireLock(outputPath)
	if err != nil {
		return err
	}
	outputCommitted := false
	defer func() {
		if err := releaseLock(); err != nil {
			if outputCommitted {
				err = newCommittedError(outputPath, fmt.Errorf("release output lock: %w", err))
			}
			buildErr = errors.Join(buildErr, err)
		}
	}()
	// Recheck after taking the lock. An ancestor render may have committed
	// between path preparation and lock acquisition.
	if err := rejectManagedAncestor(outputPath); err != nil {
		return err
	}

	outputMode, err := replaceableMode(outputPath)
	if err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".sqltom-render-*")
	if err != nil {
		return fmt.Errorf("create rendering staging directory: %w", err)
	}
	defer func() {
		if staging == "" {
			return
		}
		if err := os.RemoveAll(staging); err != nil {
			err = fmt.Errorf("remove rendering staging directory: %w", err)
			if outputCommitted {
				err = newCommittedError(outputPath, err)
			}
			buildErr = errors.Join(buildErr, err)
		}
	}()
	if err := os.WriteFile(filepath.Join(staging, markerName), []byte(markerContent), 0o644); err != nil {
		return fmt.Errorf("write output ownership marker: %w", err)
	}

	if err := build(ctx, staging); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Chmod(staging, outputMode); err != nil {
		return fmt.Errorf("set output directory permissions: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	committed, err := replace(staging, outputPath)
	outputCommitted = committed
	if committed {
		staging = ""
	}
	if err != nil {
		if committed {
			return newCommittedError(outputPath, err)
		}
		return err
	}
	return nil
}

func newCommittedError(output string, err error) error {
	if err == nil {
		return nil
	}
	var committed interface{ OutputCommitted() bool }
	if errors.As(err, &committed) && committed.OutputCommitted() {
		return err
	}
	return &committedError{output: output, err: err}
}

func safePath(outputFolder string) (string, error) {
	if strings.TrimSpace(outputFolder) == "" {
		return "", fmt.Errorf("output folder is empty")
	}
	absolute, err := filepath.Abs(outputFolder)
	if err != nil {
		return "", fmt.Errorf("resolve output folder: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to use symlink output %q", absolute)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect output path: %w", err)
	}
	resolvedParent, err := resolvePath(filepath.Dir(absolute))
	if err != nil {
		return "", fmt.Errorf("resolve output parent: %w", err)
	}
	absolute = filepath.Join(resolvedParent, filepath.Base(absolute))
	if absolute == filepath.Dir(absolute) {
		return "", fmt.Errorf("refusing to use filesystem root as output")
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to use symlink output %q", absolute)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect resolved output path: %w", err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("read working directory: %w", err)
	}
	workingDirectory, err = resolvePath(workingDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	sameFile, err := sameFileOrAncestor(absolute, workingDirectory)
	if err != nil {
		return "", fmt.Errorf("compare output with working directory: %w", err)
	}
	if sameOrAncestor(absolute, workingDirectory) || sameFile {
		return "", fmt.Errorf("refusing to replace the working directory or one of its ancestors: %q", absolute)
	}
	return absolute, nil
}

func resolvePath(path string) (string, error) {
	path = filepath.Clean(path)
	missing := make([]string, 0)
	cursor := path
	for {
		info, err := os.Stat(cursor)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("path component %q is not a directory", cursor)
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", fmt.Errorf("no existing ancestor for %q", path)
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = parent
	}

	resolved, err := filepath.EvalSymlinks(cursor)
	if err != nil {
		return "", err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return filepath.Clean(resolved), nil
}

func sameOrAncestor(ancestor, path string) bool {
	relative, err := filepath.Rel(ancestor, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

// sameFileOrAncestor supplements lexical path comparison with filesystem
// identity. This catches case and Unicode-normalization aliases on
// case-insensitive filesystems.
func sameFileOrAncestor(candidate, path string) (bool, error) {
	candidateInfo, err := os.Stat(candidate)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	for current := path; ; current = filepath.Dir(current) {
		currentInfo, err := os.Stat(current)
		if err != nil {
			return false, err
		}
		if os.SameFile(candidateInfo, currentInfo) {
			return true, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
	}
}

func replaceableMode(output string) (os.FileMode, error) {
	info, err := os.Lstat(output)
	if os.IsNotExist(err) {
		return 0o755, nil
	}
	if err != nil {
		return 0, fmt.Errorf("inspect output: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("refusing to replace symlink output %q", output)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("output %q exists and is not a directory", output)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		return 0, fmt.Errorf("inspect output directory: %w", err)
	}
	if len(entries) == 0 {
		return info.Mode().Perm(), nil
	}

	markerPath := filepath.Join(output, markerName)
	markerInfo, err := os.Lstat(markerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("refusing to replace non-empty output %q: it is not managed by sqltom", output)
		}
		return 0, fmt.Errorf("inspect output ownership marker: %w", err)
	}
	if !markerInfo.Mode().IsRegular() || markerInfo.Size() != int64(len(markerContent)) {
		return 0, fmt.Errorf("refusing to replace output %q: invalid sqltom ownership marker", output)
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		return 0, fmt.Errorf("read output ownership marker: %w", err)
	}
	if string(marker) != markerContent {
		return 0, fmt.Errorf("refusing to replace output %q: invalid sqltom ownership marker", output)
	}
	return info.Mode().Perm(), nil
}

func rejectManagedAncestor(output string) error {
	for ancestor := filepath.Dir(output); ; ancestor = filepath.Dir(ancestor) {
		managed, err := hasOwnershipMarker(ancestor)
		if err != nil {
			return fmt.Errorf("inspect output ancestor %q: %w", ancestor, err)
		}
		if managed {
			return fmt.Errorf("refusing output %q inside managed sqltom output %q", output, ancestor)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return nil
		}
	}
}

func hasOwnershipMarker(directory string) (bool, error) {
	markerPath := filepath.Join(directory, markerName)
	markerInfo, err := os.Lstat(markerPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !markerInfo.Mode().IsRegular() || markerInfo.Size() != int64(len(markerContent)) {
		return false, nil
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		return false, err
	}
	return string(marker) == markerContent, nil
}

func acquireLock(output string) (func() error, error) {
	parent, err := resolvePath(filepath.Dir(output))
	if err != nil {
		return nil, fmt.Errorf("resolve output lock parent: %w", err)
	}
	lockPath := filepath.Join(parent, lockName)
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("create output lock token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("output parent %q is locked by another sqltom process; if no render is running, remove stale lock %q", parent, lockPath)
		}
		return nil, fmt.Errorf("acquire output lock: %w", err)
	}
	ownerPath := filepath.Join(lockPath, lockOwnerName)
	if err := os.WriteFile(ownerPath, []byte(token), 0o600); err != nil {
		removeOwnerErr := os.Remove(ownerPath)
		if os.IsNotExist(removeOwnerErr) {
			removeOwnerErr = nil
		}
		removeLockErr := os.Remove(lockPath)
		return nil, errors.Join(
			fmt.Errorf("write output lock owner: %w", err),
			wrapCleanupError("remove incomplete output lock owner", removeOwnerErr),
			wrapCleanupError("remove incomplete output lock", removeLockErr),
		)
	}

	return func() error {
		owner, err := os.ReadFile(ownerPath)
		if err != nil {
			return fmt.Errorf("verify output lock owner: %w", err)
		}
		if string(owner) != token {
			return fmt.Errorf("refusing to release output lock %q owned by another process", lockPath)
		}
		if err := os.Remove(ownerPath); err != nil {
			return fmt.Errorf("remove output lock owner: %w", err)
		}
		if err := os.Remove(lockPath); err != nil {
			return fmt.Errorf("release output lock: %w", err)
		}
		return nil
	}, nil
}

func wrapCleanupError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func replace(staging, output string) (bool, error) {
	if _, err := replaceableMode(output); err != nil {
		return false, err
	}

	backup := ""
	_, statErr := os.Lstat(output)
	switch {
	case statErr == nil:
		candidate, err := os.MkdirTemp(filepath.Dir(output), ".sqltom-backup-*")
		if err != nil {
			return false, fmt.Errorf("reserve output backup: %w", err)
		}
		if err := os.Remove(candidate); err != nil {
			return false, fmt.Errorf("prepare output backup: %w", err)
		}
		backup = candidate
		if err := os.Rename(output, backup); err != nil {
			return false, fmt.Errorf("back up previous output: %w", err)
		}
	case os.IsNotExist(statErr):
		// There is no previous output to preserve.
	default:
		return false, fmt.Errorf("inspect output before backup: %w", statErr)
	}

	if err := os.Rename(staging, output); err != nil {
		if backup != "" {
			if rollbackErr := os.Rename(backup, output); rollbackErr != nil {
				return false, errors.Join(
					fmt.Errorf("replace output: %w", err),
					fmt.Errorf("restore previous output from %q: %w", backup, rollbackErr),
				)
			}
		}
		return false, fmt.Errorf("replace output: %w", err)
	}
	if backup != "" {
		if err := os.RemoveAll(backup); err != nil {
			return true, backupRemovalError(backup, err)
		}
	}
	return true, nil
}

func backupRemovalError(backup string, err error) error {
	return fmt.Errorf("remove previous output backup %q: %w", backup, err)
}
