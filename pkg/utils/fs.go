// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/h2non/filetype"
	"github.com/h2non/filetype/types"
)

func DeleteFileIfExists(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func CopyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	destinationFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destinationFile.Close()

	_, err = io.Copy(destinationFile, sourceFile)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	err = destinationFile.Sync()
	if err != nil {
		return fmt.Errorf("failed to sync destination file: %w", err)
	}

	return nil
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

func CreateOrResizeFile(path string, size int64) error {
	if !IsAbsPath(path) {
		return fmt.Errorf("path must be absolute: %s", path)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to open raw file: %w", err)
	}
	defer f.Close()

	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("failed_to_resize_raw_file: %w", err)
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

	var stderr bytes.Buffer
	c.Stderr = &stderr

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

	err = c.Wait()
	if err != nil {
		out := stderr.String()
		if strings.Contains(out, "trailing garbage ignored") {
			return nil
		}
		return fmt.Errorf("decompress failed (StreamToFile): %w (%s)", err, out)
	}

	return nil
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

// dd, not camdd: camdd can exit without writing anything when its writer thread
// starts after the reader has already read the whole image.
func FlashImageToDiskCtx(ctx context.Context, source, dest string) error {
	if source == "" || dest == "" {
		return fmt.Errorf("source and dest must be non-empty")
	}
	if source == dest {
		return fmt.Errorf("source and dest must not be the same path")
	}

	srcInfo, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat source %q: %w", source, err)
	}
	if srcInfo.IsDir() {
		return fmt.Errorf("source %q is a directory, expected a file", source)
	}

	if strings.HasPrefix(dest, "/dev/") {
		if _, err := os.Stat(dest); err != nil {
			return fmt.Errorf("destination device %q not found or not accessible: %w", dest, err)
		}
	} else {
		if info, err := os.Stat(dest); err == nil && info.IsDir() {
			return fmt.Errorf("destination %q is a directory, expected a file", dest)
		}
		if !filepath.IsAbs(dest) {
			if abs, err := filepath.Abs(dest); err == nil {
				dest = abs
			}
		}
	}

	// notrunc keeps a disk file at its size.
	output, err := exec.CommandContext(ctx, "/bin/dd", "if="+source, "of="+dest, "bs=1m", "conv=notrunc").CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("dd canceled or timed out: %w", ctx.Err())
		}
		return fmt.Errorf("dd failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()

	defer os.Remove(tmp)

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}

	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}

	if err := os.Rename(tmp, path); err != nil {
		return err
	}

	return nil
}

type writtenFile struct {
	size    int64
	modTime time.Time
	sum     [sha256.Size]byte
}

var writtenFiles sync.Map

func WriteFileIfChanged(path string, data []byte, perm os.FileMode) error {
	sum := sha256.Sum256(data)
	remember := func() {
		if info, err := os.Stat(path); err == nil {
			writtenFiles.Store(path, writtenFile{info.Size(), info.ModTime(), sum})
		}
	}
	if info, err := os.Stat(path); err == nil {
		// Reading to compare is itself a write on an atime=on dataset, so trust
		// the file we wrote last if it is still untouched.
		if last, ok := writtenFiles.Load(path); ok {
			w := last.(writtenFile)
			if w.sum == sum && w.size == info.Size() && w.modTime.Equal(info.ModTime()) {
				return nil
			}
		}
		if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
			remember()
			return nil
		}
	}
	if err := AtomicWriteFile(path, data, perm); err != nil {
		return err
	}
	remember()
	return nil
}

func ReadLastLines(path string, maxLines int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()

	const chunkSize = 4096

	var (
		offset    int64
		fileSize  int64
		lineCount int
		buffer    []byte
	)

	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	fileSize = info.Size()
	offset = fileSize

	for offset > 0 && lineCount <= maxLines {
		readSize := int64(chunkSize)
		if offset < readSize {
			readSize = offset
		}

		offset -= readSize

		chunk := make([]byte, readSize)
		_, err := f.ReadAt(chunk, offset)
		if err != nil && err != io.EOF {
			return "", err
		}

		buffer = append(chunk, buffer...)

		for i := len(chunk) - 1; i >= 0; i-- {
			if chunk[i] == '\n' {
				lineCount++
				if lineCount > maxLines {
					return string(buffer[i+1:]), nil
				}
			}
		}
	}

	return string(buffer), nil
}
