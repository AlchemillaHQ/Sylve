//go:build freebsd

package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	sylve "github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	consolepath "github.com/alchemillahq/sylve/internal/console"
	database "github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/repl"
	infoService "github.com/alchemillahq/sylve/internal/services/info"
	jailService "github.com/alchemillahq/sylve/internal/services/jail"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
	lifecycleService "github.com/alchemillahq/sylve/internal/services/lifecycle"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	systemService "github.com/alchemillahq/sylve/internal/services/system"
	utilitiesService "github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

const (
	consoleIntegrationPoolPrefix = "sylve-console-it-"
	consoleIntegrationPoolMarker = "org.alchemillahq:sylve_console_test_run"
	consoleIntegrationVdevSize   = int64(8 * 1024 * 1024 * 1024)
	// The sparse vdev can consume its full logical size; reserve additional room
	// for pkgbase downloads and bootstrap work outside the pool.
	consoleIntegrationBootstrapReserve = int64(1 * 1024 * 1024 * 1024)
)

type consoleIntegrationSuite struct {
	runID        string
	root         string
	dataPath     string
	configPath   string
	binaryPath   string
	socketPath   string
	poolName     string
	vdevPath     string
	mountPath    string
	poolLinkPath string
	poolCreated  bool

	database       *gorm.DB
	telemetryDB    *gorm.DB
	network        *networkService.Service
	jail           *jailService.Service
	virtualMachine *libvirtService.Service
	lifecycle      *lifecycleService.Service
	utilities      *utilitiesService.Service
	socket         *repl.SocketServer
	queueCancel    context.CancelFunc
	queueDone      chan struct{}
}

var integrationSuite *consoleIntegrationSuite

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}

	suite, err := newConsoleIntegrationSuite()
	if err != nil {
		fmt.Fprintf(os.Stderr, "console integration fixture setup failed: %v\n", err)
		os.Exit(1)
	}
	integrationSuite = suite

	code := m.Run()
	if err := suite.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "console integration fixture cleanup failed: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func TestConsoleIntegrationSuiteFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	if got := zfsPropertyValue(t, consoleIntegrationPoolMarker, suite.poolName); got != suite.runID {
		t.Fatalf("pool ownership marker = %q, want %q", got, suite.runID)
	}
	if target, err := os.Readlink(suite.poolLinkPath); err != nil || target != suite.mountPath {
		t.Fatalf("suite pool path target = %q, err = %v, want %q", target, err, suite.mountPath)
	}

	var settings models.BasicSettings
	if err := suite.database.First(&settings).Error; err != nil {
		t.Fatalf("load fixture basic settings: %v", err)
	}
	if len(settings.Pools) != 1 || settings.Pools[0] != suite.poolName {
		t.Fatalf("fixture pools = %v, want [%s]", settings.Pools, suite.poolName)
	}

	if output := runREPLCommand(t, suite.socketPath, "ping"); strings.TrimSpace(output) != "pong" {
		t.Fatalf("REPL ping output = %q, want pong", output)
	}

	output := runSylve(t, suite.binaryPath, suite.configPath, "notes", "list", "--json")
	var notes []infoModels.Note
	if err := json.Unmarshal([]byte(output), &notes); err != nil {
		t.Fatalf("decode fixture CLI notes list: %v\noutput: %s", err, output)
	}
	if len(notes) != 0 {
		t.Fatalf("fixture notes = %#v, want none", notes)
	}
}

func TestConsoleIntegrationFreeSpaceRequirement(t *testing.T) {
	required := uint64(consoleIntegrationVdevSize + consoleIntegrationBootstrapReserve)
	if err := validateConsoleIntegrationFreeSpace(required); err != nil {
		t.Fatalf("exact free-space requirement rejected: %v", err)
	}
	if err := validateConsoleIntegrationFreeSpace(required - 1); err == nil {
		t.Fatal("insufficient free space was accepted")
	}
}

func requireConsoleIntegrationSuite(t *testing.T) *consoleIntegrationSuite {
	t.Helper()
	if integrationSuite == nil {
		t.Fatal("console integration suite is not initialized")
	}
	return integrationSuite
}

func newConsoleIntegrationSuite() (_ *consoleIntegrationSuite, err error) {
	if err := requireConsoleIntegrationHost(); err != nil {
		return nil, err
	}

	runID, err := consoleIntegrationRunID()
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp("", "sylve-console-it-")
	if err != nil {
		return nil, fmt.Errorf("create suite directory: %w", err)
	}

	suite := &consoleIntegrationSuite{
		runID:     runID,
		root:      root,
		dataPath:  filepath.Join(root, "data"),
		poolName:  consoleIntegrationPoolPrefix + runID,
		vdevPath:  filepath.Join(root, "vdev"),
		mountPath: filepath.Join(root, "pool"),
	}
	suite.poolLinkPath = filepath.Join("/", suite.poolName)
	defer func() {
		if err == nil {
			return
		}
		if cleanupErr := suite.cleanupFailedSetup(); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()

	if err := os.MkdirAll(suite.dataPath, 0755); err != nil {
		return nil, fmt.Errorf("create suite data directory: %w", err)
	}
	if err := os.MkdirAll(suite.mountPath, 0755); err != nil {
		return nil, fmt.Errorf("create suite pool mount directory: %w", err)
	}
	if err := suite.createPool(); err != nil {
		return nil, err
	}
	if err := suite.configure(); err != nil {
		return nil, err
	}
	return suite, nil
}

func requireConsoleIntegrationHost() error {
	if runtime.GOOS != "freebsd" {
		return fmt.Errorf("must run on FreeBSD, got %s", runtime.GOOS)
	}
	if os.Geteuid() != 0 {
		return errors.New("must run as root; use make test-integration on the dedicated FreeBSD host")
	}
	if err := requireConsoleIntegrationFreeSpace(os.TempDir()); err != nil {
		return err
	}

	output, err := exec.Command("uname", "-s").CombinedOutput()
	if err != nil {
		return fmt.Errorf("verify host kernel: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if strings.TrimSpace(string(output)) != "FreeBSD" {
		return fmt.Errorf("must run on a FreeBSD kernel, got %q", strings.TrimSpace(string(output)))
	}

	for _, command := range []string{"zpool", "zfs", "pkg", "jail", "ifconfig", "route", "bhyve", "virsh", "kldstat", "service"} {
		if _, err := exec.LookPath(command); err != nil {
			return fmt.Errorf("required command %q is unavailable: %w", command, err)
		}
	}
	keyDir := "/usr/share/keys/pkgbase-15/trusted"
	if info, err := os.Stat(keyDir); err != nil {
		return fmt.Errorf("required pkgbase signing keys %s are unavailable: %w", keyDir, err)
	} else if !info.IsDir() {
		return fmt.Errorf("required pkgbase signing keys path %s is not a directory", keyDir)
	}
	if output, err := exec.Command("pkg", "-N").CombinedOutput(); err != nil {
		return fmt.Errorf("pkg is not ready: %w: %s", err, strings.TrimSpace(string(output)))
	}
	for _, group := range []string{"bridge", "epair"} {
		if output, err := exec.Command("ifconfig", "-g", group).CombinedOutput(); err != nil {
			return fmt.Errorf("required interface group %q is unavailable: %w: %s", group, err, strings.TrimSpace(string(output)))
		}
	}
	if output, err := exec.Command("kldstat", "-n", "vmm").CombinedOutput(); err != nil {
		return fmt.Errorf("bhyve vmm kernel module is not ready: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("service", "libvirtd", "onestatus").CombinedOutput(); err != nil {
		return fmt.Errorf("libvirtd is not running: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("virsh", "-c", "bhyve:///system", "version").CombinedOutput(); err != nil {
		return fmt.Errorf("bhyve libvirt connection is not ready: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func requireConsoleIntegrationFreeSpace(path string) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return fmt.Errorf("check free space for integration temporary directory %s: %w", path, err)
	}
	if stat.Bavail < 0 || stat.Bsize == 0 {
		return fmt.Errorf("invalid free-space statistics for integration temporary directory %s", path)
	}

	availableBlocks := uint64(stat.Bavail)
	if availableBlocks > ^uint64(0)/stat.Bsize {
		return fmt.Errorf("free-space calculation overflow for integration temporary directory %s", path)
	}
	if err := validateConsoleIntegrationFreeSpace(availableBlocks * stat.Bsize); err != nil {
		return fmt.Errorf("integration temporary directory %s: %w", path, err)
	}
	return nil
}

func validateConsoleIntegrationFreeSpace(available uint64) error {
	required := uint64(consoleIntegrationVdevSize + consoleIntegrationBootstrapReserve)
	if available < required {
		return fmt.Errorf(
			"insufficient free space: need at least %d bytes for the %d-byte sparse vdev and %d-byte bootstrap reserve, have %d bytes",
			required,
			consoleIntegrationVdevSize,
			consoleIntegrationBootstrapReserve,
			available,
		)
	}
	return nil
}

func consoleIntegrationRunID() (string, error) {
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate suite run ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func (s *consoleIntegrationSuite) createPool() error {
	vdev, err := os.OpenFile(s.vdevPath, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create suite vdev: %w", err)
	}
	if err := vdev.Truncate(consoleIntegrationVdevSize); err != nil {
		_ = vdev.Close()
		return fmt.Errorf("size suite vdev: %w", err)
	}
	if err := vdev.Close(); err != nil {
		return fmt.Errorf("close suite vdev: %w", err)
	}

	if output, err := exec.Command("zpool", "list", "-H", "-o", "name", s.poolName).CombinedOutput(); err == nil {
		return fmt.Errorf("refusing to use existing pool %q: %s", s.poolName, strings.TrimSpace(string(output)))
	} else if !strings.Contains(strings.ToLower(string(output)), "does not exist") &&
		!strings.Contains(strings.ToLower(string(output)), "no such pool") {
		return fmt.Errorf("check whether suite pool %s exists: %w: %s", s.poolName, err, strings.TrimSpace(string(output)))
	}

	if output, err := exec.Command(
		"zpool", "create", "-f", "-o", "cachefile=none", "-m", s.mountPath, s.poolName, s.vdevPath,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("create suite pool %s: %w: %s", s.poolName, err, strings.TrimSpace(string(output)))
	}
	s.poolCreated = true
	if output, err := exec.Command(
		"zfs", "set", consoleIntegrationPoolMarker+"="+s.runID, s.poolName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("mark suite pool %s: %w: %s", s.poolName, err, strings.TrimSpace(string(output)))
	}
	if info, err := os.Lstat(s.poolLinkPath); err == nil {
		return fmt.Errorf("refusing to replace existing suite pool path %s (%s)", s.poolLinkPath, info.Mode())
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check suite pool path %s: %w", s.poolLinkPath, err)
	}
	if err := os.Symlink(s.mountPath, s.poolLinkPath); err != nil {
		return fmt.Errorf("create suite pool path %s: %w", s.poolLinkPath, err)
	}
	if output, err := exec.Command("zfs", "create", s.poolName+"/sylve").CombinedOutput(); err != nil {
		return fmt.Errorf("create suite Sylve parent dataset: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("zfs", "create", s.poolName+"/sylve/jails").CombinedOutput(); err != nil {
		return fmt.Errorf("create suite jail parent dataset: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("zfs", "create", s.poolName+"/sylve/virtual-machines").CombinedOutput(); err != nil {
		return fmt.Errorf("create suite VM parent dataset: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s *consoleIntegrationSuite) configure() error {
	cfg := &sylve.SylveConfig{
		Environment: sylve.Production,
		DataPath:    s.dataPath,
		Admin: sylve.BaseConfigAdmin{
			Email:    "console-integration@example.invalid",
			Password: "console-integration-only",
		},
		Auth: sylve.AuthConfig{EnablePAM: false},
		BTT: sylve.BTT{
			RPC: sylve.BTTRPC{Enabled: false},
			DHT: sylve.DHTConfig{Enabled: false},
		},
		ZFS: sylve.ZFSConfig{Tune: false},
	}
	config.ParsedConfig = cfg
	if err := os.Setenv("SYLVE_DATA_PATH", s.dataPath); err != nil {
		return fmt.Errorf("set suite data path: %w", err)
	}
	if err := config.SetupDataPath(); err != nil {
		return fmt.Errorf("set up suite data path: %w", err)
	}

	contents, err := json.Marshal(struct {
		DataPath string `json:"dataPath"`
	}{DataPath: s.dataPath})
	if err != nil {
		return fmt.Errorf("marshal suite CLI config: %w", err)
	}
	s.configPath = filepath.Join(s.root, "config.json")
	if err := os.WriteFile(s.configPath, contents, 0600); err != nil {
		return fmt.Errorf("write suite CLI config: %w", err)
	}

	root, err := consoleIntegrationRepositoryRoot()
	if err != nil {
		return err
	}
	s.binaryPath = filepath.Join(s.root, "sylve")
	command := exec.Command("go", "build", "-o", s.binaryPath, "./cmd/sylve")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("build suite CLI: %w: %s", err, strings.TrimSpace(string(output)))
	}

	s.database = database.SetupDatabase(cfg, false)
	s.telemetryDB = database.SetupTelemetryDatabase(cfg, s.database, false)
	if err := s.database.Create(&models.BasicSettings{
		Pools:       []string{s.poolName},
		Services:    []models.AvailableService{models.Jails, models.Virtualization},
		Initialized: true,
	}).Error; err != nil {
		return fmt.Errorf("seed suite basic settings: %w", err)
	}
	if err := database.SetupQueue(cfg, false, zerolog.New(io.Discard)); err != nil {
		return fmt.Errorf("set up suite queue: %w", err)
	}

	gzfsClient := gzfs.NewClient(gzfs.Options{Sudo: false, ZDBCacheTTLSeconds: 0})
	system := systemService.NewSystemService(s.database, gzfsClient).(*systemService.Service)
	libvirt := libvirtService.NewLibvirtService(s.database, system, gzfsClient).(*libvirtService.Service)
	s.virtualMachine = libvirt
	network := networkService.NewNetworkService(s.database, s.telemetryDB, libvirt).(*networkService.Service)
	s.network = network
	jail := jailService.NewJailService(s.database, network, system, gzfsClient).(*jailService.Service)
	s.jail = jail
	lifecycle := lifecycleService.NewService(s.database, s.telemetryDB, libvirt, jail)
	s.lifecycle = lifecycle
	utilities := utilitiesService.NewUtilitiesService(s.database, s.telemetryDB, libvirt, jail).(*utilitiesService.Service)
	s.utilities = utilities
	utilities.RegisterJobs()
	lifecycle.RegisterJobs()

	queueCtx, queueCancel := context.WithCancel(context.Background())
	s.queueCancel = queueCancel
	s.queueDone = make(chan struct{})
	go func() {
		database.StartQueue(queueCtx)
		close(s.queueDone)
	}()

	s.socketPath = consolepath.SocketPath(s.dataPath)
	info := infoService.NewInfoService(s.database, s.telemetryDB, gzfsClient).(*infoService.Service)
	s.socket, err = repl.StartSocketServer(&repl.Context{
		Info:           info,
		Jail:           jail,
		Lifecycle:      lifecycle,
		VirtualMachine: libvirt,
		Network:        network,
		Utilities:      utilities,
	}, s.socketPath)
	if err != nil {
		return fmt.Errorf("start suite console socket: %w", err)
	}
	return nil
}

func consoleIntegrationRepositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find repository root")
		}
		dir = parent
	}
}

func (s *consoleIntegrationSuite) Close() error {
	var errs []error
	if s.socket != nil {
		if err := s.socket.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close suite socket: %w", err))
		}
	}
	if s.queueCancel != nil {
		s.queueCancel()
		select {
		case <-s.queueDone:
		case <-time.After(5 * time.Second):
			errs = append(errs, errors.New("timed out stopping suite queue"))
		}
	}
	if err := s.destroyOwnedPool(); err != nil {
		errs = append(errs, err)
		return errors.Join(errs...)
	}
	if err := s.removeOwnedPoolLink(); err != nil {
		errs = append(errs, err)
		return errors.Join(errs...)
	}
	if err := os.RemoveAll(s.root); err != nil {
		errs = append(errs, fmt.Errorf("remove suite directory: %w", err))
	}
	return errors.Join(errs...)
}

func (s *consoleIntegrationSuite) cleanupFailedSetup() error {
	var errs []error
	if s.socket != nil {
		if err := s.socket.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.queueCancel != nil {
		s.queueCancel()
	}
	if err := s.destroyPoolAfterFailedSetup(); err != nil {
		errs = append(errs, err)
	}
	if err := s.removeOwnedPoolLink(); err != nil {
		errs = append(errs, err)
	}
	if err := os.RemoveAll(s.root); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *consoleIntegrationSuite) destroyOwnedPool() error {
	if s.poolName == "" {
		return nil
	}
	output, err := exec.Command("zfs", "get", "-H", "-o", "value", consoleIntegrationPoolMarker, s.poolName).CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "does not exist") {
			return nil
		}
		return fmt.Errorf("read suite pool ownership marker: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if strings.TrimSpace(string(output)) != s.runID {
		return fmt.Errorf("refusing to destroy pool %s without matching ownership marker", s.poolName)
	}
	if output, err := exec.Command("zpool", "destroy", "-f", s.poolName).CombinedOutput(); err != nil {
		return fmt.Errorf("destroy suite pool %s: %w: %s", s.poolName, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s *consoleIntegrationSuite) destroyPoolAfterFailedSetup() error {
	if !s.poolCreated {
		return nil
	}
	if output, err := exec.Command("zpool", "destroy", "-f", s.poolName).CombinedOutput(); err != nil {
		return fmt.Errorf("destroy newly-created suite pool %s: %w: %s", s.poolName, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s *consoleIntegrationSuite) removeOwnedPoolLink() error {
	if s.poolLinkPath == "" {
		return nil
	}
	target, err := os.Readlink(s.poolLinkPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read suite pool path %s: %w", s.poolLinkPath, err)
	}
	if target != s.mountPath {
		return fmt.Errorf("refusing to remove suite pool path %s with unexpected target %q", s.poolLinkPath, target)
	}
	if err := os.Remove(s.poolLinkPath); err != nil {
		return fmt.Errorf("remove suite pool path %s: %w", s.poolLinkPath, err)
	}
	return nil
}

func zfsPropertyValue(t *testing.T, property, dataset string) string {
	t.Helper()
	output, err := exec.Command("zfs", "get", "-H", "-o", "value", property, dataset).CombinedOutput()
	if err != nil {
		t.Fatalf("zfs get %s %s: %v\n%s", property, dataset, err, output)
	}
	return strings.TrimSpace(string(output))
}
