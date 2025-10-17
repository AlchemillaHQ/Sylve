// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/h2non/filetype"
	"github.com/h2non/filetype/types"
)

func DeleteFile(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("stat %q: %w", path, err)
	}

	if info.IsDir() {
		return fmt.Errorf("%q is a directory, not a file", path)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %q: %w", path, err)
	}

	return nil
}

func CopyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed_to_open_source: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed_to_create_dest: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed_to_copy_file: %w", err)
	}

	return nil
}

func FindFileInDirectoryByPrefix(dir, prefix string) (string, error) {
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if len(d.Name()) >= len(prefix) && d.Name()[:len(prefix)] == prefix {
			return fmt.Errorf("FOUND:%s", path)
		}

		return nil
	})

	if err != nil && len(err.Error()) > 6 && err.Error()[:6] == "FOUND:" {
		return err.Error()[6:], nil
	}

	if err != nil {
		return "", fmt.Errorf("walk_error: %w", err)
	}

	return "", fmt.Errorf("file_with_prefix_not_found: %s in %s", prefix, dir)
}

func IsAbsPath(path string) bool {
	return len(path) > 0 && os.IsPathSeparator(path[0])
}

func CreateOrTruncateFile(path string, size int64) error {
	if !IsAbsPath(path) {
		return fmt.Errorf("path must be absolute: %s", path)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("failed to truncate file: %w", err)
	}

	return nil
}

func FileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("stat %q: %w", path, err)
	}

	if info.IsDir() {
		return false, fmt.Errorf("%q is a directory, not a file", path)
	}

	return true, nil
}

func ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)

	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %w", path, err)
	}

	return data, nil
}

func IsEmptyDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, fmt.Errorf("directory %q does not exist", path)
	}

	if err != nil {
		return false, fmt.Errorf("stat %q: %w", path, err)
	}

	if !info.IsDir() {
		return false, fmt.Errorf("%q is not a directory", path)
	}

	files, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("failed to read directory %q: %w", path, err)
	}

	return len(files) == 0, nil
}

func IsDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("stat %q: %w", path, err)
	}

	return info.IsDir(), nil
}

func CopyDirContents(source, destination string) error {
	if _, err := os.Stat(destination); os.IsNotExist(err) {
		if err := os.MkdirAll(destination, 0755); err != nil {
			return fmt.Errorf("failed to create destination dir: %w", err)
		}
	}

	_, err := RunCommand("cp", "-a", source+"/.", destination)
	return err
}

func RemoveDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed_to_list_dir: %w", err)
	}
	for _, entry := range entries {
		err = os.RemoveAll(filepath.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("failed_to_remove %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func DoesPathHaveBase(root string) (bool, error) {
	if root == "" {
		return false, fmt.Errorf("path_required")
	}

	info, err := os.Stat(root)

	if err != nil {
		if os.IsNotExist(err) {
			return false, fmt.Errorf("path_does_not_exist: %s", root)
		}
		return false, err
	}

	if !info.IsDir() {
		return false, fmt.Errorf("not_a_directory: %s", root)
	}

	required := []string{
		"bin/freebsd-version",
		"bin/sh",
		"libexec/ld-elf.so.1",
		"lib/libc.so.7",
	}

	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			return false, nil
		}
	}

	return true, nil
}

func IsTarLike(path string, mime string) bool {
	runHead := func(cmd string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		c := exec.CommandContext(ctx, cmd, args...)
		c.Stderr = io.Discard

		stdout, err := c.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := c.Start(); err != nil {
			return nil, err
		}

		buf := make([]byte, 512)
		n, readErr := io.ReadFull(stdout, buf)
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			_ = c.Process.Kill()
			_ = c.Wait()
			return nil, readErr
		}

		_ = stdout.Close()
		_ = c.Process.Kill()
		_ = c.Wait()

		return buf[:n], nil
	}

	var hdr []byte
	var err error

	switch mime {
	case "application/x-tar":
		// Uncompressed tar: read first 512 directly
		f, e := os.Open(path)
		if e != nil {
			return false
		}
		defer f.Close()
		hdr = make([]byte, 512)
		n, e := io.ReadFull(f, hdr)
		if e != nil || n < 512 {
			return false
		}

	case "application/gzip":
		// gzip -> gunzip header only
		hdr, err = runHead("gzip", "-dc", path)
		if err != nil || len(hdr) < 512 {
			return false
		}

	case "application/x-bzip2":
		hdr, err = runHead("bzip2", "-dc", path)
		if err != nil || len(hdr) < 512 {
			return false
		}

	case "application/x-xz":
		hdr, err = runHead("xz", "-dc", path)
		if err != nil || len(hdr) < 512 {
			return false
		}

	case "application/zstd":
		// bsdtar/libarchive recognizes zstd as “zstd”; many file detectors use application/zstd
		hdr, err = runHead("zstd", "-dc", path)
		if err != nil || len(hdr) < 512 {
			return false
		}

	case "application/x-compress":
		// legacy .Z
		hdr, err = runHead("uncompress", "-c", path)
		if err != nil || len(hdr) < 512 {
			return false
		}

	default:
		// Unknown/other: not a tar we can assert
		return false
	}

	// TAR magic at bytes 257..262
	if len(hdr) >= 263 {
		m := string(hdr[257:263]) // "ustar\000" or "ustar "
		return m == "ustar\x00" || m == "ustar "
	}
	return false
}

func StreamToFile(cmd []string, outPath string) error {
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stderr = os.Stderr

	stdout, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		return err
	}

	f, err := os.Create(outPath)
	if err != nil {
		_ = c.Process.Kill()
		_ = c.Wait()
		return err
	}
	defer f.Close()

	buf := make([]byte, 512*1024)
	if _, err := io.CopyBuffer(f, stdout, buf); err != nil {
		_ = c.Process.Kill()
		_ = c.Wait()
		return err
	}
	return c.Wait()
}

func SniffMIME(path string) (string, types.Type, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", filetype.Unknown, err
	}
	defer f.Close()

	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	kind, _ := filetype.Match(head[:n])

	if kind == filetype.Unknown {
		return "", kind, fmt.Errorf("unknown format")
	}

	return kind.MIME.Value, kind, nil
}

func ResetDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		_, _ = RunCommand("chflags", "-R", "noschg", dir)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return os.MkdirAll(dir, 0o755)
}

func DecompressOne(mime, src, out string) error {
	var cmd []string

	switch mime {
	case "application/gzip":
		if HasCmd("pigz") {
			cmd = []string{"pigz", "-dc", src}
		} else {
			cmd = []string{"gzip", "-dc", src}
		}
	case "application/x-bzip2":
		if HasCmd("pbzip2") {
			cmd = []string{"pbzip2", "-dc", src}
		} else {
			cmd = []string{"bzip2", "-dc", src}
		}
	case "application/x-xz":
		cmd = []string{"xz", "-T0", "-dc", src}
	case "application/zstd":
		cmd = []string{"zstd", "-T0", "-dc", src}
	case "application/x-compress":
		cmd = []string{"uncompress", "-c", src}
	default:
		return fmt.Errorf("unsupported compressed mime: %s", mime)
	}

	return StreamToFile(cmd, out)
}

func HasCmd(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
