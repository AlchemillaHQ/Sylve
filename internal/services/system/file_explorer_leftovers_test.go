// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package system

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"golang.org/x/sys/unix"
)

func unprotectedService() *Service {
	return &Service{fileExplorerProtectionLoader: func() (*fileExplorerProtection, error) {
		return newFileExplorerProtection(nil, ""), nil
	}}
}

func TestListDirectoryCapsFoldersFirstAndSearchesEverything(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"b-folder", "A-folder"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 6 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service := unprotectedService()

	listing, err := service.ListDirectory(root, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range listing.Entries {
		names = append(names, filepath.Base(entry.ID))
	}
	if listing.Total != 8 || listing.Matched != 8 || !listing.Truncated || strings.Join(names, ",") != "A-folder,b-folder,file-0.txt" {
		t.Fatalf("capped listing = %+v (%v); want 8 total, truncated, folders first", listing, names)
	}

	listing, err = service.ListDirectory(root, 3, "FILE-5")
	if err != nil {
		t.Fatal(err)
	}
	if listing.Total != 8 || listing.Matched != 1 || listing.Truncated || len(listing.Entries) != 1 || filepath.Base(listing.Entries[0].ID) != "file-5.txt" {
		t.Fatalf("search = %+v; want only file-5.txt", listing)
	}

	listing, err = service.ListDirectory(root, 0, "")
	if err != nil || listing.Truncated || len(listing.Entries) != 8 {
		t.Fatalf("default limit = %+v (%v); want everything", listing, err)
	}
	if _, err := service.ListDirectory(filepath.Join(root, "file-1.txt"), 10, ""); !errors.Is(err, ErrFileExplorerNotDirectory) {
		t.Fatalf("listing a file returned %v; want %v", err, ErrFileExplorerNotDirectory)
	}
}

func TestWriteFolderArchiveStoresTreeLinksAndSkippedItems(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := bytes.Repeat([]byte("archive me\n"), 4096)
	if err := os.WriteFile(filepath.Join(root, "docs", "notes.txt"), contents, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("docs/notes.txt", filepath.Join(root, "shortcut")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}

	archive, err := unprotectedService().PrepareArchive([]string{root})
	if err != nil || archive.Name != "project" {
		t.Fatalf("prepare = %+v %v", archive, err)
	}
	files, read := writeTestArchive(t, archive)
	if _, ok := files["project/docs/"]; !ok {
		t.Fatal("archive is missing the docs folder")
	}
	if read("project/docs/notes.txt") != string(contents) || files["project/docs/notes.txt"].Mode().Perm() != 0o640 {
		t.Fatal("notes.txt has the wrong contents or mode")
	}
	if files["project/shortcut"].Mode()&fs.ModeSymlink == 0 || read("project/shortcut") != "docs/notes.txt" {
		t.Fatal("the symlink was not stored as a link")
	}
	if _, ok := files["project/pipe"]; ok {
		t.Fatal("the FIFO was archived")
	}
	if note := read("project/" + FileExplorerArchiveNoteName); !strings.Contains(note, "project/pipe") {
		t.Fatalf("the skipped note does not mention the FIFO: %q", note)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := WriteArchive(cancelled, archive, io.Discard); !errors.Is(err, ErrFileExplorerCancelled) {
		t.Fatalf("cancelled archive returned %v; want %v", err, ErrFileExplorerCancelled)
	}
	if _, err := unprotectedService().PrepareArchive([]string{filepath.Join(root, "docs", "notes.txt")}); !errors.Is(err, ErrFileExplorerNotDirectory) {
		t.Fatalf("archiving a file returned %v; want %v", err, ErrFileExplorerNotDirectory)
	}
}

func writeTestArchive(t *testing.T, archive FileExplorerArchive) (map[string]*zip.File, func(string) string) {
	t.Helper()
	var output bytes.Buffer
	if err := WriteArchive(t.Context(), archive, &output); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	files := map[string]*zip.File{}
	for _, file := range reader.File {
		files[file.Name] = file
	}
	read := func(name string) string {
		t.Helper()
		file, ok := files[name]
		if !ok {
			t.Fatalf("archive is missing %s (has %d entries)", name, len(files))
		}
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer handle.Close()
		data, err := io.ReadAll(handle)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	return files, read
}

func TestArchiveOfSeveralItemsKeepsThemAtTheTopLevel(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "share")
	if err := os.MkdirAll(filepath.Join(parent, "photos", "2026"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, data string) string {
		t.Helper()
		path := filepath.Join(parent, name)
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("photos/2026/beach.txt", "sand")
	report := write("report.txt", "quarterly")
	skipNote := write(FileExplorerArchiveNoteName, "mine")
	write("ignored.txt", "not selected")
	pipe := filepath.Join(parent, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "latest")
	if err := os.Symlink("photos/2026", link); err != nil {
		t.Fatal(err)
	}
	service := unprotectedService()

	archive, err := service.PrepareArchive([]string{filepath.Join(parent, "photos"), report, skipNote, pipe, link})
	if err != nil {
		t.Fatal(err)
	}
	if archive.Name != "share - 5 items" {
		t.Fatalf("archive name = %q", archive.Name)
	}
	files, read := writeTestArchive(t, archive)
	if read("photos/2026/beach.txt") != "sand" || read("report.txt") != "quarterly" || read(FileExplorerArchiveNoteName) != "mine" {
		t.Fatal("a selected item is missing or has the wrong contents")
	}
	if files["latest"].Mode()&fs.ModeSymlink == 0 || read("latest") != "photos/2026" {
		t.Fatal("the selected symlink was not stored as a link")
	}
	if _, ok := files["ignored.txt"]; ok {
		t.Fatal("an item that was not selected was archived")
	}
	if _, ok := files["pipe"]; ok {
		t.Fatal("the FIFO was archived")
	}
	if note := read("SYLVE-SKIPPED (2).txt"); !strings.Contains(note, "pipe: ") {
		t.Fatalf("the skipped note did not avoid the selected file's name or omits the FIFO: %q", note)
	}

	for name, ids := range map[string][]string{
		"mixed parents": {report, filepath.Join(parent, "photos", "2026")},
		"duplicates":    {report, report},
		"missing":       {report, filepath.Join(parent, "gone.txt")},
		"root":          {"/", report},
		"relative":      {"report.txt", "ignored.txt"},
	} {
		if _, err := service.PrepareArchive(ids); err == nil {
			t.Fatalf("%s: preparing %v succeeded", name, ids)
		}
	}
	tooMany := make([]string, MaxFileExplorerBatchItems+1)
	for index := range tooMany {
		tooMany[index] = filepath.Join(parent, fmt.Sprintf("item-%d", index))
	}
	if _, err := service.PrepareArchive(tooMany); !errors.Is(err, ErrFileExplorerBatchTooLarge) {
		t.Fatalf("too many items returned %v; want %v", err, ErrFileExplorerBatchTooLarge)
	}
}

func TestRenameNoReplaceNeverOverwrites(t *testing.T) {
	for _, mode := range []string{"renameat2", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "fallback" {
				previous := renameat2Unavailable.Load()
				renameat2Unavailable.Store(true)
				t.Cleanup(func() { renameat2Unavailable.Store(previous) })
			}
			root := t.TempDir()
			write := func(name, data string) string {
				t.Helper()
				path := filepath.Join(root, name)
				if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
				return path
			}
			source := write("source", "new")
			target := write("target", "keep")
			if err := renameNoReplace(source, target); !errors.Is(err, fs.ErrExist) {
				t.Fatalf("file over file returned %v; want exists", err)
			}
			if data, _ := os.ReadFile(target); string(data) != "keep" {
				t.Fatal("the existing file was overwritten")
			}
			if err := renameNoReplace(source, filepath.Join(root, "renamed")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(source); !os.IsNotExist(err) {
				t.Fatal("the source file still exists after the rename")
			}

			folder := filepath.Join(root, "folder")
			empty := filepath.Join(root, "empty")
			for _, dir := range []string{folder, empty} {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write("folder/inside", "data")
			if err := renameNoReplace(folder, empty); !errors.Is(err, fs.ErrExist) {
				t.Fatalf("folder over empty folder returned %v; want exists", err)
			}
			if err := renameNoReplace(folder, filepath.Join(root, "moved")); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filepath.Join(root, "moved", "inside")); err != nil || string(data) != "data" {
				t.Fatalf("the folder did not move with its contents: %v", err)
			}

			link := filepath.Join(root, "link")
			if err := os.Symlink("missing", link); err != nil {
				t.Fatal(err)
			}
			if err := renameNoReplace(link, filepath.Join(root, "link2")); err != nil {
				t.Fatal(err)
			}
			if got, err := os.Readlink(filepath.Join(root, "link2")); err != nil || got != "missing" {
				t.Fatalf("the symlink itself was not renamed: %q %v", got, err)
			}
		})
	}
}

func TestCancelledCopiesLeaveNothingBehind(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.img")
	if err := os.WriteFile(source, bytes.Repeat([]byte("x"), 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	target := filepath.Join(root, "copy.img")
	if err := copyFileExplorerRegularFile(cancelled, source, target, info); !errors.Is(err, ErrFileExplorerCancelled) {
		t.Fatalf("cancelled file copy returned %v; want %v", err, ErrFileExplorerCancelled)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("a cancelled copy left a partial file")
	}
	err = unprotectedService().CopyOrMoveFilesOrFolders(
		cancelled,
		[]systemServiceInterfaces.FileTransferItem{{Source: source, Destination: filepath.Join(root, "batch.img")}},
		false,
	)
	if !errors.Is(err, ErrFileExplorerCancelled) {
		t.Fatalf("cancelled batch returned %v; want %v", err, ErrFileExplorerCancelled)
	}
	if _, err := os.Lstat(filepath.Join(root, "batch.img")); !os.IsNotExist(err) {
		t.Fatal("a cancelled batch created its target")
	}
}

func TestDeleteReportsItemsCompletedBeforeAFailure(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to set file flags")
	}
	const systemImmutable = 0x00020000
	root := t.TempDir()
	first := filepath.Join(root, "a-first")
	second := filepath.Join(root, "b-second")
	if err := os.WriteFile(first, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o755); err != nil {
		t.Fatal(err)
	}
	stuck := filepath.Join(second, "stuck")
	if err := os.WriteFile(stuck, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := unix.Chflags(stuck, systemImmutable); err != nil {
		t.Skipf("cannot set the system-immutable flag: %v", err)
	}
	t.Cleanup(func() { _ = unix.Chflags(stuck, 0) })

	err := unprotectedService().DeleteFilesOrFolders(t.Context(), []string{first, second})
	var batch *FileExplorerBatchError
	if !errors.As(err, &batch) || len(batch.Completed) != 1 || batch.Completed[0] != first {
		t.Fatalf("error = %v; want a batch error listing %s as completed", err, first)
	}
	if _, err := os.Lstat(first); !os.IsNotExist(err) {
		t.Fatal("the completed item still exists")
	}
}

func TestSweepFileExplorerPartialsRemovesOnlyInterruptedUploads(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Upload{})
	service := &Service{DB: database}
	root := t.TempDir()

	stale := FileExplorerPartialPath(root)
	current := FileExplorerPartialPath(root)
	foreign := filepath.Join(root, "not-a-partial.txt")
	for _, path := range []string{stale, current, foreign} {
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	register := func(path string, created time.Time) string {
		t.Helper()
		id, err := service.RegisterFileExplorerPartial(path, "user:1")
		if err != nil {
			t.Fatal(err)
		}
		if err := database.Model(&utilitiesModels.Upload{}).Where("id = ?", id).Update("created_at", created).Error; err != nil {
			t.Fatal(err)
		}
		return id
	}
	register(stale, fileExplorerProcessStart.Add(-time.Hour))
	register(foreign, fileExplorerProcessStart.Add(-time.Hour))
	currentID := register(current, time.Now())

	removed, err := service.SweepFileExplorerPartials(t.Context())
	if err != nil || removed != 1 {
		t.Fatalf("sweep removed %d (%v); want 1", removed, err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatal("the interrupted upload was not removed")
	}
	for _, path := range []string{current, foreign} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("%s was removed: %v", path, err)
		}
	}
	var remaining []utilitiesModels.Upload
	if err := database.Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != currentID {
		t.Fatalf("records left = %+v; want only the in-progress upload", remaining)
	}
	service.ReleaseFileExplorerPartial(currentID)
	if err := database.Find(&remaining).Error; err != nil || len(remaining) != 0 {
		t.Fatalf("release left %d records (%v)", len(remaining), err)
	}
}

func runTool(t *testing.T, name string, args ...string) string {
	t.Helper()
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestCopyKeepsHardLinksACLsAttributesAndFlags(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	const userNoDump = 0x1
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(source, "first")
	if err := os.WriteFile(first, []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, filepath.Join(source, "second")); err != nil {
		t.Fatal(err)
	}
	runTool(t, "setfacl", "-a", "0", "u:1234:rwxp::allow", first)
	runTool(t, "setextattr", "user", "sylve.test", "hello", first)
	if err := unix.Chflags(first, userNoDump); err != nil {
		t.Skipf("cannot set nodump: %v", err)
	}

	target := filepath.Join(root, "copy")
	if err := unprotectedService().CopyOrMoveFilesOrFolders(
		t.Context(),
		[]systemServiceInterfaces.FileTransferItem{{Source: source, Destination: target}},
		false,
	); err != nil {
		t.Fatal(err)
	}
	copiedFirst := filepath.Join(target, "first")
	firstInfo, err := os.Lstat(copiedFirst)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Lstat(filepath.Join(target, "second"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("the hard link became two separate files")
	}
	if got, want := runTool(t, "getfacl", "-q", copiedFirst), runTool(t, "getfacl", "-q", first); got != want {
		t.Fatalf("ACL = %q; want %q", got, want)
	}
	if got := runTool(t, "getextattr", "-q", "user", "sylve.test", copiedFirst); got != "hello" {
		t.Fatalf("extended attribute = %q; want hello", got)
	}
	if firstInfo.Sys().(*syscall.Stat_t).Flags&userNoDump == 0 {
		t.Fatal("the nodump flag was not copied")
	}
}

func TestDeleteStopsWhenCancelledAndNeverLeavesTheTree(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	tree := filepath.Join(root, "tree")
	for _, dir := range []string{outside, filepath.Join(tree, "nested")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(keep, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "nested", "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tree, "nested", "escape")); err != nil {
		t.Fatal(err)
	}
	service := unprotectedService()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.DeleteFilesOrFolders(cancelled, []string{tree}); !errors.Is(err, ErrFileExplorerCancelled) {
		t.Fatalf("cancelled delete returned %v; want %v", err, ErrFileExplorerCancelled)
	}
	if _, err := os.Stat(filepath.Join(tree, "nested", "file")); err != nil {
		t.Fatalf("a delete cancelled before it started removed something: %v", err)
	}

	if err := service.DeleteFilesOrFolders(t.Context(), []string{tree}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Fatal("the tree still exists")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("deleting the tree followed its symlink outside: %v", err)
	}
}

func TestDeleteNeverCrossesIntoAnotherFileSystem(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to mount")
	}
	root := t.TempDir()
	mountpoint := filepath.Join(root, "tree", "mounted")
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("mount", "-t", "tmpfs", "tmpfs", mountpoint).CombinedOutput(); err != nil {
		t.Skipf("cannot mount tmpfs: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("umount", "-f", mountpoint).Run() })
	inside := filepath.Join(mountpoint, "precious")
	if err := os.WriteFile(inside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := unprotectedService().DeleteFilesOrFolders(t.Context(), []string{filepath.Join(root, "tree")})
	var protected *FileExplorerProtectedError
	if !errors.As(err, &protected) || protected.Reason != FileExplorerProtectedMountPoint {
		t.Fatalf("delete returned %v; want a refusal at the mount point", err)
	}
	if data, err := os.ReadFile(inside); err != nil || string(data) != "keep" {
		t.Fatalf("a file on the mounted file system was deleted: %v", err)
	}
}
