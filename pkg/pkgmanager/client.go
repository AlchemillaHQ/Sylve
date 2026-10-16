// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type Target struct {
	JID     int    `json:"jid"`
	Name    string `json:"name"`
	Root    string `json:"root"`
	Dataset string `json:"dataset"`
	GUID    string `json:"guid"`
	Offline bool   `json:"offline"`
}

func (t Target) validate() error {
	if t.Name == "" || !filepath.IsAbs(t.Root) || filepath.Clean(t.Root) == "/" {
		return fmt.Errorf("invalid_package_target")
	}
	if t.Offline {
		if t.JID != 0 || t.Dataset == "" || t.GUID == "" || filepath.Clean(t.Root) != t.Root {
			return fmt.Errorf("invalid_package_target")
		}
	} else if t.JID <= 0 {
		return fmt.Errorf("invalid_package_target")
	}
	return nil
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type Runner func(context.Context, []string) (Result, error)

type Client struct {
	Binary        string
	Target        Target
	Log           io.Writer
	RunCommand    Runner
	Scope         Scope
	System        *SystemInfo
	HostOSVersion func() (uint32, error)
	Guard         *os.File
	ManagedPaths  []string
}

type Repository struct {
	Name    string
	URL     string
	Enabled bool
}

func Environment() []string {
	return []string{"PATH=/sbin:/bin:/usr/sbin:/usr/bin:/usr/local/sbin:/usr/local/bin", "HOME=/root", "LC_ALL=C", "TERM=dumb"}
}

func (c *Client) args(args ...string) ([]string, error) {
	if err := c.Target.validate(); err != nil {
		return nil, err
	}
	selector, target := "-j", strconv.Itoa(c.Target.JID)
	if c.Target.Offline {
		if !offlineCommand(args) {
			return nil, fmt.Errorf("package_offline_command_unsupported")
		}
		selector, target = "-c", c.Target.Root
	}
	base := []string{
		selector, target,
		"-o", "PKG_DBDIR=/var/db/pkg", "-o", "PKG_CACHEDIR=/var/cache/pkg",
		"-o", "IGNORE_OSVERSION=false", "-o", "IGNORE_OSMAJOR=false",
		"-o", "PKG_ENABLE_PLUGINS=false", "-o", "ASSUME_ALWAYS_YES=false",
		"-o", "DEFAULT_ALWAYS_YES=false", "-o", "REPO_AUTOUPDATE=false",
		"-o", "WARN_SIZE_LIMIT=0", "-o", "AUDIT_IGNORE_GLOB=[]", "-o", "AUDIT_IGNORE_REGEX=[]",
		"-o", "ALIAS={}", "-o", "PKG_ENV={}", "-o", "SAT_SOLVER=", "-o", "CUDF_SOLVER=",
		"-o", "HANDLE_RC_SCRIPTS=false",
	}
	if c.Target.Offline && !(len(args) == 1 && args[0] == "-vv") {
		base = append(base, "-o", "RUN_SCRIPTS=false", "-o", "PKG_TRIGGERS_ENABLE=false",
			"-o", "VALID_URL_SCHEME=pkg+http,pkg+https,https,http,file")
	}
	return append(base, args...), nil
}

func offlineCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "-N", "-vv":
		return len(args) == 1
	case "query", "rquery", "search", "audit", "update", "fetch":
		return true
	case "upgrade":
		return len(args) >= 3 && args[1] == "-U" && args[2] == "-n"
	}
	return false
}

type boundedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	n := len(data)
	left := b.limit - b.Len()
	if left < len(data) {
		b.truncated = true
		data = data[:left]
	}
	_, err := b.Buffer.Write(data)
	return n, err
}

func (c *Client) Run(ctx context.Context, args ...string) (result Result, err error) {
	argv, err := c.args(args...)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if c.Log == nil {
			return
		}
		output := result.Stderr
		includeOutput := err != nil || result.ExitCode != 0
		if len(args) > 0 {
			switch args[0] {
			case "audit", "update", "upgrade", "fetch":
				includeOutput = true
			case "-vv":
				includeOutput = false
			}
		}
		if includeOutput {
			output = result.Stdout + output
		}
		_, _ = io.WriteString(c.Log, Redact(output))
	}()
	if c.RunCommand != nil {
		return c.RunCommand(ctx, argv)
	}
	if c.Target.Offline {
		if err := ValidateTarget(ctx, c.Target); err != nil {
			return Result{}, err
		}
	}
	if c.Binary == "" {
		c.Binary = DefaultBinary
	}
	cmd := exec.CommandContext(ctx, c.Binary, argv...)
	cmd.Env = Environment()
	stdout := &boundedBuffer{limit: 16 << 20}
	stderr := &boundedBuffer{limit: 64 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	result = Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if stdout.truncated || stderr.truncated {
		return result, fmt.Errorf("package_output_truncated")
	}
	if err != nil && ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return result, fmt.Errorf("package_command_unavailable: %w", err)
	}
	return result, nil
}

func commandError(result Result) error {
	text := strings.TrimSpace(Redact(result.Stderr))
	if len(text) > 2048 {
		text = text[:2048]
	}
	return fmt.Errorf("package_command_failed: exit=%d %s", result.ExitCode, text)
}

var credentials = regexp.MustCompile(`(?i)(https?|pkg\+https?|ssh)://[^/\s@]+@`)
var secretQuery = regexp.MustCompile(`(?i)([?&](?:access_token|token|apikey|api_key|key|password|secret)=)[^&\s]+`)

func Redact(text string) string {
	return secretQuery.ReplaceAllString(credentials.ReplaceAllString(text, "$1://[redacted]@"), "${1}[redacted]")
}

func Probe(binary string) error {
	if binary == "" {
		binary = DefaultBinary
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("pkg_static_not_found")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("pkg_static_not_trusted")
	}
	cmd := exec.Command(binary, "-v")
	cmd.Env = Environment()
	out, err := cmd.Output()
	version := strings.TrimSpace(string(out))
	if err != nil || version != SupportedVersion {
		return fmt.Errorf("pkg_version_unsupported: tested version is %s", SupportedVersion)
	}
	return nil
}

func (c *Client) Inventory(ctx context.Context) ([]Package, error) {
	result, err := c.Run(ctx, "-N")
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("package_database_unavailable")
	}
	result, err = c.Run(ctx, "query", "-a", InventoryFormat)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, commandError(result)
	}
	return ParseInventory(result.Stdout)
}

func (c *Client) Audit(ctx context.Context, inventory []Package, fetch bool) (Audit, error) {
	args := []string{"audit", "--raw=json"}
	if fetch {
		args = append(args, "-F")
	}
	result, err := c.Run(ctx, args...)
	if err != nil {
		return Audit{}, err
	}
	output := result.Stdout
	if index := strings.Index(output, "{"); index >= 0 {
		output = output[index:]
	}
	report, err := ParseAudit(output, result.ExitCode, inventory)
	if strings.TrimSpace(result.Stderr) != "" {
		err = fmt.Errorf("audit_diagnostics_present")
	}
	if err != nil {
		diagnostic := strings.TrimSpace(result.Stderr)
		if diagnostic == "" {
			diagnostic = err.Error()
		}
		if len(diagnostic) > 2048 {
			diagnostic = diagnostic[:2048]
		}
		return Audit{}, fmt.Errorf("package_audit_failed: %s", Redact(diagnostic))
	}
	after, err := c.Inventory(ctx)
	if err != nil {
		return Audit{}, err
	}
	if AuditInventoryDigest(after) != AuditInventoryDigest(inventory) {
		return Audit{}, fmt.Errorf("package_inventory_changed_during_audit")
	}
	directory, err := c.openJailDirectory("var", "db", "pkg")
	if err != nil {
		return Audit{}, err
	}
	defer directory.Close()
	fd, err := unix.Openat(int(directory.Fd()), "vuln.xml", unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return Audit{}, fmt.Errorf("package_audit_database_unavailable: %w", err)
	}
	feed := os.NewFile(uintptr(fd), "audit-feed")
	info, err := feed.Stat()
	_ = feed.Close()
	if err != nil || !info.Mode().IsRegular() {
		return Audit{}, fmt.Errorf("package_audit_database_unavailable")
	}
	timestamp := info.ModTime().UTC()
	report.DatabaseAt = &timestamp
	return report, nil
}

func (c *Client) CatalogDigest(ctx context.Context, repos []Repository) (string, error) {
	names := []string{}
	for _, repo := range repos {
		if !ValidName(repo.Name) {
			return "", fmt.Errorf("invalid_package_repository")
		}
		names = append(names, repo.Name)
	}
	sort.Strings(names)
	hash := sha256.New()
	directory, err := c.openJailDirectory("var", "db", "pkg", "repos")
	if err != nil {
		return "", err
	}
	defer directory.Close()
	for _, name := range names {
		for _, suffix := range []string{"", "-wal"} {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			repofd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
			if err != nil {
				return "", err
			}
			file := "db" + suffix
			fd, err := unix.Openat(repofd, file, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
			_ = unix.Close(repofd)
			if os.IsNotExist(err) && suffix != "" {
				continue
			}
			if err != nil {
				return "", fmt.Errorf("package_catalog_unavailable: %w", err)
			}
			reader := os.NewFile(uintptr(fd), file)
			info, err := reader.Stat()
			if err != nil || !info.Mode().IsRegular() {
				reader.Close()
				return "", fmt.Errorf("invalid_package_catalog")
			}
			_, _ = io.WriteString(hash, name+suffix+"\x00")
			_, err = io.Copy(hash, reader)
			_ = reader.Close()
			if err != nil {
				return "", err
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (c *Client) CatalogsPresent(repos []Repository) (bool, error) {
	for _, repo := range repos {
		if !ValidName(repo.Name) {
			return false, fmt.Errorf("invalid_package_repository")
		}
		directory, err := c.openJailDirectory("var", "db", "pkg", "repos", repo.Name)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		var info unix.Stat_t
		err = unix.Fstatat(int(directory.Fd()), "db", &info, unix.AT_SYMLINK_NOFOLLOW)
		_ = directory.Close()
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG {
			return false, fmt.Errorf("invalid_package_catalog")
		}
		if info.Size == 0 {
			return false, nil
		}
	}
	return len(repos) > 0, nil
}

func (c *Client) openJailDirectory(parts ...string) (*os.File, error) {
	if err := c.Target.validate(); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Clean(c.Target.Root), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "package-db-directory"), nil
}

func validInspectionPath(relative string) bool {
	return relative != "" && relative != "." && relative != ".." && !path.IsAbs(relative) &&
		path.Clean(relative) == relative && !strings.HasPrefix(relative, "../") && !strings.ContainsAny(relative, "\\\x00\r\n\t")
}

func (c *Client) validateInspectionArchive(relative string) error {
	if !validInspectionPath(relative) {
		return fmt.Errorf("package_archive_path_unavailable")
	}
	parts := append([]string{"var", "cache", "pkg", "sylve-inspect"}, strings.Split(relative, "/")...)
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
		return fmt.Errorf("package_archive_path_unavailable: %s", relative)
	}
	return nil
}

func (c *Client) removeInspectionArchive(relative string) {
	if !validInspectionPath(relative) {
		return
	}
	parts := append([]string{"var", "cache", "pkg", "sylve-inspect"}, strings.Split(relative, "/")...)
	flags := 0
	for len(parts) > 3 {
		parent, err := c.openJailDirectory(parts[:len(parts)-1]...)
		if err != nil {
			return
		}
		err = unix.Unlinkat(int(parent.Fd()), parts[len(parts)-1], flags)
		_ = parent.Close()
		if err != nil {
			return
		}
		parts = parts[:len(parts)-1]
		flags = unix.AT_REMOVEDIR
	}
}

var repoStart = regexp.MustCompile(`^\s*([A-Za-z0-9_.-]+):\s*\{`)

func (c *Client) Repositories(ctx context.Context) ([]Repository, string, error) {
	result, err := c.Run(ctx, "-vv")
	if err != nil {
		return nil, "", err
	}
	if result.ExitCode != 0 {
		return nil, "", commandError(result)
	}
	repos := []Repository{}
	inRepos := false
	var current *Repository
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) == "Repositories:" {
			inRepos = true
			continue
		}
		if !inRepos {
			continue
		}
		if match := repoStart.FindStringSubmatch(line); match != nil {
			repos = append(repos, Repository{Name: match[1]})
			current = &repos[len(repos)-1]
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(strings.TrimSuffix(value, ","))
		switch strings.TrimSpace(key) {
		case "url":
			current.URL = strings.Trim(value, `"`)
		case "enabled":
			current.Enabled = strings.HasPrefix(value, "yes") || strings.HasPrefix(value, "true")
		}
	}
	scope, err := NormalizeScope(c.Scope)
	if err != nil {
		return nil, "", err
	}
	selected := []Repository{}
	for _, repo := range repos {
		if !repo.Enabled {
			continue
		}
		if scope == ScopeSystem {
			if c.System != nil && repo.Name == c.System.Repository {
				selected = append(selected, repo)
			}
		} else if !strings.Contains(strings.ToLower(repo.URL), "/base_") && !strings.Contains(strings.ToLower(repo.URL), "/kmods_") && !strings.HasPrefix(repo.Name, "FreeBSD-base") {
			selected = append(selected, repo)
		}
	}
	if scope == ScopeSystem && len(selected) == 0 && c.System != nil {
		for _, repo := range repos {
			if repo.Enabled && (strings.HasPrefix(repo.Name, "FreeBSD-base") || strings.Contains(strings.ToLower(repo.URL), "/base_")) {
				selected = append(selected, repo)
			}
		}
	}
	if scope == ScopeSystem && len(selected) > 1 {
		return nil, "", fmt.Errorf("package_system_repository_ambiguous")
	}
	if len(selected) == 0 {
		if scope == ScopeSystem {
			return nil, "", fmt.Errorf("package_system_repository_unavailable")
		}
		return nil, "", fmt.Errorf("application_repositories_unavailable")
	}
	if c.Target.Offline {
		for _, repo := range selected {
			parsed, err := url.Parse(repo.URL)
			if err != nil {
				return nil, "", fmt.Errorf("package_offline_repository_unsupported")
			}
			switch parsed.Scheme {
			case "http", "https", "pkg+http", "pkg+https", "file":
			default:
				return nil, "", fmt.Errorf("package_offline_repository_unsupported")
			}
		}
	}
	return selected, digest(result.Stdout), nil
}

func RepositoryArgs(repos []Repository) []string {
	args := []string{}
	for _, repo := range repos {
		args = append(args, "-r", repo.Name)
	}
	return args
}

func (c *Client) Refresh(ctx context.Context, repos []Repository) error {
	args := append([]string{"update"}, RepositoryArgs(repos)...)
	result, err := c.Run(ctx, args...)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return commandError(result)
	}
	return nil
}

func UpgradeArgs(repos []Repository, names []string, dry bool) []string {
	args := []string{"upgrade", "-U"}
	if dry {
		args = append(args, "-n")
	}
	args = append(args, RepositoryArgs(repos)...)
	args = append(args, names...)
	return args
}

func (c *Client) Preview(ctx context.Context, repos []Repository, names []string) (Plan, error) {
	if len(names) == 0 {
		return Plan{}, fmt.Errorf("no_eligible_packages")
	}
	for _, name := range names {
		if !ValidName(name) {
			return Plan{}, fmt.Errorf("invalid_package_name")
		}
	}
	if c.Scope == ScopeSystem {
		if err := c.validateSystemCatalog(ctx, repos, names); err != nil {
			return Plan{}, err
		}
	}
	result, err := c.Run(ctx, UpgradeArgs(repos, names, true)...)
	if err != nil {
		return Plan{}, err
	}
	if result.ExitCode != 0 {
		return Plan{}, commandError(result)
	}
	return ParsePlan(result.Stdout)
}

func (c *Client) candidateArchivePath(ctx context.Context, change Change, repository string) (string, error) {
	result, err := c.Run(ctx, "search", "-U", "-r", repository, "-e", "-S", "pkg-name", "-R", "--raw-format", "json", change.Name+"-"+change.To)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", commandError(result)
	}
	var candidates []struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Path     string `json:"path"`
		RepoPath string `json:"repopath"`
	}
	if err = json.Unmarshal([]byte(result.Stdout), &candidates); err != nil {
		return "", fmt.Errorf("package_archive_path_unavailable: %s: invalid catalog JSON: %w", change.Name, err)
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("package_archive_path_unavailable: %s: expected one candidate, got %d", change.Name, len(candidates))
	}
	candidate := candidates[0]
	if candidate.RepoPath == "" {
		candidate.RepoPath = candidate.Path
	}
	if candidate.Name != change.Name || candidate.Version != change.To {
		return "", fmt.Errorf("package_archive_path_unavailable: %s: candidate name/version mismatch", change.Name)
	}
	if candidate.Path != "" && candidate.Path != candidate.RepoPath {
		return "", fmt.Errorf("package_archive_path_unavailable: %s: conflicting path/repopath", change.Name)
	}
	relative := strings.TrimPrefix(candidate.RepoPath, "./")
	if !validInspectionPath(relative) {
		return "", fmt.Errorf("package_archive_path_unavailable: %s: unsafe repository path %.512q", change.Name, candidate.RepoPath)
	}
	return relative, nil
}

func (c *Client) VerifyCandidates(ctx context.Context, plan Plan, repos []Repository) error {
	if err := c.validateScopedChanges(plan); err != nil {
		return err
	}
	scope, _ := NormalizeScope(c.Scope)
	var files []string
	for _, change := range plan.Changes {
		if change.Action == "remove" {
			continue
		}
		repository := change.Repository
		if repository == "" && len(repos) == 1 {
			repository = repos[0].Name
		}
		if !slices.ContainsFunc(repos, func(repo Repository) bool { return repo.Name == repository }) {
			return fmt.Errorf("package_repository_ambiguous: %s", change.Name)
		}
		if scope == ScopeSystem && repository != c.System.Repository {
			return fmt.Errorf("package_system_repository_mixed")
		}
		if change.To == "" || strings.ContainsAny(change.To, "/\\\x00\r\n\t ") {
			return fmt.Errorf("invalid_package_version")
		}
		relative, err := c.candidateArchivePath(ctx, change, repository)
		if err == nil {
			err = c.validateInspectionArchive(relative)
		}
		if err != nil {
			if c.Log != nil {
				_, _ = fmt.Fprintf(c.Log, "Archive path verification failed for %s: %s\n", change.Name, Redact(err.Error()))
			}
			return err
		}
		archive := path.Join("/var/cache/pkg/sylve-inspect", relative)
		defer c.removeInspectionArchive(relative)
		result, err := c.Run(ctx, "fetch", "-U", "-y", "-r", repository, "-o", "/var/cache/pkg/sylve-inspect", change.Name+"-"+change.To)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return commandError(result)
		}
		if scope == ScopeSystem {
			metadata, err := c.systemPackage(ctx, change.Name, archive)
			if err != nil {
				return err
			}
			if metadata.version != change.To {
				return fmt.Errorf("protected_or_invalid_package_archive: %s", change.Name)
			}
			if err = c.validateSystemPackage(metadata); err != nil {
				return err
			}
		} else {
			result, err = c.Run(ctx, "query", "-F", archive, "%n\t%v\t%o")
			if err != nil {
				return err
			}
			fields := strings.Split(strings.TrimSpace(result.Stdout), "\t")
			if result.ExitCode != 0 || len(fields) != 3 || fields[0] != change.Name || fields[1] != change.To || fields[2] == "" || Protected(fields[0], fields[2]) {
				return fmt.Errorf("protected_or_invalid_package_archive: %s", change.Name)
			}
		}
		paths, err := c.packageFiles(ctx, change.Name, archive)
		if err != nil {
			return err
		}
		files = append(files, paths...)
	}
	return c.verifyPackagePaths(ctx, files)
}

func (c *Client) ValidateInstalled(plan Plan, inventory []Package) error {
	if err := c.validateScopedChanges(plan); err != nil {
		return err
	}
	scope, _ := NormalizeScope(c.Scope)
	installed := map[string]Package{}
	for _, pkg := range inventory {
		installed[pkg.Name] = pkg
	}
	for _, change := range plan.Changes {
		pkg, ok := installed[change.Name]
		if !ok {
			if change.Action != "install" {
				return fmt.Errorf("package_inventory_changed")
			}
			continue
		}
		if pkg.Locked || (scope == ScopeApplications && pkg.Protected) {
			return fmt.Errorf("protected_or_locked_package_change: %s", pkg.Name)
		}
		if scope == ScopeSystem && (Classify(pkg.Name, pkg.Origin) != KindBase || pkg.Version != change.From) {
			return fmt.Errorf("package_system_transaction_unsupported: %s", pkg.Name)
		}
	}
	return nil
}

func (c *Client) VerifyInstalled(ctx context.Context, plan Plan, inventory []Package) error {
	if err := c.ValidateInstalled(plan, inventory); err != nil {
		return err
	}
	installed := make(map[string]Package, len(inventory))
	for _, pkg := range inventory {
		installed[pkg.Name] = pkg
	}
	scope, _ := NormalizeScope(c.Scope)
	var files []string
	for _, change := range plan.Changes {
		pkg, ok := installed[change.Name]
		if !ok {
			continue
		}
		if scope == ScopeSystem {
			metadata, err := c.systemPackage(ctx, pkg.Name, "")
			if err != nil {
				return err
			}
			if metadata.version != pkg.Version {
				return fmt.Errorf("package_inventory_changed")
			}
			if err = c.validateSystemPackage(metadata); err != nil {
				return err
			}
		}
		paths, err := c.packageFiles(ctx, pkg.Name, "")
		if err != nil {
			return err
		}
		files = append(files, paths...)
	}
	return c.verifyPackagePaths(ctx, files)
}

func (c *Client) packageFiles(ctx context.Context, name, archive string) ([]string, error) {
	args := []string{"query", "%Fp", name}
	if archive != "" {
		args = []string{"query", "-F", archive, "%Fp"}
	}
	result, err := c.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, commandError(result)
	}
	files := strings.Split(result.Stdout, "\n")
	for _, file := range files {
		clean := path.Clean("/" + strings.TrimSpace(file))
		if clean == "/boot" || strings.HasPrefix(clean, "/boot/") {
			return nil, fmt.Errorf("kernel_package_change_unsupported: %s", name)
		}
	}
	return files, nil
}

func (c *Client) verifyPackagePaths(ctx context.Context, files []string) error {
	paths := append(append([]string(nil), c.ManagedPaths...), files...)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	if len(paths) > 0 && paths[0] == "" {
		paths = paths[1:]
	}
	if len(paths) > 0 && c.Target.Dataset != "" {
		if err := ValidatePackagePaths(ctx, c.Target, paths); err != nil {
			return err
		}
	}
	c.ManagedPaths = paths
	return nil
}

type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Origin  string `json:"origin"`
}

func (c *Client) Dependencies(ctx context.Context, name string) ([]Dependency, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid_package_name")
	}
	result, err := c.Run(ctx, "query", "%dn\t%dv\t%do", name)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, commandError(result)
	}
	deps := []Dependency{}
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || !ValidName(fields[0]) {
			return nil, fmt.Errorf("invalid_package_dependencies")
		}
		deps = append(deps, Dependency{Name: fields[0], Version: fields[1], Origin: fields[2]})
	}
	return deps, nil
}

func ValidateTransaction(actual, approved Plan) error {
	if len(actual.Changes) == 0 {
		return fmt.Errorf("empty_package_confirmation")
	}
	if len(actual.Changes) != len(approved.Changes) {
		return fmt.Errorf("package_plan_changed")
	}
	allowed := make(map[string]Change, len(approved.Changes))
	for _, change := range approved.Changes {
		allowed[change.Name] = change
	}
	for _, change := range actual.Changes {
		if expected, ok := allowed[change.Name]; !ok || expected != change {
			return fmt.Errorf("package_plan_changed")
		}
	}
	return nil
}
