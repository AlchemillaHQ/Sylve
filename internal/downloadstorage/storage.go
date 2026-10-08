// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package downloadstorage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/zfsutil"
	"gorm.io/gorm"
)

var (
	ErrInvalid     = errors.New("download_storage_invalid")
	ErrUnavailable = errors.New("download_storage_unavailable")
	ErrMismatch    = errors.New("download_storage_changed")
	ErrReadOnly    = errors.New("download_storage_read_only")
	ErrInUse       = errors.New("download_storage_in_use")
)

func IsError(err error) bool {
	return errors.Is(err, ErrInvalid) || errors.Is(err, ErrUnavailable) || errors.Is(err, ErrMismatch) ||
		errors.Is(err, ErrReadOnly) || errors.Is(err, ErrInUse)
}

type Choice struct {
	StoragePool string `json:"storagePool"`
	Label       string `json:"label"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason,omitempty"`
}

type Choices struct {
	Choices []Choice `json:"choices"`
	Error   string   `json:"error,omitempty"`
}

// Resolver is shared by payload reservation and destructive storage operations.
// The gate covers receiving uploads before their durable identity is registered.
type Resolver struct {
	DB    *gorm.DB
	GZFS  *gzfs.Client
	mu    sync.Mutex
	gates map[string]*poolGate
	mount func(string, string) error
}

type poolGate struct {
	sync.RWMutex
	reserve sync.Mutex
}

func New(db *gorm.DB, client *gzfs.Client) *Resolver {
	return &Resolver{DB: db, GZFS: client, mount: verifyMount}
}

func (r *Resolver) gate(pool string) *poolGate {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gates == nil {
		r.gates = make(map[string]*poolGate)
	}
	if r.gates[pool] == nil {
		r.gates[pool] = &poolGate{}
	}
	return r.gates[pool]
}

func (r *Resolver) ReadLock(pools ...string) func() {
	if r == nil {
		return func() {}
	}
	var gates []*poolGate
	for _, pool := range uniquePools(pools) {
		gate := r.gate(pool)
		gate.RLock()
		gates = append(gates, gate)
	}
	return func() {
		for i := len(gates) - 1; i >= 0; i-- {
			gates[i].RUnlock()
		}
	}
}

// Writers never wait for readers. Lock order is service-local locks, sorted
// pool gates, then database work. Default storage does not use a pool gate.
func (r *Resolver) TryMutation(names ...string) (func(), error) {
	if r == nil {
		return func() {}, nil
	}
	var pools []string
	for _, name := range names {
		name, _, _ = strings.Cut(name, "@")
		pool, _, _ := strings.Cut(name, "/")
		root := pool + "/sylve/downloads"
		if name == pool || name == pool+"/sylve" || name == root || strings.HasPrefix(name, root+"/") {
			pools = append(pools, pool)
		}
	}
	var gates []*poolGate
	release := func() {
		for i := len(gates) - 1; i >= 0; i-- {
			gates[i].Unlock()
		}
	}
	for _, pool := range uniquePools(pools) {
		gate := r.gate(pool)
		if !gate.TryLock() {
			release()
			return nil, ErrInUse
		}
		gates = append(gates, gate)
	}
	return release, nil
}

func uniquePools(pools []string) []string {
	result := slices.Clone(pools)
	slices.Sort(result)
	result = slices.Compact(result)
	return slices.DeleteFunc(result, func(pool string) bool { return pool == "" })
}

type Layout struct {
	Root    string
	dataset string
	mount   func(string, string) error
	binding utilitiesModels.DownloadStorage
}

func (l Layout) Dir(kind string) string { return filepath.Join(l.Root, kind) }

func (l Layout) Pool() string {
	pool, _, _ := strings.Cut(l.dataset, "/")
	return pool
}

func (r *Resolver) Recheck(ctx context.Context, layout Layout, write bool) error {
	current, err := r.Resolve(ctx, layout.binding, write)
	if err == nil && current.Root != layout.Root {
		return ErrMismatch
	}
	return err
}

func (l Layout) ValidateDownload(download utilitiesModels.Downloads) error {
	if !validID(download.UUID) {
		return ErrMismatch
	}
	extracted := filepath.Join(l.Dir("extracted"), download.UUID)
	if !pathWithin(l.Dir("extracted"), extracted) || extracted == l.Dir("extracted") {
		return ErrMismatch
	}
	if err := l.ValidatePath(extracted); err != nil {
		return err
	}
	path := filepath.Clean(download.Path)
	switch download.Type {
	case utilitiesModels.DownloadTypeTorrent:
		if path != filepath.Join(l.Dir("torrents"), download.UUID) {
			return ErrMismatch
		}
	case utilitiesModels.DownloadTypeHTTP, utilitiesModels.DownloadTypePath:
		kind := "http"
		if download.Type == utilitiesModels.DownloadTypePath {
			kind = "path"
		}
		if !(path != l.Dir(kind) && pathWithin(l.Dir(kind), path)) && !(path != extracted && pathWithin(extracted, path)) {
			return ErrMismatch
		}
	default:
		return ErrInvalid
	}
	if err := l.ValidatePath(path); err != nil {
		return err
	}
	if download.ExtractedPath != "" {
		output := filepath.Clean(download.ExtractedPath)
		if output != path && !pathWithin(extracted, output) {
			return ErrMismatch
		}
		return l.ValidatePath(output)
	}
	return nil
}

func (l Layout) TorrentFile(id, relative string) (string, error) {
	root := filepath.Join(l.Dir("torrents"), id)
	path := filepath.Join(root, relative)
	if !validID(id) || filepath.IsAbs(relative) || path == root || !pathWithin(root, path) || !pathWithin(l.Dir("torrents"), root) {
		return "", ErrMismatch
	}
	if err := l.ValidatePath(path); err != nil {
		return "", err
	}
	return path, nil
}

func DefaultRoot() (string, error) {
	root, err := config.GetDataPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "downloads"), nil
}

// ValidatePath accepts missing destinations but never follows symlinks or child
// mounts below a pool's managed root. A configured Default root may itself be a
// symlink, preserving the existing dataPath contract.
func (l Layout) ValidatePath(candidate string) error {
	if l.dataset != "" {
		if err := l.mount(l.Root, l.dataset); err != nil {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
	}
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(l.Root, candidate)
	if err != nil || !filepath.IsAbs(candidate) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrMismatch
	}
	for current := candidate; current != l.Root; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%w: inspect managed path: %v", ErrUnavailable, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || current != candidate && !info.IsDir() {
			return ErrMismatch
		}
		if l.dataset != "" {
			if err := l.mount(current, l.dataset); err != nil {
				return fmt.Errorf("%w: %v", ErrMismatch, err)
			}
		}
	}
	return nil
}

// ValidateTree checks recursive IO boundaries without following rootfs symlinks.
// Removal may unlink symlinks, but must never descend into a child mount.
func (l Layout) ValidateTree(root string) error {
	if err := l.ValidatePath(root); err != nil {
		return err
	}
	if l.dataset == "" {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == root {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: inspect managed tree: %v", ErrUnavailable, err)
		}
		if entry.IsDir() {
			return l.ValidatePath(path)
		}
		return nil
	})
}

func validID(id string) bool {
	return id != "" && !strings.HasPrefix(id, ".") && !strings.ContainsAny(id, "/\\\x00")
}

func (r *Resolver) pool(ctx context.Context, name string) (*gzfs.ZPool, error) {
	var db *gorm.DB
	if r != nil {
		db = r.DB
	}
	return r.poolWithDB(ctx, db, name)
}

func (r *Resolver) poolWithDB(ctx context.Context, db *gorm.DB, name string) (*gzfs.ZPool, error) {
	if strings.TrimSpace(name) != name || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return nil, ErrInvalid
	}
	if r == nil || db == nil || r.GZFS == nil || r.GZFS.Zpool == nil || r.GZFS.ZFS == nil {
		return nil, ErrUnavailable
	}
	var settings models.BasicSettings
	if err := db.WithContext(ctx).First(&settings).Error; err != nil {
		return nil, fmt.Errorf("%w: load managed pools: %v", ErrUnavailable, err)
	}
	if !slices.Contains(settings.Pools, name) {
		return nil, ErrInvalid
	}
	pool, err := r.GZFS.Zpool.Get(ctx, name)
	if err != nil || pool == nil || pool.Name != name || pool.PoolGUID == "" {
		return nil, ErrUnavailable
	}
	altroot := strings.TrimSpace(pool.Properties["altroot"].Value)
	if altroot != "" && altroot != "-" {
		return nil, ErrInvalid
	}
	return pool, nil
}

func (r *Resolver) filesystem(ctx context.Context, name string, write bool) (*gzfs.Dataset, Layout, error) {
	dataset, err := r.GZFS.ZFS.Get(ctx, name, false)
	if err != nil || dataset == nil {
		return nil, Layout{}, ErrUnavailable
	}
	root, err := zfsutil.FilesystemMountpoint(dataset)
	if err != nil || dataset.Name != name || dataset.GUID == "" {
		return nil, Layout{}, ErrMismatch
	}
	if dataset.Properties["mounted"].Value != "yes" || dataset.Properties["keystatus"].Value == "unavailable" {
		return nil, Layout{}, ErrUnavailable
	}
	if write && dataset.Properties["readonly"].Value == "on" {
		return nil, Layout{}, ErrReadOnly
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, Layout{}, ErrUnavailable
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return nil, Layout{}, ErrMismatch
	}
	if err := r.mount(root, name); err != nil {
		return nil, Layout{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return dataset, Layout{Root: root, dataset: name, mount: r.mount}, nil
}

func (r *Resolver) dataset(ctx context.Context, name string, write bool) (*gzfs.Dataset, Layout, error) {
	dataset, layout, err := r.filesystem(ctx, name, write)
	if err != nil {
		return nil, Layout{}, err
	}
	dataRoot, err := config.GetDataPath()
	if err != nil {
		return nil, Layout{}, ErrUnavailable
	}
	dataRoot, err = filepath.EvalSymlinks(dataRoot)
	if err != nil || pathWithin(layout.Root, dataRoot) || pathWithin(dataRoot, layout.Root) {
		return nil, Layout{}, ErrInvalid
	}
	return dataset, layout, nil
}

// Reserve may provision a missing download namespace. Existing storage is never
// provisioned by Resolve; in particular, a missing referenced dataset is not
// replaced by an empty same-name filesystem.
func (r *Resolver) Reserve(ctx context.Context, poolName string) (utilitiesModels.DownloadStorage, Layout, error) {
	ref := utilitiesModels.DownloadStorage{StoragePool: poolName}
	if poolName == "" {
		layout, err := r.Resolve(ctx, ref, true)
		ref.StorageRoot = layout.Root
		layout.binding = ref
		if err == nil {
			err = ensureLayout(layout)
		}
		return ref, layout, err
	}
	pool, err := r.pool(ctx, poolName)
	if err != nil {
		return ref, Layout{}, err
	}
	if pool.Properties["readonly"].Value == "on" {
		return ref, Layout{}, ErrReadOnly
	}
	// Concurrent first uploads may need the same namespace. Serialize only its
	// provisioning, not transfers or reservations on other pools.
	gate := r.gate(poolName)
	gate.reserve.Lock()
	defer gate.reserve.Unlock()
	name := poolName + "/sylve/downloads"
	existing, lookupErr := r.GZFS.ZFS.Get(ctx, name, false)
	if existing == nil {
		if lookupErr != nil && !zfsutil.DatasetDoesNotExist(lookupErr) {
			return ref, Layout{}, ErrUnavailable
		}
		if usageErr := RequireDatasetUnused(ctx, r.DB, name); usageErr != nil {
			return ref, Layout{}, fmt.Errorf("%w: referenced storage cannot be provisioned", ErrUnavailable)
		}
		if _, _, err := r.filesystem(ctx, poolName, true); err != nil {
			return ref, Layout{}, err
		}
		if _, ensureErr := zfsutil.EnsureDownloadNamespace(ctx, r.GZFS, poolName); ensureErr != nil {
			return ref, Layout{}, fmt.Errorf("%w: provision download storage: %v", ErrUnavailable, ensureErr)
		}
	}
	dataset, layout, err := r.dataset(ctx, name, true)
	if err != nil {
		return ref, Layout{}, err
	}
	ref.StorageRoot, ref.StoragePoolGUID, ref.StorageDatasetGUID = layout.Root, pool.PoolGUID, dataset.GUID
	layout.binding = ref
	if err := r.requireCompatibleBinding(ctx, ref); err != nil {
		return ref, Layout{}, err
	}
	return ref, layout, ensureLayout(layout)
}

func (r *Resolver) requireCompatibleBinding(ctx context.Context, binding utilitiesModels.DownloadStorage) error {
	refs, err := references(ctx, r.DB)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.StoragePool == binding.StoragePool && ref != binding {
			return ErrMismatch
		}
	}
	return nil
}

func ensureLayout(layout Layout) error {
	for _, kind := range []string{"http", "path", "torrents", "extracted", "uploads"} {
		directory := layout.Dir(kind)
		if err := layout.ValidatePath(directory); err != nil {
			return err
		}
		if err := os.MkdirAll(directory, 0755); err != nil {
			return fmt.Errorf("%w: create download directory: %v", ErrUnavailable, err)
		}
	}
	return nil
}

func (r *Resolver) Resolve(ctx context.Context, ref utilitiesModels.DownloadStorage, write bool) (Layout, error) {
	var db *gorm.DB
	if r != nil {
		db = r.DB
	}
	return r.ResolveWithDB(ctx, db, ref, write)
}

// ResolveWithDB preserves transaction-local reads for single-connection callers.
func (r *Resolver) ResolveWithDB(ctx context.Context, db *gorm.DB, ref utilitiesModels.DownloadStorage, write bool) (Layout, error) {
	if ref.StoragePool == "" {
		root, err := DefaultRoot()
		if err != nil {
			return Layout{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if ref.StorageRoot != "" && ref.StorageRoot != root || ref.StoragePoolGUID != "" || ref.StorageDatasetGUID != "" {
			return Layout{}, ErrMismatch
		}
		return Layout{Root: root, binding: ref}, nil
	}
	if ref.StorageRoot == "" || ref.StoragePoolGUID == "" || ref.StorageDatasetGUID == "" {
		return Layout{}, ErrMismatch
	}
	pool, err := r.poolWithDB(ctx, db, ref.StoragePool)
	if err != nil {
		return Layout{}, err
	}
	if pool.PoolGUID != ref.StoragePoolGUID {
		return Layout{}, ErrMismatch
	}
	if write && pool.Properties["readonly"].Value == "on" {
		return Layout{}, ErrReadOnly
	}
	dataset, layout, err := r.dataset(ctx, ref.StoragePool+"/sylve/downloads", write)
	if err != nil {
		return Layout{}, err
	}
	if dataset.GUID != ref.StorageDatasetGUID || layout.Root != ref.StorageRoot {
		return Layout{}, ErrMismatch
	}
	layout.binding = ref
	return layout, nil
}

func (r *Resolver) Choices(ctx context.Context) Choices {
	result := Choices{Choices: []Choice{{Label: "Default", Available: true}}}
	if r == nil || r.DB == nil {
		result.Error = ErrUnavailable.Error()
		return result
	}
	var settings models.BasicSettings
	if err := r.DB.WithContext(ctx).First(&settings).Error; err != nil {
		result.Error = ErrUnavailable.Error()
		return result
	}
	seen := map[string]bool{}
	for _, name := range settings.Pools {
		if seen[name] {
			continue
		}
		seen[name] = true
		choice := Choice{StoragePool: name, Label: name, Available: true}
		pool, err := r.pool(ctx, name)
		if err == nil && pool.Properties["readonly"].Value == "on" {
			err = ErrReadOnly
		}
		if err == nil {
			dataset, layout, lookupErr := r.dataset(ctx, name+"/sylve/downloads", true)
			err = lookupErr
			if err == nil {
				pool, poolErr := r.pool(ctx, name)
				if poolErr != nil {
					err = poolErr
				} else {
					err = r.requireCompatibleBinding(ctx, utilitiesModels.DownloadStorage{StoragePool: name,
						StorageRoot: layout.Root, StoragePoolGUID: pool.PoolGUID, StorageDatasetGUID: dataset.GUID})
				}
			}
			// Older managed pools can be provisioned on explicit reservation.
			if errors.Is(err, ErrUnavailable) {
				ds, lookupErr := r.GZFS.ZFS.Get(ctx, name+"/sylve/downloads", false)
				if ds == nil && (lookupErr == nil || zfsutil.DatasetDoesNotExist(lookupErr)) {
					if err = RequireDatasetUnused(ctx, r.DB, name+"/sylve/downloads"); err == nil {
						_, _, err = r.filesystem(ctx, name, true)
					}
				}
			}
		}
		if err != nil {
			choice.Available, choice.Reason = false, ErrorCode(err)
		}
		result.Choices = append(result.Choices, choice)
	}
	return result
}

// CleanupLayouts includes idle managed datasets as well as referenced storage,
// so interrupted transfers without an upload record can be collected. A changed
// referenced pool is skipped, never scanned as a new empty storage target.
func (r *Resolver) CleanupLayouts(ctx context.Context) ([]Layout, error) {
	defaultLayout, err := r.Resolve(ctx, utilitiesModels.DownloadStorage{}, true)
	if err != nil {
		return nil, err
	}
	layouts := []Layout{defaultLayout}
	if r == nil || r.DB == nil {
		return layouts, nil
	}
	refs, err := references(ctx, r.DB)
	if err != nil {
		return layouts, err
	}
	seen := map[string]bool{"": true}
	var cleanupErr error
	// Check every binding before scanning any pool. One stale record must not
	// permit scanning a replacement through another, current record.
	for _, ref := range refs {
		if _, err := r.Resolve(ctx, ref, true); err != nil {
			seen[ref.StoragePool] = true
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	for _, ref := range refs {
		if seen[ref.StoragePool] {
			continue
		}
		seen[ref.StoragePool] = true
		layout, err := r.Resolve(ctx, ref, true)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		layouts = append(layouts, layout)
	}
	if r.GZFS != nil {
		var settings models.BasicSettings
		if err := r.DB.WithContext(ctx).First(&settings).Error; err != nil {
			return layouts, errors.Join(cleanupErr, err)
		}
		for _, name := range settings.Pools {
			if seen[name] {
				continue
			}
			seen[name] = true
			pool, err := r.pool(ctx, name)
			if err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
				continue
			}
			if pool.Properties["readonly"].Value == "on" {
				cleanupErr = errors.Join(cleanupErr, ErrReadOnly)
				continue
			}
			dataset, layout, err := r.dataset(ctx, name+"/sylve/downloads", true)
			if err == nil {
				layout.binding = utilitiesModels.DownloadStorage{StoragePool: name, StorageRoot: layout.Root,
					StoragePoolGUID: pool.PoolGUID, StorageDatasetGUID: dataset.GUID}
				layouts = append(layouts, layout)
			}
		}
	}
	return layouts, cleanupErr
}

func ErrorCode(err error) string {
	for _, code := range []error{ErrInvalid, ErrMismatch, ErrReadOnly, ErrUnavailable, ErrInUse} {
		if errors.Is(err, code) {
			return code.Error()
		}
	}
	return ErrUnavailable.Error()
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func references(ctx context.Context, db *gorm.DB) ([]utilitiesModels.DownloadStorage, error) {
	var refs []utilitiesModels.DownloadStorage
	if db == nil {
		return nil, ErrUnavailable
	}
	for _, model := range []any{&utilitiesModels.Downloads{}, &utilitiesModels.Upload{}} {
		var rows []utilitiesModels.DownloadStorage
		query := db.WithContext(ctx).Model(model).Where("storage_pool <> ?", "")
		if _, upload := model.(*utilitiesModels.Upload); upload {
			query = query.Where("scope = ?", utilitiesModels.UploadScopeDownloader)
		}
		if err := query.Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("%w: check download storage usage: %v", ErrUnavailable, err)
		}
		refs = append(refs, rows...)
	}
	return refs, nil
}

func RequireDatasetGUIDUnused(ctx context.Context, db *gorm.DB, guid string) error {
	refs, err := references(ctx, db)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.StorageDatasetGUID == guid || ref.StoragePoolGUID == guid {
			return ErrInUse
		}
	}
	return nil
}

func RequireDatasetUnused(ctx context.Context, db *gorm.DB, names ...string) error {
	refs, err := references(ctx, db)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		root := ref.StoragePool + "/sylve/downloads"
		for _, name := range names {
			name, _, _ = strings.Cut(name, "@")
			if name == root || strings.HasPrefix(root, name+"/") || strings.HasPrefix(name, root+"/") {
				return fmt.Errorf("%w: %s", ErrInUse, ref.StoragePool)
			}
		}
	}
	return nil
}
