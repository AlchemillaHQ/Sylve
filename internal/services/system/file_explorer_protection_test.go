// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package system

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"golang.org/x/sys/unix"
)

func TestFileExplorerProtectionReasons(t *testing.T) {
	protection := newFileExplorerProtection([]fileExplorerMount{
		{Path: "/", Source: "zroot/ROOT/default", Type: "zfs"},
		{Path: "/tmp", Source: "zroot/tmp", Type: "zfs"},
		{Path: "/var/log", Source: "zroot/var/log", Type: "zfs"},
		{Path: "/usr/home", Source: "zroot/usr/home", Type: "zfs"},
		{Path: "/zroot", Source: "zroot", Type: "zfs"},
		{Path: "/zroot/sylve", Source: "zroot/sylve", Type: "zfs"},
		{Path: "/zroot/sylve/jails/121", Source: "zroot/sylve/jails/121", Type: "zfs"},
		{Path: "/zroot/sylve/jails/121/dev", Source: "devfs", Type: "devfs"},
		{Path: "/zroot/sylve/virtual-machines/100", Source: "zroot/sylve/virtual-machines/100", Type: "zfs"},
		{Path: "/zroot/data/media", Source: "zroot/data/media", Type: "zfs"},
		{Path: "/mnt/backup/disk", Source: "/dev/da0p1", Type: "ufs"},
	}, "/var/db/sylve")

	for path, want := range map[string]string{
		"/":                                      FileExplorerProtectedRoot,
		"/etc":                                   FileExplorerProtectedSystemFolder,
		"/etc/rc.conf":                           "",
		"/usr":                                   FileExplorerProtectedSystemFolder,
		"/usr/bin/ls":                            FileExplorerProtectedBaseSystem,
		"/usr/local":                             FileExplorerProtectedBaseSystem,
		"/usr/local/etc/nginx.conf":              "",
		"/usr/home":                              FileExplorerProtectedMountPoint,
		"/usr/home/alice/notes.txt":              "",
		"/boot/loader.conf":                      FileExplorerProtectedBaseSystem,
		"/tmp":                                   FileExplorerProtectedMountPoint,
		"/tmp/scratch":                           "",
		"/var/log":                               FileExplorerProtectedMountPoint,
		"/var/log/messages":                      "",
		"/var/db":                                FileExplorerProtectedContainsSylve,
		"/var/db/sylve":                          FileExplorerProtectedSylveData,
		"/var/db/sylve/sylve.db":                 FileExplorerProtectedSylveData,
		"/var/db/pkg":                            "",
		"/zroot":                                 FileExplorerProtectedMountPoint,
		"/zroot/sylve":                           FileExplorerProtectedMountPoint,
		"/zroot/sylve/virtual-machines":          FileExplorerProtectedSylveData,
		"/zroot/sylve/virtual-machines/100/disk": FileExplorerProtectedSylveData,
		"/zroot/sylve/jails/121":                 FileExplorerProtectedJailRoot,
		"/zroot/sylve/jails/121/etc/rc.conf":     "",
		"/zroot/sylve/jails/121/.sylve":          FileExplorerProtectedSylveData,
		"/zroot/sylve/jails/121/.sylve/jail.json": FileExplorerProtectedSylveData,
		"/zroot/sylve/jails/121/dev":              FileExplorerProtectedMountPoint,
		"/zroot/data":                             FileExplorerProtectedContainsMount,
		"/zroot/data/media":                       FileExplorerProtectedMountPoint,
		"/zroot/data/media/film.mkv":              "",
		"/zroot/notes.txt":                        "",
		"/mnt":                                    FileExplorerProtectedSystemFolder,
		"/mnt/backup":                             FileExplorerProtectedContainsMount,
		"/mnt/backup/disk/archive.tar":            "",
	} {
		if got := protection.reason(path); got != want {
			t.Errorf("reason(%q) = %q; want %q", path, got, want)
		}
	}
	if got := protection.mountSource("/zroot/data/media"); got != "zroot/data/media" {
		t.Errorf("mountSource = %q; want zroot/data/media", got)
	}
}

func protectedTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"mounted/inner", "data", "plain"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"mounted/inner/keep.txt", "data/sylve.db", "plain/free.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	protection := newFileExplorerProtection(
		[]fileExplorerMount{{Path: filepath.Join(root, "mounted", "inner"), Source: "tank/inner", Type: "zfs"}},
		filepath.Join(root, "data"),
	)
	return &Service{fileExplorerProtectionLoader: func() (*fileExplorerProtection, error) { return protection, nil }}, root
}

func requireProtected(t *testing.T, err error, reason string) {
	t.Helper()
	var protected *FileExplorerProtectedError
	if !errors.As(err, &protected) || !errors.Is(err, ErrFileExplorerProtectedPath) || protected.Reason != reason {
		t.Fatalf("error = %v; want a protected-path refusal for %s", err, reason)
	}
}

func TestFileExplorerMutationsRefuseProtectedPaths(t *testing.T) {
	service, root := protectedTestService(t)
	free := filepath.Join(root, "plain", "free.txt")

	err := service.DeleteFilesOrFolders(t.Context(), []string{free, filepath.Join(root, "mounted")})
	requireProtected(t, err, FileExplorerProtectedContainsMount)
	if _, err := os.Stat(free); err != nil {
		t.Fatalf("a refused batch deleted an unprotected item: %v", err)
	}
	requireProtected(t, service.DeleteFilesOrFolders(t.Context(), []string{filepath.Join(root, "data", "sylve.db")}), FileExplorerProtectedSylveData)
	requireProtected(t, service.RenameFileOrFolder(filepath.Join(root, "mounted", "inner"), "renamed"), FileExplorerProtectedMountPoint)
	requireProtected(t, service.CopyOrMoveFilesOrFolders(t.Context(),
		[]systemServiceInterfaces.FileTransferItem{{Source: filepath.Join(root, "data"), Destination: filepath.Join(root, "plain")}},
		true,
	), FileExplorerProtectedSylveData)
	for _, path := range []string{"mounted/inner/keep.txt", "data/sylve.db"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("%s changed after a refusal: %v", path, err)
		}
	}

	if err := service.CopyOrMoveFilesOrFolders(t.Context(),
		[]systemServiceInterfaces.FileTransferItem{{Source: filepath.Join(root, "data", "sylve.db"), Destination: filepath.Join(root, "plain")}},
		false,
	); err != nil {
		t.Fatalf("copying out of a protected folder failed: %v", err)
	}
	if err := service.DeleteFilesOrFolders(t.Context(), []string{filepath.Join(root, "mounted", "inner", "keep.txt"), free}); err != nil {
		t.Fatalf("deleting unprotected items failed: %v", err)
	}

	link := filepath.Join(root, "plain", "into-data")
	if err := os.Symlink(filepath.Join(root, "data"), link); err != nil {
		t.Fatal(err)
	}
	requireProtected(t, service.DeleteFilesOrFolders(t.Context(), []string{filepath.Join(link, "sylve.db")}), FileExplorerProtectedSylveData)
	if err := service.DeleteFilesOrFolders(t.Context(), []string{link}); err != nil {
		t.Fatalf("deleting a symlink that points at protected data failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "sylve.db")); err != nil {
		t.Fatalf("deleting the symlink removed its target: %v", err)
	}
}

func TestTraverseReportsLinksSpecialFilesOwnersAndProtection(t *testing.T) {
	service, root := protectedTestService(t)
	plain := filepath.Join(root, "plain")
	if err := os.Symlink(filepath.Join(root, "mounted"), filepath.Join(plain, "folder-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(plain, "broken-link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(plain, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(plain, "free.txt"), 0o640); err != nil {
		t.Fatal(err)
	}

	nodes, err := service.Traverse(plain)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]systemServiceInterfaces.FileNode{}
	for _, node := range nodes {
		byName[filepath.Base(node.ID)] = node
	}
	if node := byName["folder-link"]; node.Type != "folder" || node.Link != filepath.Join(root, "mounted") || node.Broken {
		t.Errorf("folder link = %+v; want an openable folder with its target", node)
	}
	if node := byName["broken-link"]; node.Type != "file" || node.Link != "missing" || !node.Broken {
		t.Errorf("broken link = %+v; want a broken file link", node)
	}
	if node := byName["pipe"]; node.Special != "fifo" || node.Size != 0 {
		t.Errorf("fifo = %+v; want special fifo without a size", node)
	}
	if node := byName["free.txt"]; node.Mode != "0640" || node.Owner == "" || node.Group == "" || node.Size != int64(len("plain/free.txt")) {
		t.Errorf("file = %+v; want mode 0640, an owner, a group and its size", node)
	}

	nodes, err = service.Traverse(root)
	if err != nil {
		t.Fatal(err)
	}
	protected := map[string]string{}
	for _, node := range nodes {
		protected[filepath.Base(node.ID)] = node.Protected
	}
	if protected["mounted"] != FileExplorerProtectedContainsMount || protected["data"] != FileExplorerProtectedSylveData || protected["plain"] != "" {
		t.Errorf("protection = %v; want mounted and data protected, plain free", protected)
	}
	inner, err := service.Traverse(filepath.Join(root, "mounted"))
	if err != nil || len(inner) != 1 || inner[0].Mount != "tank/inner" || inner[0].Protected != FileExplorerProtectedMountPoint {
		t.Errorf("mount point = %+v (%v); want it marked with its file system", inner, err)
	}
}

func TestMoveKeepsCompleteCopyWhenSourceCannotBeRemoved(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to set file flags")
	}
	const systemImmutable = 0x00020000
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a-free", "z-stuck"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stuck := filepath.Join(source, "z-stuck")
	if err := unix.Chflags(stuck, systemImmutable); err != nil {
		t.Skipf("cannot set the system-immutable flag: %v", err)
	}
	copied := filepath.Join(target, "z-stuck")
	t.Cleanup(func() {
		_ = unix.Chflags(stuck, 0)
		_ = unix.Chflags(copied, 0)
	})

	service := &Service{
		fileExplorerRename: func(oldPath, newPath string) error {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
		},
		fileExplorerProtectionLoader: func() (*fileExplorerProtection, error) {
			return newFileExplorerProtection(nil, ""), nil
		},
	}
	err := service.CopyOrMoveFilesOrFolders(t.Context(),
		[]systemServiceInterfaces.FileTransferItem{{Source: source, Destination: target}},
		true,
	)
	if !errors.Is(err, ErrFileExplorerMoveIncomplete) {
		t.Fatalf("error = %v; want %v", err, ErrFileExplorerMoveIncomplete)
	}
	for _, name := range []string{"a-free", "z-stuck"} {
		if contents, err := os.ReadFile(filepath.Join(target, name)); err != nil || string(contents) != name {
			t.Fatalf("the moved copy of %s is missing or wrong (%q, %v)", name, contents, err)
		}
	}
	if _, err := os.Stat(stuck); err != nil {
		t.Fatalf("the source file that could not be removed is gone: %v", err)
	}
	if info, err := os.Lstat(copied); err != nil || info.Sys().(*syscall.Stat_t).Flags&systemImmutable == 0 {
		t.Fatalf("the moved copy did not keep the immutable flag: %v", err)
	}
}

func TestCopyPreservesOwnerModeAndTimes(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to change owners")
	}
	root := t.TempDir()
	source := filepath.Join(root, "tree")
	file := filepath.Join(source, "tool")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Lchown(file, 1234, 5678); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o750|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	past := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, path := range []string{file, source} {
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatal(err)
		}
	}

	target := filepath.Join(root, "copy")
	if err := (&Service{}).CopyOrMoveFilesOrFolders(t.Context(),
		[]systemServiceInterfaces.FileTransferItem{{Source: source, Destination: target}},
		false,
	); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(target, "tool"))
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Uid != 1234 || stat.Gid != 5678 || stat.Mode&0o7777 != 0o4750 || !info.ModTime().Equal(past) {
		t.Fatalf("copied file uid=%d gid=%d mode=%04o mtime=%s; want 1234 5678 4750 %s", stat.Uid, stat.Gid, stat.Mode&0o7777, info.ModTime(), past)
	}
	if directory, err := os.Stat(target); err != nil || directory.Mode().Perm() != 0o750 || !directory.ModTime().Equal(past) {
		t.Fatalf("copied folder = %v (%v); want mode 0750 and the original time", directory, err)
	}
}

func TestCopyRegularFileRefusesFifoWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(fifo)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- copyFileExplorerRegularFile(t.Context(), fifo, filepath.Join(root, "copy"), info) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrFileExplorerUnsupportedType) {
			t.Fatalf("error = %v; want %v", err, ErrFileExplorerUnsupportedType)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copying a FIFO blocked")
	}
}

func TestValidateFileExplorerName(t *testing.T) {
	for _, name := range []string{"notes.txt", "a:b", "résumé.pdf", ".hidden", strings.Repeat("a", 255)} {
		if err := ValidateFileExplorerName(name); err != nil {
			t.Errorf("ValidateFileExplorerName(%q) = %v; want nil", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", "tab\there", "nul\x00", "bad\xff", strings.Repeat("a", 256)} {
		if err := ValidateFileExplorerName(name); !errors.Is(err, ErrFileExplorerInvalidName) {
			t.Errorf("ValidateFileExplorerName(%q) = %v; want %v", name, err, ErrFileExplorerInvalidName)
		}
	}
}
