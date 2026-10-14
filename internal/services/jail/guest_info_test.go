// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"gorm.io/gorm"
)

const jailGuestNetworkFixture = `{"statistics":{"interface":[
	{"name":"lo0","network":"<Link#1>","address":"lo0","received-bytes":0,"sent-bytes":0},
	{"name":"lo0","network":"127.0.0.0/8","address":"127.0.0.1","received-bytes":50,"sent-bytes":50},
	{"name":"abcde_net7b","network":"<Link#2>","address":"02:00:00:00:00:07","received-bytes":1024,"sent-bytes":2048},
	{"name":"abcde_net7b","network":"192.0.2.0/24","address":"192.0.2.100","received-bytes":500,"sent-bytes":600},
	{"name":"abcde_net7b","network":"2001:db8::/64","address":"2001:db8::100"},
	{"name":"abcde_net7b","network":"fe80::%abcde_net7b/64","address":"fe80::1%abcde_net7b"},
	{"name":"abcde_net7b","network":"192.0.2.0/24","address":"192.0.2.100"}
]}}`

func jailGuestRuntimeFixture(t *testing.T, runtime jailGuestRuntime) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"jail-information": map[string]any{"jail": []map[string]string{{
			"jid": runtime.JID.String(), "name": runtime.Name, "path": runtime.Path,
			"vnet": runtime.VNET, "ip4": runtime.IPv4, "ip6": runtime.IPv6,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func newJailGuestInfoTestService(t *testing.T) (*Service, jailGuestRuntime) {
	t.Helper()
	root := t.TempDir()
	service, jail := newResolvedJailRootTestService(t, 9306, root)
	if err := service.DB.Model(jail).Update("type", jailModels.JailTypeFreeBSD).Error; err != nil {
		t.Fatal(err)
	}
	service.ctidHashByCTID = map[uint]string{jail.CTID: "abcde"}
	if err := os.Mkdir(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "freebsd-version"), []byte("#!/bin/sh\nUSERLAND_VERSION=\"14.3-RELEASE-p2\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	runtime := jailGuestRuntime{JID: json.Number("47"), Name: "abcde", Path: root, VNET: "new", IPv4: "new", IPv6: "new"}
	service.guestInfoRunFn = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > jailGuestInfoTimeout {
			t.Fatal("guest inspection must have a bounded deadline")
		}
		switch command {
		case "/usr/sbin/jls":
			want := []string{"--libxo", "json", "-j", "abcde", "jid", "name", "path", "vnet", "ip4", "ip6"}
			if !slices.Equal(args, want) {
				t.Fatalf("jls arguments = %v, want %v", args, want)
			}
			return jailGuestRuntimeFixture(t, runtime), nil
		case "/usr/bin/uname":
			if !slices.Equal(args, []string{"-rm"}) {
				t.Fatalf("uname arguments = %v", args)
			}
			return []byte("15.0-RELEASE amd64\n"), nil
		case "/usr/bin/netstat":
			want := []string{"-j", "47", "-ibdn", "--libxo", "json"}
			if !slices.Equal(args, want) {
				t.Fatalf("netstat must use the runtime JID, not CTID: %v", args)
			}
			return []byte(jailGuestNetworkFixture), nil
		default:
			t.Fatalf("unexpected guest inspection command %q", command)
			return nil, errors.New("unexpected command")
		}
	}
	return service, runtime
}

func TestGetJailGuestInfoCollectsUserlandAndRuntimeNetworking(t *testing.T) {
	service, _ := newJailGuestInfoTestService(t)
	info, err := service.GetJailGuestInfo(context.Background(), 9306)
	if err != nil || !info.Available || info.NetworkMode != "vnet" || info.OSInfo == nil {
		t.Fatalf("guest info = %+v, err=%v", info, err)
	}
	if info.OSInfo.Version != "14.3-RELEASE-p2" || info.OSInfo.KernelRelease != "15.0-RELEASE" || info.OSInfo.Architecture != "amd64" {
		t.Fatalf("userland and host kernel must be distinct: %+v", info.OSInfo)
	}
	if len(info.Interfaces) != 2 {
		t.Fatalf("interfaces = %+v", info.Interfaces)
	}
	loopback, iface := info.Interfaces[0], info.Interfaces[1]
	if loopback.HardwareAddress != "" || loopback.Statistics == nil || loopback.Statistics.RXBytes != 0 {
		t.Fatalf("zero counters and loopback MAC = %+v", loopback)
	}
	if iface.HardwareAddress != "02:00:00:00:00:07" || iface.Statistics == nil || iface.Statistics.RXBytes != 1024 || iface.Statistics.TXBytes != 2048 {
		t.Fatalf("interface must use guest-side link totals, not summed address rows: %+v", iface)
	}
	if len(iface.IPAddresses) != 3 || iface.IPAddresses[0].Address != "192.0.2.100" || iface.IPAddresses[0].Prefix != 24 ||
		iface.IPAddresses[2].Address != "fe80::1%abcde_net7b" || iface.IPAddresses[2].Prefix != 64 {
		t.Fatalf("live addresses must preserve prefixes, scope and deduplicate: %+v", iface.IPAddresses)
	}
}

func TestGetJailGuestInfoSkipsLinux(t *testing.T) {
	service, _ := newJailGuestInfoTestService(t)
	if err := service.DB.Model(&jailModels.Jail{}).Where("ct_id = ?", 9306).Update("type", jailModels.JailTypeLinux).Error; err != nil {
		t.Fatal(err)
	}
	service.guestInfoRunFn = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("Linux jail must not run guest inspection commands")
		return nil, nil
	}
	info, err := service.GetJailGuestInfo(context.Background(), 9306)
	if err != nil || info.Available || info.OSInfo != nil || info.Interfaces == nil {
		t.Fatalf("Linux guest info = %+v, err=%v", info, err)
	}
}

func TestGetJailGuestInfoHidesUnavailableInspection(t *testing.T) {
	for _, test := range []struct {
		name    string
		command string
		output  string
		err     error
	}{
		{name: "stopped", command: "/usr/sbin/jls", output: `{"jail-information":{"jail":[]}}`},
		{name: "missing jls", command: "/usr/sbin/jls", err: exec.ErrNotFound},
		{name: "missing network tool", command: "/usr/bin/netstat", err: exec.ErrNotFound},
		{name: "timeout", command: "/usr/bin/netstat", err: context.DeadlineExceeded},
		{name: "malformed network data", command: "/usr/bin/netstat", output: `{"statistics":{}}`},
		{name: "malformed kernel data", command: "/usr/bin/uname", output: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newJailGuestInfoTestService(t)
			run := service.guestInfoRunFn
			service.guestInfoRunFn = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == test.command {
					return []byte(test.output), test.err
				}
				return run(ctx, command, args...)
			}
			info, err := service.GetJailGuestInfo(context.Background(), 9306)
			if err != nil || info.Available || info.OSInfo != nil || len(info.Interfaces) != 0 || info.NetworkMode != "" {
				t.Fatalf("unavailable guest info = %+v, err=%v", info, err)
			}
		})
	}
}

func TestGetJailGuestInfoValidatesRuntimeIdentity(t *testing.T) {
	for _, scenario := range []string{"wrong name", "wrong root", "invalid JID", "unknown VNET", "restart", "stop during inspection"} {
		t.Run(scenario, func(t *testing.T) {
			service, runtime := newJailGuestInfoTestService(t)
			run := service.guestInfoRunFn
			lookups := 0
			service.guestInfoRunFn = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command != "/usr/sbin/jls" {
					return run(ctx, command, args...)
				}
				lookups++
				current := runtime
				switch scenario {
				case "wrong name":
					current.Name = "another-jail"
				case "wrong root":
					current.Path = filepath.Join(runtime.Path, "other")
				case "invalid JID":
					current.JID = json.Number("0")
				case "unknown VNET":
					current.VNET = ""
				case "restart":
					if lookups > 1 {
						current.JID = json.Number("48")
					}
				case "stop during inspection":
					if lookups > 1 {
						return nil, errors.New("jail not found")
					}
				}
				return jailGuestRuntimeFixture(t, current), nil
			}
			info, err := service.GetJailGuestInfo(context.Background(), 9306)
			if err != nil || info.Available {
				t.Fatalf("changed/mismatched runtime = %+v, err=%v", info, err)
			}
		})
	}
}

func TestGetJailGuestInfoSharedAndDisabledNetworks(t *testing.T) {
	for _, test := range []struct {
		name string
		ipv4 string
		mode string
	}{
		{name: "inherited IPv4", ipv4: "inherit", mode: "shared"},
		{name: "disabled", ipv4: "disable", mode: "disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, runtime := newJailGuestInfoTestService(t)
			runtime.VNET, runtime.IPv4, runtime.IPv6 = "inherit", test.ipv4, "disable"
			run := service.guestInfoRunFn
			service.guestInfoRunFn = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == "/usr/sbin/jls" {
					return jailGuestRuntimeFixture(t, runtime), nil
				}
				if test.mode == "disabled" && command == "/usr/bin/netstat" {
					t.Fatal("disabled networking must not query host interfaces")
				}
				return run(ctx, command, args...)
			}
			info, err := service.GetJailGuestInfo(context.Background(), 9306)
			if err != nil || !info.Available || info.NetworkMode != test.mode {
				t.Fatalf("guest info = %+v, err=%v", info, err)
			}
			if test.mode == "disabled" {
				if len(info.Interfaces) != 0 {
					t.Fatalf("disabled network = %+v", info)
				}
				return
			}
			for _, iface := range info.Interfaces {
				if iface.Statistics != nil {
					t.Fatal("shared interfaces must not expose host traffic as jail counters")
				}
				for _, ip := range iface.IPAddresses {
					if ip.Type != "ipv4" {
						t.Fatalf("disabled IPv6 family must not be reported: %+v", ip)
					}
				}
			}
		})
	}
}

func TestGetJailGuestInfoValidatesServiceAndMissingJail(t *testing.T) {
	if _, err := (*Service)(nil).GetJailGuestInfo(context.Background(), 1); err == nil {
		t.Fatal("nil service must fail")
	}
	service, _ := newJailGuestInfoTestService(t)
	if _, err := service.GetJailGuestInfo(context.Background(), 0); err == nil {
		t.Fatal("zero CTID must fail")
	}
	if _, err := service.GetJailGuestInfo(context.Background(), 9999); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing jail error = %v", err)
	}
}

func TestReadJailGuestVersionReadsOnlyLiteralMetadata(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		version string
	}{
		{name: "release", content: "USERLAND_VERSION=\"14.3-RELEASE-p2\"\n", version: "14.3-RELEASE-p2"},
		{name: "current", content: "USERLAND_VERSION='16.0-CURRENT'\n", version: "16.0-CURRENT"},
		{name: "missing literal", content: "#!/bin/sh\necho 14.3-RELEASE\n"},
		{name: "substitution", content: "USERLAND_VERSION=\"$(touch marker)\"\n"},
		{name: "duplicate", content: "USERLAND_VERSION='14.3-RELEASE'\nUSERLAND_VERSION='15.0-RELEASE'\n"},
		{name: "oversized", content: strings.Repeat("x", jailGuestVersionLimit+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "bin"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "bin", "freebsd-version"), []byte(test.content), 0755); err != nil {
				t.Fatal(err)
			}
			version, err := readJailGuestVersion(root)
			if test.version == "" {
				if err == nil {
					t.Fatalf("unsafe/unsupported version file accepted: %q", version)
				}
			} else if err != nil || version != test.version {
				t.Fatalf("version = %q, err=%v", version, err)
			}
		})
	}
}

func TestReadJailGuestVersionRejectsEscapingSymlinksAndSpecialFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "missing"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "bin"), 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "bin", "freebsd-version")
			switch kind {
			case "symlink":
				outside := filepath.Join(t.TempDir(), "freebsd-version")
				if err := os.WriteFile(outside, []byte("USERLAND_VERSION='14.3-RELEASE'\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if version, err := readJailGuestVersion(root); err == nil || version != "" {
				t.Fatalf("%s accepted: version=%q, err=%v", kind, version, err)
			}
		})
	}
}

func TestJailGuestCommandBoundsOutputAndCancellation(t *testing.T) {
	var output jailGuestOutput
	if _, err := output.Write(make([]byte, jailGuestOutputLimit)); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("x")); err == nil || output.buffer.Len() != jailGuestOutputLimit {
		t.Fatal("command output must not grow beyond its limit")
	}
	var copied jailGuestOutput
	reader := io.LimitReader(strings.NewReader(strings.Repeat("x", jailGuestOutputLimit+1)), jailGuestOutputLimit+1)
	if _, err := io.Copy(&copied, reader); err == nil || copied.buffer.Len() > jailGuestOutputLimit {
		t.Fatal("io.Copy must not bypass the command output limit")
	}
	command, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Service{}).runJailGuestCommand(ctx, command, "10"); !errors.Is(err, context.Canceled) {
		t.Fatalf("command cancellation = %v", err)
	}
}

func TestParseJailGuestInterfacesRejectsMalformedPrefixes(t *testing.T) {
	runtime := jailGuestRuntime{VNET: "new", IPv4: "new", IPv6: "new"}
	for _, network := range []string{"192.0.2", "192.0.2.0/33", "192.0.2.0/-1", "192.0.2.0/invalid"} {
		data := `{"statistics":{"interface":[{"name":"epair0b","network":"` + network + `","address":"192.0.2.100"}]}}`
		if _, err := parseJailGuestInterfaces([]byte(data), runtime); err == nil {
			t.Fatalf("invalid prefix accepted: %q", network)
		}
	}
}

func TestIntegrationJailGuestInfo(t *testing.T) {
	if testing.Short() || runtime.GOOS != "freebsd" || os.Geteuid() != 0 {
		t.Skip("requires FreeBSD root and a non-short test run")
	}
	for _, binary := range []string{"/usr/sbin/jail", "/usr/sbin/jls", "/usr/bin/netstat", "/sbin/ifconfig"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("required tool unavailable: %v", err)
		}
	}
	for _, mode := range []string{"vnet", "shared", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			service, fixture := newJailGuestInfoTestService(t)
			service.guestInfoRunFn = nil
			name := "sylve_test_guest_info_" + strconv.Itoa(os.Getpid()) + "_" + strconv.FormatInt(time.Now().UnixNano(), 16)
			service.ctidHashByCTID[9306] = name
			args := []string{"-c", "name=" + name, "path=" + fixture.Path, "host.hostname=" + name, "persist"}
			switch mode {
			case "vnet":
				args = append(args, "vnet")
			case "shared":
				args = append(args, "ip4=inherit", "ip6=disable")
			case "disabled":
				args = append(args, "ip4=disable", "ip6=disable")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, "/usr/sbin/jail", args...).CombinedOutput()
			if err != nil {
				t.Fatalf("create owned test jail: %v: %s", err, output)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if output, err := exec.CommandContext(ctx, "/usr/sbin/jail", "-r", name).CombinedOutput(); err != nil {
					t.Errorf("remove owned test jail %s: %v: %s", name, err, output)
				}
			})
			if mode == "vnet" {
				output, err := exec.CommandContext(ctx, "/sbin/ifconfig", "-j", name, "-n", "lo0", "inet", "127.0.0.1/8", "up").CombinedOutput()
				if err != nil {
					t.Fatalf("configure isolated test loopback: %v: %s", err, output)
				}
			}
			info, err := service.GetJailGuestInfo(ctx, 9306)
			if err != nil || !info.Available || info.NetworkMode != mode {
				t.Fatalf("native guest collection = %+v, err=%v", info, err)
			}
			if info.OSInfo.Version != "14.3-RELEASE-p2" || info.OSInfo.KernelRelease == "" {
				t.Fatalf("native OS collection = %+v", info.OSInfo)
			}
			if mode == "vnet" && (len(info.Interfaces) != 1 || info.Interfaces[0].Name != "lo0" || info.Interfaces[0].Statistics == nil || len(info.Interfaces[0].IPAddresses) == 0) {
				t.Fatalf("native isolated network collection = %+v", info.Interfaces)
			}
			for _, iface := range info.Interfaces {
				if mode == "shared" && iface.Statistics != nil {
					t.Fatal("native shared host counters must be omitted")
				}
			}
		})
	}
}
