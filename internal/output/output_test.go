package output

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildCreatesManagedOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "models")
	err := Build(context.Background(), output, func(_ context.Context, stagingDir string) error {
		return os.WriteFile(filepath.Join(stagingDir, "model.go"), []byte("package model\n"), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(output, "model.go"), "package model\n")
	assertFileContent(t, filepath.Join(output, markerName), markerContent)
}

func TestBuildFailureLeavesPreviousOutputIntact(t *testing.T) {
	output := filepath.Join(t.TempDir(), "models")
	if err := Build(context.Background(), output, func(_ context.Context, stagingDir string) error {
		return os.WriteFile(filepath.Join(stagingDir, "keep.txt"), []byte("previous"), 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("generation failed")
	err := Build(context.Background(), output, func(_ context.Context, stagingDir string) error {
		if err := os.WriteFile(filepath.Join(stagingDir, "partial.txt"), []byte("partial"), 0o644); err != nil {
			return err
		}
		return cause
	})
	if !errors.Is(err, cause) {
		t.Fatalf("Build() error = %v, want generation failure", err)
	}
	assertFileContent(t, filepath.Join(output, "keep.txt"), "previous")
	if _, err := os.Stat(filepath.Join(output, "partial.txt")); !os.IsNotExist(err) {
		t.Fatalf("partial output became visible: %v", err)
	}
}

func TestBuildRefusesUnmanagedNonEmptyOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "models")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(output, "keep.txt")
	if err := os.WriteFile(keep, []byte("unrelated"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Build(context.Background(), output, emptyBuild)
	if err == nil || !strings.Contains(err.Error(), "not managed by sqltom") {
		t.Fatalf("Build() error = %v", err)
	}
	assertFileContent(t, keep, "unrelated")
}

func TestBuildRefusesOutputInsideManagedAncestor(t *testing.T) {
	root := t.TempDir()
	ancestor := filepath.Join(root, "models")
	if err := Build(context.Background(), ancestor, func(_ context.Context, stagingDir string) error {
		return os.WriteFile(filepath.Join(stagingDir, "keep.txt"), []byte("ancestor"), 0o644)
	}); err != nil {
		t.Fatal(err)
	}

	callbackCalled := false
	missingParent := filepath.Join(ancestor, "missing")
	child := filepath.Join(missingParent, "nested")
	err := Build(context.Background(), child, func(context.Context, string) error {
		callbackCalled = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "inside managed sqltom output") {
		t.Fatalf("Build() error = %v", err)
	}
	if callbackCalled {
		t.Fatal("Build() invoked the callback for a nested managed output")
	}
	assertFileContent(t, filepath.Join(ancestor, "keep.txt"), "ancestor")
	if _, err := os.Stat(missingParent); !os.IsNotExist(err) {
		t.Fatalf("nested output parent was created before refusal: %v", err)
	}
}

func TestBuildReplacesManagedOutputAndPreservesMode(t *testing.T) {
	output := filepath.Join(t.TempDir(), "models")
	if err := Build(context.Background(), output, emptyBuild); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(output, 0o750); err != nil {
		t.Fatal(err)
	}
	obsolete := filepath.Join(output, "obsolete.txt")
	if err := os.WriteFile(obsolete, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Build(context.Background(), output, emptyBuild); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(obsolete); !os.IsNotExist(err) {
		t.Fatalf("obsolete file still exists: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(output)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o750 {
			t.Fatalf("output permissions = %o, want 750", got)
		}
	}
}

func TestSafePathRejectsWorkingDirectoryAndAncestors(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{workingDirectory, filepath.Dir(workingDirectory)} {
		if _, err := safePath(candidate); err == nil || !strings.Contains(err.Error(), "working directory or one of its ancestors") {
			t.Errorf("safePath(%q) error = %v", candidate, err)
		}
	}
}

func TestSafePathRejectsCaseVariantOfWorkingDirectoryAndAncestor(t *testing.T) {
	root := t.TempDir()
	workingParent := filepath.Join(root, "managed")
	workingDirectory := filepath.Join(workingParent, "child")
	if err := os.MkdirAll(workingDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	parentAlias := filepath.Join(root, "MANAGED")
	workingAlias := filepath.Join(parentAlias, "CHILD")
	aliasInfo, err := os.Stat(workingAlias)
	if os.IsNotExist(err) {
		t.Skip("filesystem is case-sensitive")
	}
	if err != nil {
		t.Fatal(err)
	}
	workingInfo, err := os.Stat(workingDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(aliasInfo, workingInfo) {
		t.Skip("case variant does not identify the same directory")
	}

	t.Chdir(workingDirectory)
	for _, candidate := range []string{workingAlias, parentAlias} {
		if _, err := safePath(candidate); err == nil || !strings.Contains(err.Error(), "working directory or one of its ancestors") {
			t.Errorf("safePath(%q) error = %v", candidate, err)
		}
	}
}

func TestSafePathResolvesIntermediateSymlinkAndRejectsFinalSymlink(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "real")
	if err := os.Mkdir(realParent, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realParent, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	resolved, err := safePath(filepath.Join(alias, "models"))
	if err != nil {
		t.Fatal(err)
	}
	canonicalParent, err := filepath.EvalSymlinks(realParent)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(canonicalParent, "models"); resolved != want {
		t.Fatalf("resolved output = %q, want %q", resolved, want)
	}

	target := filepath.Join(realParent, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	finalLink := filepath.Join(root, "output-link")
	if err := os.Symlink(target, finalLink); err != nil {
		t.Fatal(err)
	}
	if _, err := safePath(finalLink); err == nil || !strings.Contains(err.Error(), "symlink output") {
		t.Fatalf("safePath(final symlink) error = %v", err)
	}
}

func TestCanceledImmediatelyBeforeCommitDoesNotReplaceManagedOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "models")
	if err := Build(context.Background(), output, func(_ context.Context, stagingDir string) error {
		return os.WriteFile(filepath.Join(stagingDir, "keep.txt"), []byte("previous"), 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	ctx := &cancelOnErrCallContext{Context: context.Background(), cancelAt: 4}
	err := Build(ctx, output, emptyBuild)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Build() error = %v, want context.Canceled", err)
	}
	if ctx.calls != ctx.cancelAt {
		t.Fatalf("context Err calls = %d, want %d", ctx.calls, ctx.cancelAt)
	}
	assertFileContent(t, filepath.Join(output, "keep.txt"), "previous")
}

func TestCanceledBuildDoesNotCreateOutputParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "missing")
	output := filepath.Join(parent, "models")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Build(ctx, output, emptyBuild)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Build() error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("canceled build created output parent: %v", err)
	}
}

func TestNilCallbackDoesNotCreateOutputParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "missing")
	err := Build(context.Background(), filepath.Join(parent, "models"), nil)
	if err == nil || !strings.Contains(err.Error(), "callback is nil") {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("nil callback created output parent: %v", err)
	}
}

func TestOutputLockExcludesSiblingOutputs(t *testing.T) {
	root := t.TempDir()
	firstOutput := filepath.Join(root, "models-a")
	secondOutput := filepath.Join(root, "models-b")
	release, err := acquireLock(firstOutput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(secondOutput); err == nil || !strings.Contains(err.Error(), "locked by another sqltom process") {
		t.Fatalf("sibling acquire error = %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	release, err = acquireLock(secondOutput)
	if err != nil {
		t.Fatalf("acquire sibling after release: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestHasOwnershipMarkerRequiresExactRegularMarker(t *testing.T) {
	directory := t.TempDir()
	managed, err := hasOwnershipMarker(directory)
	if err != nil || managed {
		t.Fatalf("missing marker = (%v, %v), want (false, nil)", managed, err)
	}
	if err := os.WriteFile(filepath.Join(directory, markerName), []byte("not sqltom"), 0o644); err != nil {
		t.Fatal(err)
	}
	managed, err = hasOwnershipMarker(directory)
	if err != nil || managed {
		t.Fatalf("invalid marker = (%v, %v), want (false, nil)", managed, err)
	}
	if err := os.WriteFile(filepath.Join(directory, markerName), []byte(markerContent), 0o644); err != nil {
		t.Fatal(err)
	}
	managed, err = hasOwnershipMarker(directory)
	if err != nil || !managed {
		t.Fatalf("valid marker = (%v, %v), want (true, nil)", managed, err)
	}
}

func TestReplaceRestoresBackupWhenInstallRenameFails(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "models")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, markerName), []byte(markerContent), 0o644); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(output, "keep.txt")
	if err := os.WriteFile(keep, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed, err := replace(filepath.Join(root, "missing-staging"), output)
	if err == nil || !strings.Contains(err.Error(), "replace output") {
		t.Fatalf("replace() error = %v", err)
	}
	if committed {
		t.Fatal("replace() reported a commit after restoring the previous output")
	}
	assertFileContent(t, keep, "previous")
}

func TestReplaceReportsSuccessfulCommit(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, markerName), []byte(markerContent), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "models")
	committed, err := replace(staging, output)
	if err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("replace() did not report the successful commit")
	}
	assertFileContent(t, filepath.Join(output, markerName), markerContent)
}

func TestCommittedErrorIsExplicitAndUnwrapsCause(t *testing.T) {
	cause := errors.New("cleanup failed")
	err := newCommittedError("models", cause)
	if !strings.Contains(err.Error(), "new output is already active") {
		t.Fatalf("committed error = %q", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("committed error does not unwrap cause: %v", err)
	}
	var state interface{ OutputCommitted() bool }
	if !errors.As(err, &state) || !state.OutputCommitted() {
		t.Fatalf("committed error does not expose commit state: %v", err)
	}
}

func TestBackupRemovalErrorIncludesRecoverablePath(t *testing.T) {
	cause := errors.New("permission denied")
	err := backupRemovalError("/tmp/.sqltom-backup-123", cause)
	if !strings.Contains(err.Error(), `backup "/tmp/.sqltom-backup-123"`) {
		t.Fatalf("backup removal error = %q", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("backup removal error does not unwrap cause: %v", err)
	}
}

func emptyBuild(context.Context, string) error {
	return nil
}

type cancelOnErrCallContext struct {
	context.Context
	cancelAt int
	calls    int
}

func (c *cancelOnErrCallContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func assertFileContent(t *testing.T, filename, want string) {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", filename, data, want)
	}
}
