// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package system

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
)

func TestFileExplorerPathOverlaps(t *testing.T) {
	root := "/zroot/sylve/jails/42"
	for _, tt := range []struct {
		path string
		want bool
	}{
		{path: "/zroot/sylve/jails/42/etc/rc.conf", want: true},
		{path: "/zroot/sylve/jails", want: true},
		{path: "/zroot/sylve/jails/420/etc/rc.conf", want: false},
		{path: "/zroot/sylve/other", want: false},
	} {
		if got := fileExplorerPathOverlaps(tt.path, root); got != tt.want {
			t.Errorf("fileExplorerPathOverlaps(%q, %q) = %t, want %t", tt.path, root, got, tt.want)
		}
	}
}

func TestResolveFileExplorerGuardPathResolvesExistingSymlinkPrefix(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realDirectory, alias); err != nil {
		t.Fatal(err)
	}

	got := resolveFileExplorerGuardPath(filepath.Join(alias, "new", "file.img"))
	want := filepath.Join(realDirectory, "new", "file.img")
	if got != want {
		t.Fatalf("resolved path=%q want=%q", got, want)
	}
}

func TestNormalizeFileExplorerDeletePathsRejectsRoot(t *testing.T) {
	for _, path := range []string{"/", "/tmp/.."} {
		t.Run(path, func(t *testing.T) {
			_, err := normalizeFileExplorerDeletePaths([]string{path})
			if !errors.Is(err, ErrFileExplorerRootMutation) {
				t.Fatalf("error = %v; want %v", err, ErrFileExplorerRootMutation)
			}
		})
	}
}

func TestNormalizeFileExplorerDeletePathsDeduplicatesCanonicalPaths(t *testing.T) {
	root := t.TempDir()
	paths, err := normalizeFileExplorerDeletePaths([]string{root, filepath.Join(root, ".")})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != root {
		t.Fatalf("paths = %#v; want [%q]", paths, root)
	}
}

func TestNormalizeFileExplorerDeletePathsBoundsBatch(t *testing.T) {
	paths := make([]string, MaxFileExplorerBatchItems+1)
	for i := range paths {
		paths[i] = filepath.Join("/tmp", "file-explorer-test-"+strconv.Itoa(i))
	}

	_, err := normalizeFileExplorerDeletePaths(paths)
	if !errors.Is(err, ErrFileExplorerBatchTooLarge) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerBatchTooLarge)
	}
}

func TestTraverseEmptyDirectoryReturnsNonNilSlice(t *testing.T) {
	nodes, err := (&Service{}).Traverse(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if nodes == nil || len(nodes) != 0 {
		t.Fatalf("nodes = %#v; want non-nil empty slice", nodes)
	}
}

func TestAddFileOrFolderDoesNotReplaceDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing"), link); err != nil {
		t.Fatal(err)
	}

	err := (&Service{}).AddFileOrFolder(root, "dangling", false)
	if !errors.Is(err, ErrFileExplorerAlreadyExists) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerAlreadyExists)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("dangling symlink was replaced or removed: %v", err)
	}
}

func TestDeleteFilesOrFoldersDeletesDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing"), link); err != nil {
		t.Fatal(err)
	}

	if err := (&Service{}).DeleteFilesOrFolders([]string{link}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("Lstat error = %v; want not exist", err)
	}
}

func TestDeleteFilesOrFoldersPreflightsEntireBatch(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := (&Service{}).DeleteFilesOrFolders([]string{existing, filepath.Join(root, "missing")})
	if !errors.Is(err, ErrFileExplorerNotFound) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerNotFound)
	}
	if _, err := os.Lstat(existing); err != nil {
		t.Fatalf("preflight failure removed the first path: %v", err)
	}
}

func TestRenameFileOrFolderRenamesDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old-link")
	newPath := filepath.Join(root, "new-link")
	target := filepath.Join(root, "missing")
	if err := os.Symlink(target, oldPath); err != nil {
		t.Fatal(err)
	}

	if err := (&Service{}).RenameFileOrFolder(oldPath, "new-link"); err != nil {
		t.Fatal(err)
	}
	gotTarget, err := os.Readlink(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if gotTarget != target {
		t.Fatalf("symlink target = %q; want %q", gotTarget, target)
	}
}

func TestRenameFileOrFolderSameNameIsNoOp(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "entry")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Service{}).RenameFileOrFolder(path, "entry"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "content" {
		t.Fatalf("contents = %q; want content", contents)
	}
}

func TestRenameFileOrFolderRejectsRoot(t *testing.T) {
	err := (&Service{}).RenameFileOrFolder("/tmp/..", "renamed-root")
	if !errors.Is(err, ErrFileExplorerRootMutation) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerRootMutation)
	}
}

func TestDownloadFileOpensRegularFileAndKeepsValidatedHandle(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "disk-image.raw")
	if err := os.WriteFile(path, []byte("original contents"), 0o600); err != nil {
		t.Fatal(err)
	}

	download, err := (&Service{}).DownloadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Reader.Close()

	if download.Name != "disk-image.raw" {
		t.Fatalf("download name = %q; want disk-image.raw", download.Name)
	}
	if download.ModTime.IsZero() {
		t.Fatal("download modification time is zero")
	}

	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement contents"), 0o600); err != nil {
		t.Fatal(err)
	}

	contents, err := io.ReadAll(download.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "original contents" {
		t.Fatalf("downloaded contents = %q; want original contents", contents)
	}
}

func TestDownloadFileAllowsSymlinkToRegularFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.iso")
	link := filepath.Join(root, "alias.iso")
	if err := os.WriteFile(target, []byte("iso contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	download, err := (&Service{}).DownloadFile(link)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Reader.Close()
	if download.Name != "alias.iso" {
		t.Fatalf("download name = %q; want alias.iso", download.Name)
	}
	contents, err := io.ReadAll(download.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "iso contents" {
		t.Fatalf("downloaded contents = %q; want iso contents", contents)
	}
}

func TestDownloadFileRejectsInvalidMissingAndSpecialPaths(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "blocked.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{name: "relative", path: "relative.img", wantErr: ErrFileExplorerInvalidPath},
		{name: "missing", path: filepath.Join(root, "missing.img"), wantErr: ErrFileExplorerNotFound},
		{name: "directory", path: root, wantErr: ErrFileExplorerUnsupportedType},
		{name: "fifo", path: fifo, wantErr: ErrFileExplorerUnsupportedType},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			download, err := (&Service{}).DownloadFile(test.path)
			if download != nil {
				_ = download.Reader.Close()
				t.Fatalf("download = %#v; want nil", download)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v; want %v", err, test.wantErr)
			}
		})
	}
}

func TestCopyOrMoveFilesOrFoldersStreamsRegularFileAndPreservesMode(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.img")
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	contents := bytes.Repeat([]byte("streamed-file-content"), 32*1024)
	if err := os.WriteFile(source, contents, 0o640); err != nil {
		t.Fatal(err)
	}

	err := (&Service{}).CopyOrMoveFilesOrFolders(
		[]systemServiceInterfaces.FileTransferItem{{Source: source, Destination: destination}},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(destination, filepath.Base(source))
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, contents) {
		t.Fatalf("copied content length = %d; want %d", len(got), len(contents))
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("copied mode = %o; want 640", info.Mode().Perm())
	}
}

func TestCopyOrMoveFilesOrFoldersPreservesDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source-link")
	target := filepath.Join(root, "copied-link")
	if err := os.Symlink("missing-relative-target", source); err != nil {
		t.Fatal(err)
	}

	err := (&Service{}).CopyOrMoveFilesOrFolders(
		[]systemServiceInterfaces.FileTransferItem{{Source: source, Destination: target}},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != "missing-relative-target" {
		t.Fatalf("copied symlink target = %q; want missing-relative-target", got)
	}
}

func TestCopyOrMoveFilesOrFoldersRejectsSpecialFileWithoutOpeningIt(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source-fifo")
	target := filepath.Join(root, "copied-fifo")
	if err := syscall.Mkfifo(source, 0o600); err != nil {
		t.Fatal(err)
	}

	err := (&Service{}).CopyOrMoveFilesOrFolders(
		[]systemServiceInterfaces.FileTransferItem{{Source: source, Destination: target}},
		false,
	)
	if !errors.Is(err, ErrFileExplorerUnsupportedType) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerUnsupportedType)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target Lstat error = %v; want not exist", err)
	}
}

func TestCopyOrMoveFilesOrFoldersRejectsSelfAndDescendantTargets(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{source, filepath.Join(source, "nested-copy")} {
		t.Run(target, func(t *testing.T) {
			err := (&Service{}).CopyOrMoveFilesOrFolders(
				[]systemServiceInterfaces.FileTransferItem{{Source: source, Destination: target}},
				false,
			)
			if !errors.Is(err, ErrFileExplorerInvalidOperation) {
				t.Fatalf("error = %v; want %v", err, ErrFileExplorerInvalidOperation)
			}
		})
	}
}

func TestCopyOrMoveFilesOrFoldersDoesNotOverwriteDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := (&Service{}).CopyOrMoveFilesOrFolders(
		[]systemServiceInterfaces.FileTransferItem{{Source: source, Destination: target}},
		false,
	)
	if !errors.Is(err, ErrFileExplorerAlreadyExists) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerAlreadyExists)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "keep" {
		t.Fatalf("target contents = %q; want keep", contents)
	}
}

func TestCopyOrMoveFilesOrFoldersPreflightsEntireBatch(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	firstTarget := filepath.Join(root, "first-target")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := (&Service{}).CopyOrMoveFilesOrFolders(
		[]systemServiceInterfaces.FileTransferItem{
			{Source: source, Destination: firstTarget},
			{Source: filepath.Join(root, "missing"), Destination: filepath.Join(root, "second-target")},
		},
		false,
	)
	if !errors.Is(err, ErrFileExplorerNotFound) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerNotFound)
	}
	if _, err := os.Lstat(firstTarget); !os.IsNotExist(err) {
		t.Fatalf("preflight failure created first target: %v", err)
	}
}

func TestCopyOrMoveFilesOrFoldersRejectsDuplicateSources(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := (&Service{}).CopyOrMoveFilesOrFolders(
		[]systemServiceInterfaces.FileTransferItem{
			{Source: source, Destination: filepath.Join(root, "target-one")},
			{Source: source, Destination: filepath.Join(root, "target-two")},
		},
		false,
	)
	if !errors.Is(err, ErrFileExplorerBatchConflict) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerBatchConflict)
	}
}

func TestCopyOrMoveFilesOrFoldersBoundsBatch(t *testing.T) {
	items := make([]systemServiceInterfaces.FileTransferItem, MaxFileExplorerBatchItems+1)
	err := (&Service{}).CopyOrMoveFilesOrFolders(items, false)
	if !errors.Is(err, ErrFileExplorerBatchTooLarge) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerBatchTooLarge)
	}
}

func TestCopyOrMoveFilesOrFoldersFallsBackAcrossFilesystems(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(source, []byte("move-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	service := &Service{
		fileExplorerRename: func(oldPath, newPath string) error {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
		},
	}
	if err := service.CopyOrMoveFilesOrFolders(
		[]systemServiceInterfaces.FileTransferItem{{Source: source, Destination: target}},
		true,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Fatalf("source Lstat error = %v; want not exist", err)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "move-me" {
		t.Fatalf("target contents = %q; want move-me", contents)
	}
}

func TestCopyFileExplorerEntryCleansPartialDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "a-file"), []byte("copied first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(source, "z-fifo"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := copyFileExplorerEntry(source, target)
	if !errors.Is(err, ErrFileExplorerUnsupportedType) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerUnsupportedType)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("partial target Lstat error = %v; want not exist", err)
	}
}
