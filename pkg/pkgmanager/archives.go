// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package pkgmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/blake2s"
	"golang.org/x/sys/unix"
)

type candidateArchive struct {
	change   Change
	path     string
	checksum string
	size     int64
	local    bool
}

func (c *Client) resolveCandidateArchive(ctx context.Context, change Change, repository Repository) (candidateArchive, error) {
	result, err := c.Run(ctx, "search", "-U", "-r", repository.Name, "-e", "-S", "pkg-name", "-R", "--raw-format", "json", change.Name+"-"+change.To)
	if err != nil {
		return candidateArchive{}, err
	}
	if result.ExitCode != 0 {
		return candidateArchive{}, commandError(result)
	}
	var candidates []struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Path     string `json:"path"`
		RepoPath string `json:"repopath"`
		Sum      string `json:"sum"`
		PkgSize  int64  `json:"pkgsize"`
	}
	if err = json.Unmarshal([]byte(result.Stdout), &candidates); err != nil {
		return candidateArchive{}, fmt.Errorf("package_archive_path_unavailable: %s: invalid catalog JSON: %w", change.Name, err)
	}
	if len(candidates) != 1 {
		return candidateArchive{}, fmt.Errorf("package_archive_path_unavailable: %s: expected one candidate, got %d", change.Name, len(candidates))
	}
	candidate := candidates[0]
	if candidate.RepoPath == "" {
		candidate.RepoPath = candidate.Path
	}
	if candidate.Name != change.Name || candidate.Version != change.To {
		return candidateArchive{}, fmt.Errorf("package_archive_path_unavailable: %s: candidate name/version mismatch", change.Name)
	}
	if candidate.Path != "" && candidate.Path != candidate.RepoPath {
		return candidateArchive{}, fmt.Errorf("package_archive_path_unavailable: %s: conflicting path/repopath", change.Name)
	}
	relative := strings.TrimPrefix(candidate.RepoPath, "./")
	if !validInspectionPath(relative) {
		return candidateArchive{}, fmt.Errorf("package_archive_path_unavailable: %s: unsafe repository path %.512q", change.Name, candidate.RepoPath)
	}
	if _, _, _, err = archiveHash(candidate.Sum); err != nil || candidate.PkgSize <= 0 {
		return candidateArchive{}, fmt.Errorf("package_archive_path_unavailable: %s: invalid archive checksum or size", change.Name)
	}
	archive := candidateArchive{change: change, checksum: candidate.Sum, size: candidate.PkgSize}
	if strings.HasPrefix(repository.URL, "file:") {
		directory := path.Clean(strings.TrimPrefix(repository.URL, "file:"))
		if !strings.HasPrefix(repository.URL, "file:///") {
			return candidateArchive{}, fmt.Errorf("package_archive_path_unavailable: %s: unsupported file repository URL", change.Name)
		}
		archive.path, archive.local = path.Join(directory, relative), true
		return archive, nil
	}
	extension := ""
	if dot := strings.LastIndexByte(relative, '.'); dot >= 0 {
		extension = relative[dot:]
		if strings.Contains(extension, "/") {
			return candidateArchive{}, fmt.Errorf("package_archive_path_unavailable: %s: unsupported cache extension", change.Name)
		}
	}
	archive.path = path.Join("/var/cache/pkg", change.Name+"-"+change.To+"~"+candidate.Sum[:10]+extension)
	return archive, nil
}

func validInspectionPath(relative string) bool {
	return relative != "" && relative != "." && relative != ".." && !path.IsAbs(relative) &&
		path.Clean(relative) == relative && !strings.HasPrefix(relative, "../") && !strings.ContainsAny(relative, "\\\x00\r\n\t")
}

func (c *Client) validateArchivePath(archive string) error {
	if !path.IsAbs(archive) || !validInspectionPath(strings.TrimPrefix(archive, "/")) {
		return fmt.Errorf("package_archive_path_unavailable")
	}
	parts := strings.Split(strings.TrimPrefix(archive, "/"), "/")
	parent, err := c.openJailDirectory(parts[:len(parts)-1]...)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("package_archive_path_unavailable: %w", err)
	}
	defer parent.Close()
	var info unix.Stat_t
	err = unix.Fstatat(int(parent.Fd()), parts[len(parts)-1], &info, unix.AT_SYMLINK_NOFOLLOW)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("package_archive_path_unavailable: %s", archive)
	}
	return nil
}

func (c *Client) inspectCandidates(ctx context.Context, plan Plan, repos []Repository, inspect func(Change, string) error) error {
	groups := make(map[string][]candidateArchive)
	seenPaths := make(map[string]bool)
	var order []string
	for _, change := range plan.Changes {
		if change.Action == "remove" {
			continue
		}
		if change.To == "" || strings.ContainsAny(change.To, "/\\\x00\r\n\t \"'") {
			return fmt.Errorf("invalid_package_version")
		}
		repository := change.Repository
		if c.Scope == ScopeHost {
			var err error
			repository, err = c.hostChangeRepository(ctx, change, repos)
			if err != nil {
				return err
			}
			if strings.HasPrefix(change.Name, "FreeBSD-") && repository != c.HostSystem.Repository {
				return fmt.Errorf("host_base_repository_changed")
			}
		} else if repository == "" && len(repos) == 1 {
			repository = repos[0].Name
		}
		index := slices.IndexFunc(repos, func(repo Repository) bool { return repo.Name == repository })
		if index < 0 {
			return fmt.Errorf("package_repository_ambiguous: %s", change.Name)
		}
		if c.Scope == ScopeSystem && repository != c.System.Repository {
			return fmt.Errorf("package_system_repository_mixed")
		}
		candidate, err := c.resolveCandidateArchive(ctx, change, repos[index])
		if err == nil {
			err = c.validateArchivePath(candidate.path)
		}
		if err != nil {
			if c.Log != nil {
				_, _ = fmt.Fprintf(c.Log, "Archive path verification failed for %s: %s\n", change.Name, Redact(err.Error()))
			}
			return err
		}
		if seenPaths[candidate.path] {
			return fmt.Errorf("package_archive_path_unavailable: duplicate archive path in %s", repository)
		}
		seenPaths[candidate.path] = true
		if len(groups[repository]) == 0 {
			order = append(order, repository)
		}
		groups[repository] = append(groups[repository], candidate)
	}
	for _, repository := range order {
		candidates := groups[repository]
		var missing []candidateArchive
		for _, candidate := range candidates {
			cached, err := c.cachedArchive(ctx, candidate)
			if err != nil {
				return err
			}
			if !cached {
				missing = append(missing, candidate)
			}
		}
		if reused := len(candidates) - len(missing); reused > 0 && c.Log != nil {
			_, _ = fmt.Fprintf(c.Log, "Reusing %d verified package archives from %s.\n", reused, repository)
		}
		for batch := range slices.Chunk(missing, 128) {
			args := []string{"fetch", "-U", "-y", "-r", repository}
			for _, candidate := range batch {
				args = append(args, candidate.change.Name+"-"+candidate.change.To)
			}
			result, err := c.Run(ctx, args...)
			if err != nil {
				return err
			}
			if result.ExitCode != 0 {
				return commandError(result)
			}
			for _, candidate := range batch {
				verified, err := c.cachedArchive(ctx, candidate)
				if err != nil {
					return err
				}
				if !verified {
					return fmt.Errorf("package_archive_checksum_mismatch: %s", candidate.change.Name)
				}
			}
		}
		for _, candidate := range candidates {
			if err := c.validateArchivePath(candidate.path); err != nil {
				return err
			}
			if err := inspect(candidate.change, candidate.path); err != nil {
				return err
			}
		}
	}
	return nil
}

const pkgBase32Alphabet = "ybndrfg8ejkmcpqxot1uwisza345h769"

func archiveHash(sum string) (hash.Hash, string, bool, error) {
	kind, expected, encoded := strings.Cut(sum, "$")
	if !encoded {
		kind, expected = "1", sum
	}
	var h hash.Hash
	switch kind {
	case "0", "1":
		h = sha256.New()
	case "2":
		h, _ = blake2b.New512(nil)
	case "5":
		h, _ = blake2s.New256(nil)
	default:
		return nil, "", false, fmt.Errorf("package_archive_checksum_unsupported")
	}
	hexadecimal := kind == "1"
	length, alphabet := (h.Size()*8+4)/5, pkgBase32Alphabet
	if hexadecimal {
		length, alphabet = h.Size()*2, "0123456789abcdef"
	}
	if len(expected) != length || strings.Trim(expected, alphabet) != "" {
		return nil, "", false, fmt.Errorf("package_archive_checksum_invalid")
	}
	return h, expected, hexadecimal, nil
}

func pkgBase32(data []byte) string {
	var output strings.Builder
	var value uint32
	var bits uint
	for _, b := range data {
		value |= uint32(b) << bits
		bits += 8
		for bits >= 5 {
			output.WriteByte(pkgBase32Alphabet[value&31])
			value >>= 5
			bits -= 5
		}
	}
	if bits > 0 {
		output.WriteByte(pkgBase32Alphabet[value&31])
	}
	return output.String()
}

func (c *Client) cachedArchive(ctx context.Context, candidate candidateArchive) (bool, error) {
	parts := strings.Split(strings.TrimPrefix(candidate.path, "/"), "/")
	parent, err := c.openJailDirectory(parts[:len(parts)-1]...)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("package_archive_path_unavailable: %w", err)
	}
	defer parent.Close()
	name := parts[len(parts)-1]
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("package_archive_path_unavailable: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false, fmt.Errorf("package_archive_path_unavailable: %s", candidate.change.Name)
	}
	if info.Size() < candidate.size {
		return false, nil
	}
	valid := false
	if info.Size() == candidate.size {
		h, expected, hexadecimal, err := archiveHash(candidate.checksum)
		if err != nil {
			return false, err
		}
		buffer := make([]byte, 128<<10)
		for {
			if err = ctx.Err(); err != nil {
				return false, err
			}
			n, readErr := file.Read(buffer)
			_, _ = h.Write(buffer[:n])
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return false, readErr
			}
		}
		actual := hex.EncodeToString(h.Sum(nil))
		if !hexadecimal {
			actual = pkgBase32(h.Sum(nil))
		}
		valid = actual == expected
	}
	if valid {
		return true, nil
	}
	if candidate.local {
		return false, fmt.Errorf("package_archive_checksum_mismatch: %s", candidate.change.Name)
	}
	if c.Log != nil {
		_, _ = fmt.Fprintf(c.Log, "Discarding corrupt cached archive for %s; downloading it again.\n", candidate.change.Name)
	}
	return false, unix.Unlinkat(int(parent.Fd()), name, 0)
}
