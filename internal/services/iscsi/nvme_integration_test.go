// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const iscsiTestNQNPrefix = "nqn.2026-10.io.sylve:test-"

type integrationNVMeAssociations struct {
	XMLName     xml.Name                     `xml:"ctlnvmflist"`
	Connections []integrationNVMeAssociation `xml:"connection"`
}

type integrationNVMeAssociation struct {
	ID        int    `xml:"id,attr"`
	Host      string `xml:"hostnqn"`
	Target    string `xml:"subnqn"`
	Transport int    `xml:"trtype"`
}

var integrationNVMeDevicePattern = regexp.MustCompile(`^nvme[0-9]+$`)

func integrationNVMeAssociationsFor(ctx context.Context, svc *Service) (*integrationNVMeAssociations, error) {
	out, err := svc.runTargetCommand(ctx, "", "/usr/sbin/ctladm", "nvlist", "-x")
	if err != nil {
		return nil, errors.New("cannot inspect native NVMe associations")
	}
	var list integrationNVMeAssociations
	if xml.Unmarshal([]byte(out), &list) != nil {
		return nil, errors.New("invalid native NVMe association inventory")
	}
	return &list, nil
}

func (f *iscsiIntegrationFixture) ownsNVMeController(name string) bool {
	for _, controller := range f.manifest.NVMeControllers {
		if controller.NQN == name && strings.HasPrefix(name, iscsiTestNQNPrefix+f.manifest.RunID+":") {
			return true
		}
	}
	return false
}

func nvmeIdentifyField(text, field string) string {
	result, found := "", false
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == field {
			if found {
				return ""
			}
			result, found = strings.TrimSpace(value), true
		}
	}
	return result
}

func nvmeDeviceAtEndpoint(text, device, endpoint string) bool {
	matches := 0
	for _, line := range strings.Split(text, "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || key != device {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(line)), " (connected via tcp "+strings.ToLower(endpoint)+")") {
			return false
		}
		matches++
	}
	return matches == 1
}

func nvmeControllerIdentity(text string) (string, int, bool) {
	serial := nvmeIdentifyField(text, "Serial Number")
	id, err := strconv.ParseUint(nvmeIdentifyField(text, "Controller ID"), 0, 16)
	return serial, int(id), serial != "" && err == nil
}

func (f *iscsiIntegrationFixture) nvmeDevicePaths() ([]string, error) {
	if f.nvmeDevices != nil {
		return f.nvmeDevices()
	}
	return filepath.Glob("/dev/nvme[0-9]*")
}

func (f *iscsiIntegrationFixture) checkNVMeNamespace(ctx context.Context) error {
	list, err := integrationNVMeAssociationsFor(ctx, f.service)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, connection := range list.Connections {
		if seen[connection.Target] {
			return errors.New("ambiguous NVMe associations appeared; left untouched")
		}
		seen[connection.Target] = true
		owned := false
		for _, controller := range f.manifest.NVMeControllers {
			if connection.Target == controller.NQN && connection.Host == controller.HostNQN && connection.Transport == 3 && f.ownsNVMeController(controller.NQN) &&
				(controller.ControllerID == nil || connection.ID == *controller.ControllerID) {
				owned = true
			}
		}
		if !owned {
			return errors.New("foreign NVMe association appeared; left untouched")
		}
	}
	devices, err := f.nvmeDevicePaths()
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return nil
	}
	inventory, err := f.service.runTargetCommand(ctx, "", "/sbin/nvmecontrol", "devlist")
	if err != nil {
		return errors.New("cannot verify NVMe transport endpoints; left untouched")
	}
	for _, path := range devices {
		device := filepath.Base(path)
		if !integrationNVMeDevicePattern.MatchString(device) {
			continue
		}
		out, err := f.service.runTargetCommand(ctx, "", "/sbin/nvmecontrol", "identify", device)
		if err != nil {
			return errors.New("cannot verify NVMe controller identity; left untouched")
		}
		serial, id, valid := nvmeControllerIdentity(out)
		owned := false
		for _, controller := range f.manifest.NVMeControllers {
			if valid && nvmeIdentifyField(out, "NVM Subsystem Name") == controller.NQN && f.ownsNVMeController(controller.NQN) &&
				(controller.Device == "" || controller.Device == device) && (controller.ControllerID == nil || id == *controller.ControllerID) &&
				(controller.Serial == "" || serial == controller.Serial) && nvmeDeviceAtEndpoint(inventory, device, controller.Endpoint) {
				for _, connection := range list.Connections {
					if connection.ID == id && connection.Target == controller.NQN && connection.Host == controller.HostNQN && connection.Transport == 3 {
						owned = true
					}
				}
			}
		}
		if !owned {
			return errors.New("foreign or unverified NVMe controller appeared; left untouched")
		}
	}
	return nil
}

func TestISCSIFixtureNVMeOwnershipAndDisconnect(t *testing.T) {
	for _, test := range []struct {
		name   string
		unsafe bool
		mount  string
		swap   string
		change func(*integrationNVMeController, *integrationNVMeAssociation, *string, *string, *[]string)
	}{
		{name: "owned"},
		{name: "mounted namespace", unsafe: true, mount: "/dev/nvme0ns2 /mnt ufs rw 1 1\n"},
		{name: "swap namespace", unsafe: true, swap: "/dev/nvme0ns2 16384 0 16384 0%\n"},
		{name: "mounted CAM namespace", unsafe: true, mount: "/dev/nda0 /mnt ufs rw 1 1\n"},
		{name: "foreign target", unsafe: true, change: func(_ *integrationNVMeController, a *integrationNVMeAssociation, _, _ *string, _ *[]string) {
			a.Target = iscsiTestNQNPrefix + "other:extra"
		}},
		{name: "foreign host", unsafe: true, change: func(_ *integrationNVMeController, a *integrationNVMeAssociation, _, _ *string, _ *[]string) {
			a.Host = iscsiTestNQNPrefix + "other:host"
		}},
		{name: "wrong transport", unsafe: true, change: func(_ *integrationNVMeController, a *integrationNVMeAssociation, _, _ *string, _ *[]string) {
			a.Transport = 1
		}},
		{name: "replaced association", unsafe: true, change: func(_ *integrationNVMeController, a *integrationNVMeAssociation, _, _ *string, _ *[]string) { a.ID++ }},
		{name: "foreign endpoint", unsafe: true, change: func(_ *integrationNVMeController, _ *integrationNVMeAssociation, inventory, _ *string, _ *[]string) {
			*inventory = "nvme0: CTL (connected via TCP 127.0.0.1:49223)\n"
		}},
		{name: "controller id changed", unsafe: true, change: func(_ *integrationNVMeController, _ *integrationNVMeAssociation, _, identity *string, _ *[]string) {
			*identity = strings.Replace(*identity, "0x000a", "0x000b", 1)
		}},
		{name: "serial changed", unsafe: true, change: func(_ *integrationNVMeController, _ *integrationNVMeAssociation, _, identity *string, _ *[]string) {
			*identity = strings.Replace(*identity, "owned-serial", "other-serial", 1)
		}},
		{name: "ambiguous controller identity", unsafe: true, change: func(_ *integrationNVMeController, _ *integrationNVMeAssociation, _, identity *string, _ *[]string) {
			*identity += "Controller ID: 0x000a\n"
		}},
		{name: "nqn changed", unsafe: true, change: func(_ *integrationNVMeController, _ *integrationNVMeAssociation, _, identity *string, _ *[]string) {
			*identity = strings.Replace(*identity, "ownership-unit:extra", "other:extra", 1)
		}},
		{name: "device changed", unsafe: true, change: func(c *integrationNVMeController, _ *integrationNVMeAssociation, _, _ *string, _ *[]string) {
			c.Device = "nvme1"
		}},
		{name: "missing association", unsafe: true, change: func(_ *integrationNVMeController, _ *integrationNVMeAssociation, _, _ *string, devices *[]string) {
			*devices = append(*devices, "/dev/nvme1")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			id := 10
			controller := integrationNVMeController{NQN: iscsiTestNQNPrefix + "ownership-unit:extra", HostNQN: iscsiTestNQNPrefix + "ownership-unit:host", Endpoint: "127.0.0.1:49222", Device: "nvme0", Namespace: "nvme0ns2", Serial: "owned-serial", ControllerID: &id}
			association := integrationNVMeAssociation{ID: id, Host: controller.HostNQN, Target: controller.NQN, Transport: 3}
			inventory := "nvme0: CTL (connected via TCP 127.0.0.1:49222)\n"
			identity := "NVM Subsystem Name: " + controller.NQN + "\nController ID: 0x000a\nSerial Number: owned-serial\n"
			devices := []string{"/dev/nvme0", "/dev/nvme0ns2"}
			if test.change != nil {
				test.change(&controller, &association, &inventory, &identity, &devices)
			}
			f := &iscsiIntegrationFixture{directory: t.TempDir(), manifest: integrationManifest{RunID: "ownership-unit", NVMeControllers: []integrationNVMeController{controller}}}
			f.nvmeDevices = func() ([]string, error) { return devices, nil }
			disconnects := 0
			f.service = &Service{runtime: &targetRuntime{run: func(_ context.Context, _, command string, args ...string) (string, error) {
				if command == "/sbin/mount" {
					return test.mount, nil
				}
				if command == "/usr/sbin/swapinfo" {
					return test.swap, nil
				}
				if command == "/usr/sbin/ctladm" {
					list := integrationNVMeAssociations{}
					if len(devices) != 0 {
						list.Connections = []integrationNVMeAssociation{association}
					}
					data, err := xml.Marshal(list)
					return string(data), err
				}
				if command == "/sbin/nvmecontrol" {
					switch args[0] {
					case "devlist":
						return inventory, nil
					case "identify":
						return identity, nil
					case "disconnect":
						disconnects++
						devices = nil
						return "", nil
					}
				}
				t.Fatalf("unexpected NVMe ownership command: %s", command)
				return "", nil
			}}}
			err := f.disconnectNVMeControllers(t.Context())
			if (err != nil) != test.unsafe || test.unsafe && disconnects != 0 || !test.unsafe && disconnects != 1 {
				t.Fatal("NVMe disconnect did not enforce recorded identity")
			}
		})
	}
}

func TestISCSIFixtureNVMeEndpointInventoryMustBeExact(t *testing.T) {
	owned := "nvme0: CTL (connected via TCP 127.0.0.1:49222)\n"
	if !nvmeDeviceAtEndpoint(owned, "nvme0", "127.0.0.1:49222") {
		t.Fatal("owned endpoint was not recognized")
	}
	for _, text := range []string{owned + owned, "nvme0: CTL (disconnected for 1 seconds)\n", "nvme0: CTL (connected via TCP 127.0.0.1:49223)\n", "nvme1: CTL (connected via TCP 127.0.0.1:49222)\n"} {
		if nvmeDeviceAtEndpoint(text, "nvme0", "127.0.0.1:49222") {
			t.Fatal("ambiguous or foreign endpoint was accepted")
		}
	}
}

func TestISCSIFixtureNVMeConnectNamespaceIdentity(t *testing.T) {
	controller := integrationNVMeController{NQN: iscsiTestNQNPrefix + "connect-unit:extra", HostNQN: iscsiTestNQNPrefix + "connect-unit:host", Endpoint: "127.0.0.1:49222"}
	f := &iscsiIntegrationFixture{directory: t.TempDir(), manifest: integrationManifest{RunID: "connect-unit", NVMeControllers: []integrationNVMeController{controller}}}
	f.nvmeDevices = func() ([]string, error) { return []string{"/dev/nvme0", "/dev/nvme0ns2"}, nil }
	f.service = &Service{runtime: &targetRuntime{run: func(_ context.Context, _, command string, args ...string) (string, error) {
		if command == "/usr/sbin/ctladm" {
			data, err := xml.Marshal(integrationNVMeAssociations{Connections: []integrationNVMeAssociation{{ID: 10, Host: controller.HostNQN, Target: controller.NQN, Transport: 3}}})
			return string(data), err
		}
		if command == "/sbin/nvmecontrol" {
			switch strings.Join(args, " ") {
			case "connect -t tcp -q " + controller.HostNQN + " " + controller.Endpoint + " " + controller.NQN:
				return "", nil
			case "devlist":
				return "nvme0: CTL (connected via TCP 127.0.0.1:49222)\n", nil
			case "identify nvme0":
				return "NVM Subsystem Name: " + controller.NQN + "\nController ID: 0x000a\nSerial Number: owned-serial\n", nil
			case "nsid nvme0ns2":
				return "nvme0\t2\n", nil
			case "identify nvme0ns2":
				return "Size:                        32768 blocks\nCurrent LBA Format:          LBA Format #00\nLBA Format #00: Data Size:   512  Metadata Size:     0  Performance: Best\n", nil
			}
		}
		t.Fatalf("unexpected native NVMe command: %s %v", command, args)
		return "", nil
	}}}
	connected := f.connectNVMe(t, controller)
	if connected.Device != "nvme0" || connected.Namespace != "nvme0ns2" || connected.Serial != "owned-serial" || connected.ControllerID == nil || *connected.ControllerID != 10 {
		t.Fatalf("native NVMe namespace identity was not recorded: %+v", connected)
	}
}

func (f *iscsiIntegrationFixture) disconnectNVMeControllers(ctx context.Context) error {
	for i, controller := range f.manifest.NVMeControllers {
		if err := f.checkNVMeNamespace(ctx); err != nil {
			return err
		}
		devices, err := f.nvmeDevicePaths()
		if err != nil {
			return err
		}
		for _, path := range devices {
			device := filepath.Base(path)
			if !integrationNVMeDevicePattern.MatchString(device) {
				continue
			}
			out, err := f.service.runTargetCommand(ctx, "", "/sbin/nvmecontrol", "identify", device)
			if err != nil {
				return errors.New("cannot confirm owned NVMe controller before disconnect")
			}
			if nvmeIdentifyField(out, "NVM Subsystem Name") != controller.NQN {
				continue
			}
			serial, id, valid := nvmeControllerIdentity(out)
			if !valid {
				return errors.New("cannot record NVMe identity before disconnect")
			}
			controller.Device, controller.Namespace, controller.Serial, controller.ControllerID = device, device+"ns2", serial, &id
			f.manifest.NVMeControllers[i] = controller
			if err := f.saveManifest(); err != nil {
				return errors.New("cannot save NVMe identity before disconnect")
			}
			if err := f.checkNVMeNamespace(ctx); err != nil {
				return err
			}
			if err := f.checkNVMeDiskUse(ctx, device); err != nil {
				return err
			}
			if _, err := f.service.runTargetCommand(ctx, "", "/sbin/nvmecontrol", "disconnect", device); err != nil {
				return errors.New("owned NVMe disconnect failed")
			}
		}
	}
	_, interval := f.service.targetTimings()
	for len(f.manifest.NVMeControllers) != 0 {
		list, err := integrationNVMeAssociationsFor(ctx, f.service)
		if err != nil {
			return err
		}
		devices, err := f.nvmeDevicePaths()
		if err != nil {
			return err
		}
		if len(list.Connections) == 0 && len(devices) == 0 {
			return nil
		}
		if err := waitTarget(ctx, interval); err != nil {
			return errors.New("owned NVMe detach pending")
		}
	}
	return nil
}

func TestIntegrationISCSIExtraConfigNVMeTCP(t *testing.T) {
	if testing.Short() {
		t.Skip("requires real FreeBSD NVMe/TCP; skipped in short mode")
	}
	for _, tool := range []string{"/sbin/nvmecontrol", "/usr/sbin/ctladm"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("NVMe/TCP native tool missing: %s", tool)
		}
	}
	if _, err := os.Stat("/dev/nvmf"); err != nil {
		t.Fatal("NVMe/TCP integration requires nvmf, nvmft, and nvmf_tcp kernel modules")
	}
	devices, err := filepath.Glob("/dev/nvme[0-9]*")
	if err != nil || len(devices) != 0 {
		t.Fatal("NVMe/TCP test requires a host without existing NVMe controllers; left untouched")
	}
	f := requireISCSIIntegrationFixture(t)
	ctx, cancel := f.service.targetContext()
	list, err := integrationNVMeAssociationsFor(ctx, f.service)
	cancel()
	if err != nil || len(list.Connections) != 0 {
		t.Fatal("NVMe/TCP target support missing or foreign associations exist")
	}
	managedEndpoint := f.endpoint(t)
	managed := f.target(t, "nvme-managed", managedEndpoint, "None", "", "", "", "")
	f.initiator(t, managed, managedEndpoint, "nvme-managed", "None", "", "", "", "")
	endpoint := f.endpoint(t)
	controller := integrationNVMeController{NQN: iscsiTestNQNPrefix + f.manifest.RunID + ":extra", HostNQN: iscsiTestNQNPrefix + f.manifest.RunID + ":host", Endpoint: endpoint}
	f.manifest.NVMeControllers = append(f.manifest.NVMeControllers, controller)
	f.manifest.Targets = append(f.manifest.Targets, controller.NQN)
	backing := f.fileBacking(t, "nvme-extra")
	extra := "transport-group user-nvme { listen tcp " + endpoint + " } controller " + nativeQuote(controller.NQN) + " { transport-group user-nvme auth-group no-authentication namespace 1 { path " + nativeQuote(backing) + " serial nvme-owned } }"
	if _, err := f.service.SetExtraTargetConfig(&extra); err != nil {
		t.Fatalf("save native NVMe controller: %v", err)
	}
	f.record(t)
	controller = f.connectNVMe(t, controller)
	f.nvmeIO(t, controller, backing, 'V')
	f.io(t, managed, managedEndpoint, 'M')
	if err := f.service.UpdateTarget(managed.ID, managed.TargetName, "managed alias", "None", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = f.service.targetContext()
	err = f.disconnectNVMeControllers(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	controller = integrationNVMeController{NQN: controller.NQN, HostNQN: controller.HostNQN, Endpoint: controller.Endpoint}
	f.manifest.NVMeControllers[0] = controller
	if err := f.saveManifest(); err != nil {
		t.Fatal(err)
	}
	controller = f.connectNVMe(t, controller)
	f.disconnect(t, managed, managedEndpoint)
	if err := f.service.WriteConfig(true); err != nil {
		t.Fatal(err)
	}
	f.nvmeIO(t, controller, backing, 'W')
	f.io(t, managed, managedEndpoint, 'N')
	ctx, cancel = f.service.targetContext()
	err = f.disconnectNVMeControllers(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	clear := ""
	if _, err := f.service.SetExtraTargetConfig(&clear); err != nil {
		t.Fatal(err)
	}
	f.record(t)
	f.io(t, managed, managedEndpoint, 'P')
}

func (f *iscsiIntegrationFixture) connectNVMe(t *testing.T, controller integrationNVMeController) integrationNVMeController {
	t.Helper()
	endpoint := controller.Endpoint
	if _, err := integrationCommand(f.service, "/sbin/nvmecontrol", "connect", "-t", "tcp", "-q", controller.HostNQN, endpoint, controller.NQN); err != nil {
		t.Fatal("native NVMe connection failed; check host UUID and native logs")
	}
	waitIntegration(t, "owned NVMe namespace identity", func(ctx context.Context) bool {
		if err := f.checkNVMeNamespace(ctx); err != nil {
			t.Fatal(err)
		}
		list, err := integrationNVMeAssociationsFor(ctx, f.service)
		if err != nil || len(list.Connections) != 1 || list.Connections[0].Target != controller.NQN || list.Connections[0].Host != controller.HostNQN {
			return false
		}
		devices, err := f.nvmeDevicePaths()
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range devices {
			device := filepath.Base(path)
			if !integrationNVMeDevicePattern.MatchString(device) {
				continue
			}
			out, err := f.service.runTargetCommand(ctx, "", "/sbin/nvmecontrol", "identify", device)
			if err != nil || nvmeIdentifyField(out, "NVM Subsystem Name") != controller.NQN {
				continue
			}
			serial, id, valid := nvmeControllerIdentity(out)
			if !valid || list.Connections[0].ID != id {
				return false
			}
			namespace := device + "ns2"
			nsid, err := f.service.runTargetCommand(ctx, "", "/sbin/nvmecontrol", "nsid", namespace)
			if err != nil || strings.TrimSpace(nsid) != device+"\t2" {
				return false
			}
			out, err = f.service.runTargetCommand(ctx, "", "/sbin/nvmecontrol", "identify", namespace)
			if err != nil || nvmeIdentifyField(out, "Size") != "32768 blocks" || nvmeIdentifyField(out, "Current LBA Format") != "LBA Format #00" || !strings.Contains(out, "Data Size:   512") {
				return false
			}
			controller.Device, controller.Namespace = device, namespace
			controller.Serial, controller.ControllerID = serial, &id
			f.manifest.NVMeControllers[0] = controller
			if err := f.saveManifest(); err != nil {
				t.Fatal(err)
			}
			return true
		}
		return false
	})
	return controller
}

func (f *iscsiIntegrationFixture) checkNVMeDiskUse(ctx context.Context, device string) error {
	for _, command := range []string{"/sbin/mount", "/usr/sbin/swapinfo"} {
		args := []string{"-p"}
		if command == "/usr/sbin/swapinfo" {
			args = []string{"-k"}
		}
		out, err := f.service.runTargetCommand(ctx, "", command, args...)
		if err != nil {
			return errors.New("cannot check NVMe disk use; left untouched")
		}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) != 0 && (strings.HasPrefix(fields[0], "/dev/"+device) || strings.HasPrefix(fields[0], "/dev/nda")) {
				return errors.New("NVMe disk is mounted or used as swap; left untouched")
			}
		}
	}
	return nil
}

func (f *iscsiIntegrationFixture) nvmeIO(t *testing.T, controller integrationNVMeController, backing string, patternByte byte) {
	t.Helper()
	if !f.ownsNVMeController(controller.NQN) || !integrationNVMeDevicePattern.MatchString(controller.Device) || controller.Namespace != controller.Device+"ns2" {
		t.Fatal("unrecorded NVMe namespace; refusing I/O")
	}
	if controller.ControllerID == nil || controller.Serial == "" {
		t.Fatal("NVMe controller identity was not recorded; refusing I/O")
	}
	ownedBacking := false
	for _, file := range f.manifest.FileBackings {
		if file.Path == backing && f.ownsFileBacking(file) {
			ownedBacking = true
		}
	}
	if !ownedBacking {
		t.Fatal("unrecorded NVMe backing; refusing I/O")
	}
	ctx, cancel := f.service.targetContext()
	defer cancel()
	if err := f.checkOwnedNamespace(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := f.service.runTargetCommand(ctx, "", "/sbin/nvmecontrol", "identify", controller.Device)
	if err != nil || nvmeIdentifyField(out, "NVM Subsystem Name") != controller.NQN {
		t.Fatal("NVMe controller identity changed; refusing I/O")
	}
	nsid, err := f.service.runTargetCommand(ctx, "", "/sbin/nvmecontrol", "nsid", controller.Namespace)
	if err != nil || strings.TrimSpace(nsid) != controller.Device+"\t2" {
		t.Fatal("NVMe namespace identity changed; refusing I/O")
	}
	if err := f.checkNVMeDiskUse(ctx, controller.Device); err != nil {
		t.Fatal(err)
	}
	text, err := f.service.GenerateTargetConfig()
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.service.prepareTargetConfig(ctx, text)
	if err == nil {
		_, err = f.service.checkTargetRuntime(ctx, state)
	}
	if err != nil {
		t.Fatal("namespace mapping or backing identity does not match")
	}
	data := strings.Repeat(string([]byte{patternByte}), 4096)
	pattern := filepath.Join(f.directory, "pattern")
	if err := os.WriteFile(pattern, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationCommand(f.service, "/sbin/nvmecontrol", "io-passthru", "-o", "1", "-w", "-l", "4096", "-i", pattern, "-4", "64", "-6", "7", controller.Namespace); err != nil {
		t.Fatal("owned NVMe namespace write failed")
	}
	if _, err := integrationCommand(f.service, "/sbin/nvmecontrol", "io-passthru", "-o", "0", controller.Namespace); err != nil {
		t.Fatal("owned NVMe namespace flush failed")
	}
	out, err = integrationCommand(f.service, "/sbin/nvmecontrol", "io-passthru", "-o", "2", "-b", "-r", "-l", "4096", "-4", "64", "-6", "7", controller.Namespace)
	if err != nil || out != data {
		t.Fatal("owned NVMe namespace read failed")
	}
	out, err = integrationCommand(f.service, "/bin/dd", "if="+backing, "bs=4096", "count=1", "skip=8")
	if err != nil || out != data {
		t.Fatal("NVMe I/O did not match the owned namespace backing")
	}
}
