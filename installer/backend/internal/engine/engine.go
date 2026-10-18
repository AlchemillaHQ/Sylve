// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/alchemillahq/gzfs"
)

type StepState string

const (
	StatePending StepState = "pending"
	StateRunning StepState = "running"
	StateDone    StepState = "done"
	StateFailed  StepState = "failed"
	StateSkipped StepState = "skipped"
)

type Step struct {
	ID     string    `json:"id"`
	Label  string    `json:"label"`
	Detail string    `json:"detail"`
	State  StepState `json:"state"`
	Error  string    `json:"error,omitempty"`
}

type Network struct {
	Mode     string `json:"mode"`
	Uplink   string `json:"uplink"`
	Address  string `json:"address"`
	Gateway  string `json:"gateway"`
	DNS      string `json:"dns"`
	Mode6    string `json:"mode6,omitempty"`
	Address6 string `json:"address6,omitempty"`
	Gateway6 string `json:"gateway6,omitempty"`
	DNS6     string `json:"dns6,omitempty"`
}

type ZFSVdev struct {
	Type  string   `json:"type"`
	Disks []string `json:"disks"`
}

type ZFSStorage struct {
	Name        string            `json:"name,omitempty"`
	Vdevs       []ZFSVdev         `json:"vdevs,omitempty"`
	Encrypt     bool              `json:"encrypt"`
	Passphrase  string            `json:"passphrase"`
	Swap        string            `json:"swap"`
	Force4k     *bool             `json:"force4k,omitempty"`
	Compression string            `json:"compression,omitempty"`
	Ashift      string            `json:"ashift,omitempty"`
	Autotrim    bool              `json:"autotrim,omitempty"`
	Comment     string            `json:"comment,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

type UFSStorage struct {
	Disk       string `json:"disk"`
	Target     string `json:"target"`
	Swap       string `json:"swap"`
	Encrypt    bool   `json:"encrypt"`
	Passphrase string `json:"passphrase"`
}

type Storage struct {
	Mode string     `json:"mode"`
	ZFS  ZFSStorage `json:"zfs"`
	UFS  UFSStorage `json:"ufs"`
}

type Plan struct {
	Hostname      string   `json:"hostname"`
	Timezone      string   `json:"timezone"`
	Keymap        string   `json:"keymap"`
	Network       Network  `json:"network"`
	RootPassword  string   `json:"rootPassword"`
	AdminPassword string   `json:"adminPassword"`
	DryRun        bool     `json:"dryRun"`
	Storage       *Storage `json:"storage,omitempty"`
}

type State struct {
	Running   bool     `json:"running"`
	Done      bool     `json:"done"`
	Failed    bool     `json:"failed"`
	DryRun    bool     `json:"dryRun"`
	Error     string   `json:"error,omitempty"`
	Steps     []Step   `json:"steps"`
	Logs      []string `json:"logs"`
	StartedAt string   `json:"startedAt,omitempty"`
	EndedAt   string   `json:"endedAt,omitempty"`
}

type Event struct {
	Type  string `json:"type"`
	State *State `json:"state,omitempty"`
	Line  string `json:"line,omitempty"`
}

type Engine struct {
	mu          sync.Mutex
	plan        Plan
	storage     Storage
	state       State
	subs        map[chan Event]struct{}
	repoOffline bool
}

const (
	passFile  = "/tmp/.sylve-install-pass"
	maxLogs   = 500
	tipRepo   = "AlchemillaHQ/Sylve"
	tipAsset  = "sylve-amd64"
	mountRoot = "/mnt"
)

func New(plan Plan) *Engine {
	storage := resolveStorage(plan)
	return &Engine{
		plan:    plan,
		storage: storage,
		state: State{
			Steps: storageSteps(storage.Mode, poolNameOf(storage.ZFS)),
		},
		subs: map[chan Event]struct{}{},
	}
}

func resolveStorage(p Plan) Storage {
	if p.Storage != nil && p.Storage.Mode == "ufs" {
		st := *p.Storage
		if st.UFS.Target == "" {
			st.UFS.Target = "entire"
		}
		if st.UFS.Swap == "" {
			st.UFS.Swap = "0"
		}
		return st
	}
	st := Storage{Mode: "zfs"}
	if p.Storage != nil {
		st.ZFS = p.Storage.ZFS
	}
	if st.ZFS.Swap == "" {
		st.ZFS.Swap = "4g"
	}
	if st.ZFS.Compression == "" {
		st.ZFS.Compression = "lz4"
	}
	return st
}

func storageSteps(mode, pool string) []Step {
	if mode == "ufs" {
		return []Step{
			{ID: "validate", Label: "Validate plan", Detail: "disk, passwords, boot media"},
			{ID: "partition", Label: "Partition disk", Detail: "GPT: boot, EFI, root, swap"},
			{ID: "encrypt", Label: "Encrypt root (GELI)", Detail: "AES-XTS on the root partition"},
			{ID: "pool", Label: "Create filesystem", Detail: "newfs -U and mount root"},
			{ID: "base", Label: "Install base system", Detail: "pkgbase: FreeBSD-set-base + kernel"},
			{ID: "configure", Label: "Configure system", Detail: "rc.conf, loader, users, network"},
			{ID: "boot", Label: "Install boot code", Detail: "BIOS gptboot + UEFI loader.efi"},
			{ID: "sylve", Label: "Install Sylve", Detail: "deps + tip binary"},
			{ID: "finish", Label: "Finish", Detail: "sync and unmount"},
		}
	}
	return []Step{
		{ID: "validate", Label: "Validate plan", Detail: "disks, passwords, boot media"},
		{ID: "partition", Label: "Partition disks", Detail: "GPT: boot, EFI, swap, ZFS"},
		{ID: "encrypt", Label: "Encrypt (GELI)", Detail: "AES-XTS on the ZFS partition"},
		{ID: "pool", Label: "Create ZFS pool", Detail: pool + " + datasets"},
		{ID: "base", Label: "Install base system", Detail: "pkgbase: FreeBSD-set-base + kernel"},
		{ID: "configure", Label: "Configure system", Detail: "rc.conf, loader, users, network"},
		{ID: "boot", Label: "Install boot code", Detail: "BIOS gptzfsboot + UEFI loader.efi"},
		{ID: "sylve", Label: "Install Sylve", Detail: "deps + tip binary"},
		{ID: "finish", Label: "Finish", Detail: "sync, unmount, export pool"},
	}
}

func (e *Engine) ufsMode() bool { return e.storage.Mode == "ufs" }

func (e *Engine) encryptEnabled() bool {
	if e.ufsMode() {
		return e.storage.UFS.Encrypt
	}
	return e.storage.ZFS.Encrypt
}

func (e *Engine) devices() []string {
	if e.ufsMode() {
		return []string{e.storage.UFS.Disk}
	}
	return e.zfsDisks()
}

func (e *Engine) zfsDisks() []string {
	var disks []string
	for _, vdev := range e.storage.ZFS.Vdevs {
		disks = append(disks, vdev.Disks...)
	}
	return disks
}

var partitionSuffixRe = regexp.MustCompile(`[ps][0-9]+$`)

func zfsMemberIsPartition(member string) bool {
	return partitionSuffixRe.MatchString(member)
}

func partitionParent(member string) string {
	return partitionSuffixRe.ReplaceAllString(member, "")
}

func (e *Engine) zfsWholeDisks() []string {
	var disks []string
	for _, vdev := range e.storage.ZFS.Vdevs {
		for _, member := range vdev.Disks {
			if !zfsMemberIsPartition(member) {
				disks = append(disks, member)
			}
		}
	}
	return disks
}

func (e *Engine) zfsPartitionMembers() []string {
	var parts []string
	for _, vdev := range e.storage.ZFS.Vdevs {
		for _, member := range vdev.Disks {
			if zfsMemberIsPartition(member) {
				parts = append(parts, member)
			}
		}
	}
	return parts
}

type zfsVdevSpec struct {
	min  int
	even bool
}

var zfsVdevs = map[string]zfsVdevSpec{
	"stripe": {min: 1},
	"mirror": {min: 2},
	"raidz1": {min: 3},
	"raidz2": {min: 4},
	"raidz3": {min: 5},
	"raid10": {min: 4, even: true},
}

func zfsVdevArgs(vdev string, providers []string) ([]string, error) {
	spec, ok := zfsVdevs[vdev]
	if !ok {
		return nil, fmt.Errorf("unsupported vdev type %q", vdev)
	}
	if len(providers) < spec.min {
		return nil, fmt.Errorf("%s needs at least %d disks (have %d)", vdev, spec.min, len(providers))
	}
	if spec.even && len(providers)%2 != 0 {
		return nil, fmt.Errorf("%s needs an even number of disks (have %d)", vdev, len(providers))
	}
	switch vdev {
	case "stripe":
		return providers, nil
	case "mirror":
		return append([]string{"mirror"}, providers...), nil
	case "raidz1":
		return append([]string{"raidz"}, providers...), nil
	case "raidz2":
		return append([]string{"raidz2"}, providers...), nil
	case "raidz3":
		return append([]string{"raidz3"}, providers...), nil
	case "raid10":
		var args []string
		for i := 0; i+1 < len(providers); i += 2 {
			args = append(args, "mirror", providers[i], providers[i+1])
		}
		return args, nil
	}
	return nil, fmt.Errorf("unsupported vdev type %q", vdev)
}

var (
	compressionRe      = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._+-]*$`)
	poolPropertyRe     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._:@-]*$`)
	poolNameRe         = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]*$`)
	diskLikePoolNameRe = regexp.MustCompile(`^c[0-9]`)
	rootLoginRe        = regexp.MustCompile(`(?m)^#?\s*PermitRootLogin\s+.*$`)
	hostnameRe         = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
)

func validateZFSOptions(z ZFSStorage) error {
	if poolNameOf(z) == "bootpool" {
		return fmt.Errorf("pool name %q is reserved for the installer bootpool", "bootpool")
	}
	if z.Name != "" {
		if err := validatePoolName(z.Name); err != nil {
			return err
		}
	}
	if z.Compression != "" && !compressionRe.MatchString(z.Compression) {
		return fmt.Errorf("invalid compression %q", z.Compression)
	}
	if strings.ContainsAny(z.Comment, "\x00\n\r") {
		return fmt.Errorf("the pool comment must be a single line")
	}
	for key, value := range z.Properties {
		switch key {
		case "altroot", "cachefile":
			return fmt.Errorf("the %q pool property is managed by the installer", key)
		}
		if !poolPropertyRe.MatchString(key) {
			return fmt.Errorf("invalid pool property name %q", key)
		}
		if strings.ContainsAny(value, "\x00\n\r") {
			return fmt.Errorf("the %q pool property must be a single line", key)
		}
	}
	if z.Ashift != "" {
		n, err := strconv.Atoi(z.Ashift)
		if err != nil || n < 9 || n > 16 {
			return fmt.Errorf("ashift must be a number between 9 and 16")
		}
	}
	return nil
}

func validatePoolName(name string) error {
	name = strings.TrimSpace(name)
	if !poolNameRe.MatchString(name) {
		return fmt.Errorf("invalid pool name %q", name)
	}
	for _, reserved := range []string{"log", "mirror", "raidz", "spare"} {
		if strings.HasPrefix(name, reserved) {
			return fmt.Errorf("pool name %q starts with the reserved word %q", name, reserved)
		}
	}
	if diskLikePoolNameRe.MatchString(name) {
		return fmt.Errorf("pool name %q cannot start with c followed by a digit", name)
	}
	return nil
}

func poolNameOf(z ZFSStorage) string {
	if name := strings.TrimSpace(z.Name); name != "" {
		return name
	}
	return "zroot"
}

func (e *Engine) poolName() string { return poolNameOf(e.storage.ZFS) }

func (e *Engine) zfsForce4k() bool {
	if e.storage.ZFS.Force4k == nil {
		return true
	}
	return *e.storage.ZFS.Force4k
}

func (e *Engine) zfsPartitionIndices() (swapIdx, zfsIdx int) {
	withSwap := false
	if bytes, err := parseSizeString(e.storage.ZFS.Swap); err == nil && bytes > 0 {
		withSwap = true
	}
	if e.encryptEnabled() {
		if withSwap {
			return 4, 5
		}
		return 0, 4
	}
	if withSwap {
		return 3, 4
	}
	return 0, 3
}

func (e *Engine) Start() error {
	e.mu.Lock()
	if e.state.Running {
		e.mu.Unlock()
		return fmt.Errorf("an install is already running")
	}
	if err := e.validateLocked(); err != nil {
		e.mu.Unlock()
		return err
	}
	e.state.Running = true
	e.state.StartedAt = time.Now().UTC().Format(time.RFC3339)
	e.mu.Unlock()

	e.emitState()
	go e.run()
	return nil
}

func (e *Engine) Snapshot() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

func (e *Engine) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 128)
	e.mu.Lock()
	e.subs[ch] = struct{}{}
	snap := e.snapshotLocked()
	e.mu.Unlock()

	select {
	case ch <- Event{Type: "state", State: &snap}:
	default:
	}
	return ch, func() {
		e.mu.Lock()
		delete(e.subs, ch)
		e.mu.Unlock()
	}
}

func (e *Engine) run() {
	order := []string{"partition", "encrypt", "pool", "base", "configure", "boot", "sylve", "finish"}
	fail := func(id string, err error) {
		e.setStep(id, StateFailed, err.Error())
		e.logf("ERROR: %v", err)
		_ = os.Remove(passFile)
		e.mu.Lock()
		e.state.Running = false
		e.state.Failed = true
		e.state.Error = err.Error()
		e.state.EndedAt = time.Now().UTC().Format(time.RFC3339)
		e.mu.Unlock()
		e.emitState()
	}

	e.setStep("validate", StateDone, "")
	for _, id := range order {
		if e.plan.DryRun {
			e.setStep(id, StateSkipped, "dry run")
			continue
		}
		if id == "encrypt" && !e.encryptEnabled() {
			e.setStep(id, StateSkipped, "encryption disabled")
			continue
		}
		e.setStep(id, StateRunning, "")
		if err := e.execStep(id); err != nil {
			fail(id, err)
			return
		}
		e.setStep(id, StateDone, "")
	}

	_ = os.Remove(passFile)
	e.mu.Lock()
	e.state.Running = false
	e.state.Done = true
	e.state.EndedAt = time.Now().UTC().Format(time.RFC3339)
	e.mu.Unlock()
	e.emitState()
}

func (e *Engine) execStep(id string) error {
	switch id {
	case "partition":
		if e.ufsMode() {
			return e.stepPartitionUFS()
		}
		return e.stepPartition()
	case "encrypt":
		if e.ufsMode() {
			return e.stepEncryptUFS()
		}
		return e.stepEncrypt()
	case "pool":
		if e.ufsMode() {
			return e.stepRootfsUFS()
		}
		return e.stepPool()
	case "base":
		return e.stepBase()
	case "configure":
		return e.stepConfigure()
	case "boot":
		if e.ufsMode() {
			return e.stepBootUFS()
		}
		return e.stepBoot()
	case "sylve":
		return e.stepSylve()
	case "finish":
		return e.stepFinish()
	}
	return fmt.Errorf("unknown step %q", id)
}

func (e *Engine) stepPartition() error {
	if err := e.validateCurrentLayout(); err != nil {
		return err
	}
	swapBytes, err := parseSwapSize(e.storage.ZFS.Swap)
	if err != nil {
		return err
	}
	_, zfsIdx := e.zfsPartitionIndices()

	if err := cleanupTargetPools(context.Background(), e.zfsClient().Zpool, e.storage.ZFS, resolvePoolProvider); err != nil {
		return err
	}
	for _, name := range e.zfsWholeDisks() {
		_ = e.cmd("/sbin/geli", "detach", fmt.Sprintf("/dev/%sp%d.eli", name, zfsIdx))
	}
	for i, name := range e.zfsWholeDisks() {
		dev := "/dev/" + name
		if err := e.nukePartitionTable(dev); err != nil {
			return err
		}
		if err := e.cmd("/sbin/gpart", "create", "-s", "gpt", dev); err != nil {
			return err
		}
		alignSmall, alignBig := []string{}, []string{}
		if e.zfsForce4k() {
			alignSmall = []string{"-a", "4k"}
			alignBig = []string{"-a", "1m"}
		}
		adds := [][]string{
			append(append([]string{}, alignSmall...), "-t", "freebsd-boot", "-s", fmt.Sprintf("%dB", biosBootBytes), "-l", fmt.Sprintf("boot%d", i)),
			append(append([]string{}, alignSmall...), "-t", "efi", "-s", fmt.Sprintf("%dB", efiBytes), "-l", fmt.Sprintf("efi%d", i)),
		}
		if e.encryptEnabled() {
			adds = append(adds, append(append([]string{}, alignBig...), "-t", "freebsd-zfs", "-s", fmt.Sprintf("%dB", bootPoolBytes), "-l", fmt.Sprintf("bp%d", i)))
		}
		if swapBytes > 0 {
			adds = append(adds, append(append([]string{}, alignBig...), "-t", "freebsd-swap", "-s", fmt.Sprintf("%dB", swapBytes), "-l", fmt.Sprintf("swap%d", i)))
		}
		adds = append(adds, append(append([]string{}, alignBig...), "-t", "freebsd-zfs", "-l", fmt.Sprintf("zfs%d", i)))
		for _, a := range adds {
			if err := e.addPartitionWiped(dev, a...); err != nil {
				return err
			}
		}
		if err := e.cmd("/sbin/newfs_msdos", "-F", "16", "-L", "EFI", dev+"p2"); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) stepEncrypt() error {
	if err := os.WriteFile(passFile, []byte(e.storage.ZFS.Passphrase), 0o600); err != nil {
		return err
	}
	_, zfsIdx := e.zfsPartitionIndices()
	for _, name := range e.zfsWholeDisks() {
		dev := fmt.Sprintf("/dev/%sp%d", name, zfsIdx)
		if err := e.cmd("/sbin/geli", "init", "-b", "-s", "4096", "-e", "AES-XTS", "-l", "256", "-J", passFile, dev); err != nil {
			return err
		}
		if err := e.cmd("/sbin/geli", "attach", "-j", passFile, dev); err != nil {
			return err
		}
	}
	return nil
}

var rootDatasets = [][2]string{
	{"tmp", "/tmp"},
	{"var", ""},
	{"var/log", "/var/log"},
	{"var/db", "/var/db"},
	{"usr", ""},
	{"usr/ports", "/usr/ports"},
	{"usr/src", "/usr/src"},
}

func (e *Engine) createRootPool(vdevArgs []string) error {
	z := e.storage.ZFS
	pool := e.poolName()
	ctx := context.Background()
	client := e.zfsClient()

	if err := os.MkdirAll(mountRoot+"/boot/zfs", 0o755); err != nil {
		return err
	}
	compression := z.Compression
	if compression == "" {
		compression = "lz4"
	}

	props := map[string]string{
		"altroot":   mountRoot,
		"cachefile": mountRoot + "/boot/zfs/zpool.cache",
	}
	if z.Ashift != "" {
		props["ashift"] = z.Ashift
	}
	if z.Autotrim {
		props["autotrim"] = "on"
	}
	if z.Comment != "" {
		props["comment"] = z.Comment
	}
	for key, value := range z.Properties {
		props[key] = value
	}
	if err := client.Zpool.CreateWithOptions(ctx, pool, gzfs.ZPoolCreateOptions{
		Force:      true,
		Mountpoint: "/" + pool,
		Properties: props,
	}, vdevArgs...); err != nil {
		return fmt.Errorf("zpool create %s: %w", pool, err)
	}

	rootPool, err := client.ZFS.Get(ctx, pool, false)
	if err != nil || rootPool == nil {
		return fmt.Errorf("zfs get %s: %w", pool, err)
	}
	if err := rootPool.SetProperties(ctx, "canmount", "off"); err != nil {
		return err
	}
	if err := rootPool.SetProperties(ctx, "atime", "off", "compression", compression); err != nil {
		return err
	}

	ensureMounted := func(ds *gzfs.Dataset) error {
		if ds == nil {
			return nil
		}
		prop, err := ds.GetProperty(ctx, "mounted")
		if err != nil {
			return err
		}
		if prop.Value == "yes" {
			return nil
		}
		return ds.Mount(ctx, false)
	}

	if _, err := client.ZFS.CreateFilesystem(ctx, pool+"/ROOT", map[string]string{
		"mountpoint": "none",
		"canmount":   "off",
	}); err != nil {
		return err
	}
	root := pool + "/ROOT/default"
	rootDS, err := client.ZFS.CreateFilesystem(ctx, root, map[string]string{
		"mountpoint": "/",
		"canmount":   "noauto",
	})
	if err != nil {
		return err
	}
	if err := client.Zpool.SetProperty(ctx, pool, "bootfs", root); err != nil {
		return err
	}
	if err := ensureMounted(rootDS); err != nil {
		return err
	}

	for _, ds := range rootDatasets {
		props := map[string]string{}
		if ds[1] != "" {
			props["mountpoint"] = ds[1]
		} else {
			props["mountpoint"] = "none"
			props["canmount"] = "off"
		}
		created, err := client.ZFS.CreateFilesystem(ctx, pool+"/"+ds[0], props)
		if err != nil {
			return err
		}
		if ds[1] != "" {
			if err := ensureMounted(created); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) stepPool() error {
	if e.zfsForce4k() {
		if err := e.cmd("/sbin/sysctl", "vfs.zfs.vdev.min_auto_ashift=12"); err != nil {
			return err
		}
	}
	_, zfsIdx := e.zfsPartitionIndices()
	vdevArgs, err := zfsVdevArgsFor(e.storage.ZFS, zfsIdx)
	if err != nil {
		return err
	}
	if err := e.createRootPool(vdevArgs); err != nil {
		return err
	}
	if e.encryptEnabled() {
		return e.createBootPool()
	}
	return nil
}

func (e *Engine) createBootPool() error {
	ctx := context.Background()
	client := e.zfsClient()

	var providers []string
	for _, name := range e.zfsWholeDisks() {
		providers = append(providers, fmt.Sprintf("/dev/%sp3", name))
	}
	vdevArgs := providers
	if len(providers) > 1 {
		var err error
		if vdevArgs, err = zfsVdevArgs("mirror", providers); err != nil {
			return err
		}
	}

	if err := client.Zpool.CreateWithOptions(ctx, "bootpool", gzfs.ZPoolCreateOptions{
		Force:      true,
		Mountpoint: "/bootpool",
		Properties: map[string]string{"altroot": mountRoot, "cachefile": "none"},
	}, vdevArgs...); err != nil {
		return fmt.Errorf("zpool create bootpool: %w", err)
	}
	if err := os.MkdirAll(mountRoot+"/bootpool/boot", 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(mountRoot + "/boot"); err != nil {
		return err
	}
	if err := os.Symlink("bootpool/boot", mountRoot+"/boot"); err != nil {
		return fmt.Errorf("symlink /boot into the bootpool: %w", err)
	}
	if err := os.MkdirAll(mountRoot+"/boot/zfs", 0o755); err != nil {
		return err
	}
	if err := client.Zpool.SetProperty(ctx, "bootpool", "cachefile", mountRoot+"/boot/zfs/zpool.cache"); err != nil {
		return err
	}
	return nil
}

func validateVdevs(vdevs []ZFSVdev) error {
	seen := map[string]bool{}
	for i, vdev := range vdevs {
		if len(vdev.Disks) == 0 {
			return fmt.Errorf("vdev %d needs at least one disk", i+1)
		}
		if _, err := zfsVdevArgs(vdev.Type, vdev.Disks); err != nil {
			return err
		}
		for _, name := range vdev.Disks {
			if seen[name] {
				return fmt.Errorf("disk %q is used by more than one vdev", name)
			}
			seen[name] = true
		}
	}
	return nil
}

func zfsVdevArgsFor(z ZFSStorage, zfsIdx int) ([]string, error) {
	var args []string
	for _, vdev := range z.Vdevs {
		var providers []string
		for _, member := range vdev.Disks {
			var prov string
			if zfsMemberIsPartition(member) {
				prov = "/dev/" + member
			} else {
				prov = fmt.Sprintf("/dev/%sp%d", member, zfsIdx)
				if z.Encrypt {
					prov += ".eli"
				}
			}
			providers = append(providers, prov)
		}
		part, err := zfsVdevArgs(vdev.Type, providers)
		if err != nil {
			return nil, err
		}
		args = append(args, part...)
	}
	return args, nil
}

func pkgBinary() string {
	for _, path := range []string{"/usr/local/sbin/pkg", "/usr/sbin/pkg"} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "/usr/sbin/pkg"
}

func pkgArgs(args ...string) []string {
	return append([]string{
		"-r", mountRoot,
		"-o", "REPOS_DIR=" + mountRoot + "/etc/pkg," + mountRoot + "/usr/local/etc/pkg/repos",
	}, args...)
}

func (e *Engine) stepBase() error {
	if _, err := os.Stat("/usr/share/keys/pkg"); err != nil {
		return fmt.Errorf("live media is missing /usr/share/keys/pkg")
	}
	for _, dir := range []string{mountRoot + "/usr/share/keys", mountRoot + "/usr/local/etc/pkg/repos", mountRoot + "/etc/pkg"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := e.cmd("/bin/cp", "-R", "/usr/share/keys/pkg", mountRoot+"/usr/share/keys/"); err != nil {
		return err
	}

	local := offlinePayloadAvailable() && !hostReachable("https://pkg.freebsd.org/")
	if local {
		e.logf("pkg.FreeBSD.org unreachable - installing base from the bundled offline payload")
	}
	if err := e.writeRepoConfigs(local); err != nil {
		return err
	}
	install := func() error {
		if err := e.cmdEnv([]string{"ASSUME_ALWAYS_YES=yes"}, pkgBinary(), pkgArgs("update")...); err != nil {
			return err
		}
		return e.cmdEnv([]string{"ASSUME_ALWAYS_YES=yes"}, pkgBinary(),
			pkgArgs("install", "-y", "FreeBSD-set-base", "FreeBSD-kernel-generic", "pkg")...)
	}
	err := install()
	if err != nil && offlinePayloadAvailable() && !local {
		e.logf("online base install failed (%v) - retrying from the bundled offline payload", err)
		if err := e.writeRepoConfigs(true); err != nil {
			return err
		}
		return install()
	}
	return err
}

const fstabHeader = "# Device\tMountpoint\tFStype\tOptions\tDump\tPass#\n"

func swapFstabEntry(label string, encrypted bool) string {
	device := "/dev/gpt/" + label
	options := "sw"
	if encrypted {
		device += ".eli"
		options += ",ealgo=AES-XTS,keylen=256"
	}
	return fmt.Sprintf("%s\tnone\tswap\t%s\t0\t0\n", device, options)
}

func (e *Engine) zfsLoaderConf() string {
	loader := fmt.Sprintf("zfs_load=\"YES\"\nautoboot_delay=\"3\"\nvfs.root.mountfrom=\"zfs:%s/ROOT/default\"\n", e.poolName())
	loader += "kern.geom.label.disk_ident.enable=\"0\"\n"
	loader += "kern.geom.label.gptid.enable=\"0\"\n"
	if e.storage.ZFS.Encrypt {
		loader += "geom_eli_load=\"YES\"\n"
		loader += "aesni_load=\"YES\"\n"
		loader += "geom_eli_passphrase_prompt=\"YES\"\n"
	}
	return loader
}

func (e *Engine) zfsFstab() string {
	fstab := fstabHeader
	if bytes, err := parseSizeString(e.storage.ZFS.Swap); err == nil && bytes > 0 {
		for i := range e.zfsWholeDisks() {
			fstab += swapFstabEntry(fmt.Sprintf("swap%d", i), e.storage.ZFS.Encrypt)
		}
	}
	return fstab
}

func (e *Engine) stepConfigure() error {
	rc := fmt.Sprintf("hostname=%q\n", e.plan.Hostname)
	rc += "sshd_enable=\"YES\"\nzfs_enable=\"YES\"\ndbus_enable=\"YES\"\nsylve_enable=\"YES\"\ndumpdev=\"NO\"\n"
	rc += "sendmail_enable=\"NONE\"\nsendmail_submit_enable=\"NO\"\nsendmail_outbound_enable=\"NO\"\nsendmail_msp_queue_enable=\"NO\"\n"
	if !e.ufsMode() && e.encryptEnabled() {
		rc += "bootpool_enable=\"YES\"\n"
	}
	if e.plan.Keymap != "" && e.plan.Keymap != "us" {
		rc += fmt.Sprintf("keymap=%q\n", e.plan.Keymap)
	}
	if err := writeFile(mountRoot+"/etc/rc.conf", rc); err != nil {
		return err
	}

	if !e.ufsMode() && e.encryptEnabled() {
		script := mountRoot + "/etc/rc.d/bootpool"
		if err := writeFile(script, bootpoolRCScript); err != nil {
			return err
		}
		if err := os.Chmod(script, 0o555); err != nil {
			return err
		}
	}

	var loader, fstab string
	if e.ufsMode() {
		loader = e.ufsLoaderConf()
		fstab = e.ufsFstab()
	} else {
		loader = e.zfsLoaderConf()
		fstab = e.zfsFstab()
	}
	loader += "kern.racct.enable=1\n"
	if err := writeFile(mountRoot+"/boot/loader.conf", loader); err != nil {
		return err
	}

	if err := writeFile(mountRoot+"/etc/fstab", fstab); err != nil {
		return err
	}

	if tz := e.plan.Timezone; tz != "" {
		if err := e.cmd("/bin/cp", mountRoot+"/usr/share/zoneinfo/"+tz, mountRoot+"/etc/localtime"); err != nil {
			return err
		}
	}
	if e.plan.RootPassword != "" {
		if err := e.cmdStdin(e.plan.RootPassword, "/usr/sbin/pw", "-R", mountRoot, "usermod", "root", "-h", "0"); err != nil {
			return err
		}
		sshdCfg := mountRoot + "/etc/ssh/sshd_config"
		if blob, err := os.ReadFile(sshdCfg); err == nil {
			updated := rootLoginRe.ReplaceAllString(string(blob), "PermitRootLogin yes")
			if updated != string(blob) {
				if err := writeFile(sshdCfg, updated); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (e *Engine) stepBoot() error {
	for i, name := range e.zfsWholeDisks() {
		dev := "/dev/" + name
		if err := e.cmd("/sbin/gpart", "bootcode", "-b", mountRoot+"/boot/pmbr",
			"-p", mountRoot+"/boot/gptzfsboot", "-i", "1", dev); err != nil {
			return err
		}
		mp := fmt.Sprintf("/mnt/.efi%d", i)
		if err := os.MkdirAll(mp, 0o755); err != nil {
			return err
		}
		if err := e.cmd("/sbin/mount", "-t", "msdosfs", fmt.Sprintf("%sp2", dev), mp); err != nil {
			return err
		}
		if err := os.MkdirAll(mp+"/EFI/BOOT", 0o755); err != nil {
			return err
		}
		if err := e.cmd("/bin/cp", mountRoot+"/boot/loader.efi", mp+"/EFI/BOOT/BOOTX64.EFI"); err != nil {
			return err
		}
		if err := e.cmd("/sbin/umount", mp); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) stepSylve() error {
	if err := e.installSylvePackages(); err != nil {
		return err
	}

	cfg := map[string]any{
		"environment": "production",
		"ip":          "0.0.0.0",
		"port":        8181,
		"httpPort":    8182,
		"logLevel":    3,
		"dataPath":    "/var/db/sylve",
		"admin": map[string]any{
			"email":              "admin@sylve.local",
			"password":           e.plan.AdminPassword,
			"forcePasswordReset": false,
		},
	}
	if err := os.MkdirAll(mountRoot+"/usr/local/etc/sylve", 0o755); err != nil {
		return err
	}
	blob, _ := json.MarshalIndent(cfg, "", "    ")
	if err := writeFile(mountRoot+"/usr/local/etc/sylve/config.json", string(blob)+"\n"); err != nil {
		return err
	}

	tmp := "/tmp/.sylve-tip"
	if err := e.fetchTip(tmp); err != nil {
		return err
	}
	if err := e.cmd("/bin/cp", tmp, mountRoot+"/usr/local/sbin/sylve"); err != nil {
		return err
	}
	if err := e.cmd("/bin/chmod", "555", mountRoot+"/usr/local/sbin/sylve"); err != nil {
		return err
	}
	_ = os.Remove(tmp)

	if err := e.installSylveRc(); err != nil {
		return err
	}
	if err := os.MkdirAll(mountRoot+"/var/db/sylve", 0o755); err != nil {
		return err
	}
	return e.writeSylveBootstrap()
}

func (e *Engine) stepFinish() error {
	if err := e.writeRepoConfigs(false); err != nil {
		return err
	}

	uplink := strings.TrimSpace(e.plan.Network.Uplink)
	uplinkLine := "none (no usable interface at install time)"
	if uplink != "" {
		if iface, err := net.InterfaceByName(uplink); err == nil && len(iface.HardwareAddr) > 0 {
			uplinkLine = fmt.Sprintf("%s (%s)", uplink, iface.HardwareAddr)
		} else {
			uplinkLine = uplink
		}
	}
	report := fmt.Sprintf("FreeBSD + Sylve install report\nfinished: %s\nhostname: %s\nmode: %s\ndisks: %s\nencryption: %v\nuplink: %s\n",
		time.Now().UTC().Format(time.RFC3339), e.plan.Hostname, e.storage.Mode,
		strings.Join(e.devices(), ", "), e.encryptEnabled(), uplinkLine)
	_ = writeFile(mountRoot+"/var/log/freebsd-sylve-install.log", report)

	if err := e.cmd("/bin/sync"); err != nil {
		return err
	}
	if e.ufsMode() {
		return e.cmd("/sbin/umount", mountRoot)
	}
	pool := e.poolName()
	ctx := context.Background()
	client := e.zfsClient()

	for i := len(rootDatasets) - 1; i >= 0; i-- {
		if rootDatasets[i][1] == "" {
			continue
		}
		ds, err := client.ZFS.Get(ctx, pool+"/"+rootDatasets[i][0], false)
		if err != nil {
			return err
		}
		if ds == nil {
			continue
		}
		if err := ds.Unmount(ctx, true); err != nil {
			return err
		}
	}
	root, err := client.ZFS.Get(ctx, pool+"/ROOT/default", false)
	if err != nil {
		return err
	}
	if root != nil {
		if err := root.Unmount(ctx, true); err != nil {
			return err
		}
	}
	if err := client.Zpool.Export(ctx, pool); err != nil {
		return err
	}
	if e.encryptEnabled() {
		if bp, err := client.ZFS.Get(ctx, "bootpool", false); err != nil {
			return err
		} else if bp != nil {
			if err := bp.Unmount(ctx, true); err != nil {
				return err
			}
		}
		if err := client.Zpool.Export(ctx, "bootpool"); err != nil {
			return err
		}
	}
	return nil
}

var ifaceExists = func(name string) bool {
	iface, err := net.InterfaceByName(name)
	return err == nil && iface != nil
}

var readDiskInventory = Disks
var diskIsMounted = isMounted

func (e *Engine) validateLocked() error {
	p := e.plan
	if p.Storage != nil && p.Storage.Mode != "" && p.Storage.Mode != "zfs" && p.Storage.Mode != "ufs" {
		return fmt.Errorf("unsupported storage mode %q — use \"zfs\" or \"ufs\"", p.Storage.Mode)
	}
	if e.ufsMode() {
		if err := validateUFSTarget(e.storage.UFS.Target); err != nil {
			return err
		}
	}
	if !hostnameRe.MatchString(p.Hostname) {
		return fmt.Errorf("invalid hostname %q", p.Hostname)
	}
	if p.RootPassword == "" {
		return fmt.Errorf("root password is required")
	}
	if p.AdminPassword == "" {
		return fmt.Errorf("Sylve admin password is required")
	}
	if utf8.RuneCountInString(p.AdminPassword) < 8 {
		return fmt.Errorf("Sylve admin password must be at least 8 characters")
	}
	uplink := strings.TrimSpace(p.Network.Uplink)
	if uplink != "" && !ifaceExists(uplink) {
		return fmt.Errorf("network interface %q not found", uplink)
	}
	disks, err := readDiskInventory()
	if err != nil {
		return err
	}
	byName := map[string]Disk{}
	for _, d := range disks {
		byName[d.Name] = d
	}

	if e.ufsMode() {
		u := e.storage.UFS
		if u.Disk == "" {
			return fmt.Errorf("choose a disk for the UFS install")
		}
		if _, err := parseSizeString(u.Swap); err != nil {
			return err
		}
		if u.Encrypt && len(u.Passphrase) < 8 {
			return fmt.Errorf("the encryption passphrase must be at least 8 characters")
		}
		d, ok := byName[u.Disk]
		if !ok {
			return fmt.Errorf("disk %q not found", u.Disk)
		}
		if d.Boot {
			return fmt.Errorf("disk %q contains the running installer and cannot be a target", u.Disk)
		}
		if u.Target == "free" && d.Scheme == "" {
			return fmt.Errorf("%s has no partition table — initialise GPT first or use the entire disk", u.Disk)
		}
		if mounted, _ := diskIsMounted(u.Disk); mounted {
			return fmt.Errorf("disk %q has mounted partitions", u.Disk)
		}
		return e.validateStorageLayout(byName)
	}

	z := e.storage.ZFS
	if len(z.Vdevs) == 0 {
		return fmt.Errorf("choose at least one disk for the ZFS pool")
	}
	if err := validateVdevs(z.Vdevs); err != nil {
		return err
	}
	if err := validateZFSOptions(z); err != nil {
		return err
	}
	if _, err := parseSizeString(z.Swap); err != nil {
		return err
	}
	if z.Encrypt && len(z.Passphrase) < 8 {
		return fmt.Errorf("the encryption passphrase must be at least 8 characters")
	}
	if len(e.zfsWholeDisks()) == 0 {
		return fmt.Errorf("at least one whole disk is required for the boot loader")
	}
	for _, name := range e.zfsWholeDisks() {
		d, ok := byName[name]
		if !ok {
			return fmt.Errorf("disk %q not found", name)
		}
		if d.Boot {
			return fmt.Errorf("disk %q contains the running installer and cannot be a target", name)
		}
		if mounted, _ := diskIsMounted(name); mounted {
			return fmt.Errorf("disk %q has mounted partitions", name)
		}
	}
	if err := e.validatePartitionMembers(byName); err != nil {
		return err
	}
	return e.validateStorageLayout(byName)
}

const minTargetBytes = 8 << 30

func zfsUsableBytes(vdevs []ZFSVdev, memberSize func(string) int64) int64 {
	var usable int64
	for _, vdev := range vdevs {
		spec, ok := zfsVdevs[vdev.Type]
		if !ok || len(vdev.Disks) < spec.min || (spec.even && len(vdev.Disks)%2 != 0) {
			return 0
		}
		var sizes []int64
		for _, member := range vdev.Disks {
			size := memberSize(member)
			if size <= 0 {
				return 0
			}
			sizes = append(sizes, size)
		}
		smallest := sizes[0]
		for _, size := range sizes {
			smallest = min(smallest, size)
		}
		var capacity int64
		switch vdev.Type {
		case "mirror":
			capacity = smallest
		case "raidz1", "raidz2", "raidz3":
			parity := int(vdev.Type[len(vdev.Type)-1] - '0')
			members := int64(len(sizes) - parity)
			if smallest > math.MaxInt64/members {
				return 0
			}
			capacity = smallest * members
		case "raid10":
			for i := 0; i+1 < len(sizes); i += 2 {
				pair := min(sizes[i], sizes[i+1])
				if pair > math.MaxInt64-capacity {
					return 0
				}
				capacity += pair
			}
		default:
			for _, size := range sizes {
				if size > math.MaxInt64-capacity {
					return 0
				}
				capacity += size
			}
		}
		if capacity > math.MaxInt64-usable {
			return 0
		}
		usable += capacity
	}
	return usable
}

func (e *Engine) validatePartitionMembers(byName map[string]Disk) error {
	whole := map[string]bool{}
	for _, disk := range e.zfsWholeDisks() {
		whole[disk] = true
	}
	for _, member := range e.zfsPartitionMembers() {
		parentName := partitionParent(member)
		if whole[parentName] {
			return fmt.Errorf("%s and %s cannot both join the pool", parentName, member)
		}
		parent, ok := byName[parentName]
		if !ok {
			return fmt.Errorf("disk %q for partition %q not found", parentName, member)
		}
		if parent.Boot {
			return fmt.Errorf("partition %q is on the disk containing the running installer and cannot be a target", member)
		}
		if _, err := os.Stat("/dev/" + member); err != nil {
			return fmt.Errorf("partition %q not found", member)
		}
		if mounted, _ := isMounted(member); mounted {
			return fmt.Errorf("partition %q has mounted volumes", member)
		}
		_, parts := partitions(parent.Name, parent.SectorSize)
		found := false
		for _, part := range parts {
			if part.Name != member {
				continue
			}
			found = true
			if part.Type != "freebsd-zfs" {
				return fmt.Errorf("partition %q is %s — only freebsd-zfs partitions can join the pool", member, part.Type)
			}
		}
		if !found {
			return fmt.Errorf("partition %q not found on %s", member, parent.Name)
		}
	}
	return nil
}

func isMounted(disk string) (bool, string) {
	out, err := exec.Command("/sbin/mount").Output()
	if err != nil {
		return false, ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "/dev/"+disk) {
			return true, line
		}
	}
	return false, ""
}

func (e *Engine) setStep(id string, state StepState, errMsg string) {
	e.mu.Lock()
	for i := range e.state.Steps {
		if e.state.Steps[i].ID == id {
			e.state.Steps[i].State = state
			e.state.Steps[i].Error = errMsg
		}
	}
	e.mu.Unlock()
	e.emitState()
}

func (e *Engine) snapshotLocked() State {
	s := e.state
	s.Steps = append([]Step(nil), e.state.Steps...)
	s.Logs = append([]string(nil), e.state.Logs...)
	return s
}

func (e *Engine) emitState() {
	e.mu.Lock()
	snap := e.snapshotLocked()
	subs := make([]chan Event, 0, len(e.subs))
	for ch := range e.subs {
		subs = append(subs, ch)
	}
	e.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- Event{Type: "state", State: &snap}:
		default:
		}
	}
}

func (e *Engine) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05 ") + fmt.Sprintf(format, args...)
	e.mu.Lock()
	e.state.Logs = append(e.state.Logs, line)
	if len(e.state.Logs) > maxLogs {
		e.state.Logs = e.state.Logs[len(e.state.Logs)-maxLogs:]
	}
	subs := make([]chan Event, 0, len(e.subs))
	for ch := range e.subs {
		subs = append(subs, ch)
	}
	e.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- Event{Type: "log", Line: line}:
		default:
		}
	}
}

func (e *Engine) cmd(name string, args ...string) error {
	return e.cmdEnv(nil, name, args...)
}

func (e *Engine) cmdEnv(env []string, name string, args ...string) error {
	e.logf("$ %s %s", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	return e.runCommand(cmd)
}

func (e *Engine) cmdStdin(stdin string, name string, args ...string) error {
	e.logf("$ %s %s <password>", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin + "\n")
	return e.runCommand(cmd)
}

func (e *Engine) runCommand(cmd *exec.Cmd) error {
	output := commandOutput{e}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", cmd.Args[0], err)
	}
	return nil
}

type commandOutput struct{ e *Engine }

func (w commandOutput) Write(p []byte) (int, error) {
	w.e.logCommandOutput(string(p))
	return len(p), nil
}

func (e *Engine) logCommandOutput(output string) {
	for _, line := range strings.FieldsFunc(output, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if strings.TrimSpace(line) != "" {
			e.logf("  %s", line)
		}
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

const baseRepoConf = `FreeBSD-base {
  url: "pkg+https://pkg.freebsd.org/${ABI}/base_latest",
  mirror_type: "srv",
  signature_type: "fingerprints",
  fingerprints: "/usr/share/keys/pkg",
  enabled: yes
}
`

const portsRepoConf = `FreeBSD-ports {
  url: "pkg+https://pkg.FreeBSD.org/${ABI}/latest",
  mirror_type: "srv",
  signature_type: "fingerprints",
  fingerprints: "/usr/share/keys/pkg",
  enabled: yes
}
`

func sylveTipRelease() (url, digest, commit string, err error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+tipRepo+"/releases/tags/tip", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "freebsd-sylve-installer")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("github release lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("github release lookup: %s", resp.Status)
	}
	var release struct {
		TargetCommitish string `json:"target_commitish"`
		Assets          []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", "", err
	}
	for _, a := range release.Assets {
		if a.Name != tipAsset {
			continue
		}
		if !strings.HasPrefix(a.URL, "https://") {
			return "", "", "", fmt.Errorf("asset %q has no HTTPS download URL", tipAsset)
		}
		digest, ok := strings.CutPrefix(a.Digest, "sha256:")
		if !ok || !hex64Re.MatchString(digest) {
			return "", "", "", fmt.Errorf("asset %q has no valid GitHub SHA-256 digest", tipAsset)
		}
		return a.URL, strings.ToLower(digest), release.TargetCommitish, nil
	}
	return "", "", "", fmt.Errorf("asset %q not found in the tip release", tipAsset)
}

func download(url, dest, wantSHA256 string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if wantSHA256 != "" && got != wantSHA256 {
		_ = os.Remove(dest)
		return fmt.Errorf("tip binary checksum mismatch: want %s got %s", wantSHA256, got)
	}
	return nil
}

const bootpoolRCScript = `#!/bin/sh
#
# Import the small unencrypted pool that carries /boot on encrypted installs.
# The pool is deliberately kept out of the loader zpool cache: the disks are
# not guaranteed to be up when the kernel consumes the preload, and a race can
# leave the pool imported without its datasets.

# PROVIDE: bootpool
# REQUIRE: devfs
# BEFORE: zfs mountlate

. /etc/rc.subr

name="bootpool"
rcvar="bootpool_enable"
start_cmd="bootpool_start"
stop_cmd=":"

bootpool_start()
{
	# A pool that is imported but whose datasets are not visible yet is the
	# race this script exists to avoid; drop it and import it cleanly.
	if zpool list -H -o name bootpool >/dev/null 2>&1 && 	    ! zfs list -H -o name bootpool >/dev/null 2>&1; then
		zpool export bootpool 2>/dev/null || true
	fi
	if zfs list -H -o name bootpool >/dev/null 2>&1; then
		zfs mount bootpool 2>/dev/null || true
		return 0
	fi
	i=0
	while [ "$i" -lt 10 ]; do
		if zpool import bootpool 2>/dev/null; then
			return 0
		fi
		i=$((i + 1))
		sleep 1
	done
	return 0
}

load_rc_config "$name"
: ${bootpool_enable:="NO"}
run_rc_command "$1"
`
