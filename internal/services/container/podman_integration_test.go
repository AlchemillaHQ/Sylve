// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/services/startup"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"github.com/alchemillahq/sylve/pkg/utils/sysctl"
)

const integrationImage = "docker.io/library/alpine:3.20"

func TestIntegrationContainerLifecycleRealPodman(t *testing.T) {
	if testing.Short() || os.Geteuid() != 0 {
		t.Skip("requires root on FreeBSD")
	}
	if _, err := os.Stat(podmanBinary); err != nil {
		t.Skip("podman is not installed")
	}
	pool, client := zfstest.DedicatedPool(t)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(runDir, pool)) })
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &networkModels.StandardSwitch{})
	if err := containerModels.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO sqlite_sequence (name, seq) VALUES ('container_networks', 9000)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.BasicSettings{Pools: []string{pool}, Services: []models.AvailableService{models.Containers, models.Firewall}}).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db, GZFS: client, DataDir: t.TempDir(), PrepareHost: (&startup.Service{}).PrepareContainerHost}
	t.Cleanup(func() { service.ReleasePool(pool) })
	ctx := t.Context()
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		networks, _ := service.ListNetworks(context.Background())
		for _, network := range networks {
			_ = service.DeleteNetwork(context.Background(), network.ID)
		}
	})
	nat, err := service.CreateNetwork(ctx, containerServiceInterfaces.NetworkRequest{Name: "itest-nat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := net.InterfaceByName(nat.Bridge); err == nil {
		t.Fatalf("refusing to share the host's existing bridge %s", nat.Bridge)
	}

	execOutput := func(ocid uint, args ...string) string {
		t.Helper()
		cmd, err := service.Command(ctx, ocid, append([]string{"exec", "{}"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("exec %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	waitRunning := func(ocid uint) {
		t.Helper()
		for range 50 {
			if detail, _ := service.Get(ctx, ocid); detail.Runtime != nil && detail.Runtime.State == "running" {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("container did not start", ocid)
	}

	rules := func(runtimeID string) string {
		output, _ := exec.Command("/usr/bin/rctl", rctlSubject(runtimeID)).CombinedOutput()
		return string(output)
	}
	create := containerServiceInterfaces.CreateRequest{OCID: 9101, Name: "web", Pool: pool, StartAfterCreate: true, Cores: 1, Memory: 256 << 20,
		Spec: containerServiceInterfaces.Spec{Image: integrationImage, OS: "linux", Network: nat.Name, Command: []string{"sh", "-c", "echo hello; exec sleep 600"},
			Env:         []containerModels.EnvVar{{Name: "TOKEN", Value: "not-on-the-command-line"}},
			Ports:       []containerModels.Port{{HostPort: 18080, ContainerPort: 80}},
			Mounts:      []containerModels.Mount{{Type: containerModels.MountVolume, Source: "data", Target: "/data", Owner: "1000:1001", Mode: "750"}},
			Annotations: map[string]string{"org.freebsd.jail.allow.mlock": "true"},
			ExtraHosts:  []string{"db.internal:192.0.2.5"}, ShmSize: "64m"}}
	enabled, _ := sysctl.GetInt64("kern.racct.enable")
	racct := enabled == 1
	if !racct {
		if err := service.Create(ctx, create); err == nil || !strings.Contains(err.Error(), "container_limits_require_racct") {
			t.Fatal("limits without racct must be rejected", err)
		}
		create.Cores, create.Memory = 0, 0
	}
	if err := service.Create(ctx, create); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Delete(context.Background(), 9101, true) })
	waitRunning(9101)
	listed := func(ocid uint) string {
		t.Helper()
		items, err := service.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.OCID == ocid {
				return item.State
			}
		}
		return "missing"
	}
	if state := listed(9101); state != "running" {
		t.Fatal("a running container must be listed as running, not", state)
	}

	if logs, err := service.Logs(ctx, 9101, 10); err != nil || !strings.Contains(logs, "hello") {
		t.Fatal("unexpected logs", logs, err)
	}
	if got := execOutput(9101, "sh", "-c", "echo $TOKEN"); got != "not-on-the-command-line" {
		t.Fatal("environment not passed", got)
	}
	if output, _ := exec.Command("ps", "-axww", "-o", "command").Output(); strings.Contains(string(output), "not-on-the-command-line") {
		t.Fatal("environment value visible in the process list")
	}
	detail, err := service.Get(ctx, 9101)
	if err != nil || detail.Runtime == nil || detail.Runtime.State != "running" || len(detail.Runtime.IPs) != 1 {
		t.Fatal("unexpected detail", detail.Runtime, err)
	}
	if got := execOutput(9101, "sh", "-c", "stat -c %u:%g:%a /data; grep db.internal /etc/hosts"); !strings.HasPrefix(got, "1000:1001:750\n192.0.2.5") {
		t.Fatal("volume owner or extra hosts missing", got)
	}
	if got, _ := exec.Command("jls", "-j", detail.RuntimeID, "allow.mlock").Output(); strings.TrimSpace(string(got)) != "true" {
		t.Fatal("jail annotations not applied", string(got))
	}
	execOutput(9101, "sh", "-c", "echo kept > /data/file")
	if got := rules(detail.RuntimeID); racct && (!strings.Contains(got, "pcpu:deny=100") || !strings.Contains(got, "memoryuse:deny=268435456")) {
		t.Fatal("limits not applied", got)
	}
	if cpu, memory, err := usage(ctx, detail.Container); racct && (err != nil || memory <= 0 || cpu < 0) {
		t.Fatal("usage unavailable", cpu, memory, err)
	}

	spec := containerServiceInterfaces.SpecOf(detail.Container)
	spec.Env = []containerModels.EnvVar{{Name: "TOKEN", Value: "updated"}}
	if err := service.Update(ctx, 9101, spec); err != nil {
		t.Fatal(err)
	}
	waitRunning(9101)
	if got := execOutput(9101, "sh", "-c", "echo $TOKEN; cat /data/file"); got != "updated\nkept" {
		t.Fatal("update lost state", got)
	}
	updated, _ := service.Get(ctx, 9101)
	if strings.Contains(rules(detail.RuntimeID), "deny") || racct && !strings.Contains(rules(updated.RuntimeID), "memoryuse:deny") {
		t.Fatal("limits did not follow the recreated container")
	}

	bridge, err := exec.Command("ifconfig", "bridge", "create").Output()
	if err != nil {
		t.Fatal(err)
	}
	bridgeName := strings.TrimSpace(string(bridge))
	t.Cleanup(func() { _ = exec.Command("ifconfig", bridgeName, "destroy").Run() })
	if err := db.Create(&networkModels.StandardSwitch{Name: "itest", BridgeName: bridgeName}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateNetwork(ctx, containerServiceInterfaces.NetworkRequest{Name: "itest", Switch: "itest",
		Subnet: "192.0.2.0/24", Gateway: "192.0.2.1", RangeStart: "192.0.2.100", RangeEnd: "192.0.2.150"}); err != nil {
		t.Fatal(err)
	}
	lan := containerServiceInterfaces.CreateRequest{OCID: 9102, Name: "lan", Pool: pool, StartAfterCreate: true,
		Spec: containerServiceInterfaces.Spec{Image: integrationImage, OS: "linux", Network: "itest", IP: "192.0.2.120", Command: []string{"sleep", "600"}}}
	if err := service.Create(ctx, lan); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Delete(context.Background(), 9102, false) })
	waitRunning(9102)
	lanDetail, err := service.Get(ctx, 9102)
	if err != nil || lanDetail.Runtime == nil || !slices.Contains(lanDetail.Runtime.IPs, "192.0.2.120") {
		t.Fatal("static switch address missing", lanDetail.Runtime, err)
	}
	if routes, err := exec.Command("netstat", "-j", lanDetail.RuntimeID, "-rn", "-f", "inet").CombinedOutput(); err != nil || !strings.Contains(string(routes), "192.0.2.1") {
		t.Fatalf("switch gateway route missing: %v: %s", err, routes)
	}
	if output, _ := exec.Command("ifconfig", bridgeName).Output(); strings.Contains(string(output), "192.0.2.1") {
		t.Fatal("switch bridge must not take the gateway address")
	}

	installBuiltinCatalog(t, service.DataDir)
	plan, err := service.RenderCatalogApp(ctx, "daemonless", "redis", containerServiceInterfaces.CatalogRenderRequest{})
	if err != nil || len(plan.Containers) != 1 {
		t.Fatal(plan, err)
	}
	redis := plan.Containers[0]
	redis.OCID, redis.Pool, redis.Spec.Network, redis.Spec.Ports[0].HostPort = 9103, pool, nat.Name, 16379
	var reports sync.Map
	report := func(message string) { reports.Store(message, true) }
	if err := service.Create(context.WithValue(ctx, progressKey{}, report), redis); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Delete(context.Background(), 9103, true) })
	downloaded := false
	reports.Range(func(message, _ any) bool {
		downloaded = downloaded || strings.HasPrefix(message.(string), "Pulling "+redis.Spec.Image+": ") &&
			strings.HasSuffix(message.(string), " downloaded")
		return true
	})
	if !downloaded {
		t.Fatal("a pull must report how much has downloaded")
	}
	if downloads, err := service.downloads(ctx, pool); err != nil {
		t.Fatal(err)
	} else if left, _ := os.ReadDir(downloads); len(left) > 0 {
		t.Fatal("a pull's downloads must be removed", left)
	}

	record, _ := service.record(ctx, 9101)
	consoles := func() int {
		output, _ := exec.Command("/bin/ps", "-e", "-J", record.RuntimeID, "-o", "command=").Output()
		return strings.Count(string(output), consoleEnv+"=")
	}
	one, err := service.Console(ctx, 9101, "/bin/sh", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	two, err := service.Console(ctx, 9101, "/bin/sh", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	output := make(chan string, 64)
	go func() {
		buffer := make([]byte, 1024)
		for {
			n, err := one.Read(buffer)
			output <- string(buffer[:n])
			if err != nil {
				close(output)
				return
			}
		}
	}()
	expect := func(command, answer string) {
		_, _ = one.Write([]byte(command + "\n"))
		var seen string
		timeout := time.After(10 * time.Second)
		for !strings.Contains(seen, answer) {
			select {
			case chunk, ok := <-output:
				if !ok {
					t.Fatalf("console ended before %q answered %q: %q", command, answer, seen)
				}
				seen += chunk
			case <-timeout:
				t.Fatalf("%q didn't answer %q: %q", command, answer, seen)
			}
		}
	}
	expect("stty size", "24 80")
	if err := one.Resize(40, 120); err != nil {
		t.Fatal(err)
	}
	expect("sleep 600 & stty size", "40 120")
	if count := consoles(); count != 3 {
		t.Fatal("expected two shells and a sleep from the consoles, found", count)
	}
	_ = one.Close()
	time.Sleep(500 * time.Millisecond)
	if count := consoles(); count != 1 {
		t.Fatal("closing a console must end its shell and what it started, and only those; left", count)
	}
	service.endConsole(ctx, record, "")
	time.Sleep(500 * time.Millisecond)
	if count := consoles(); count != 0 || listed(9101) != "running" {
		t.Fatal("at startup every console ends, and the container keeps running; left", count)
	}

	busybox := func(name string, command ...string) containerServiceInterfaces.CreateRequest {
		return containerServiceInterfaces.CreateRequest{Name: name, StartAfterCreate: true,
			Spec: containerServiceInterfaces.Spec{Image: "docker.io/library/busybox:1.36", OS: "linux", Command: command}}
	}
	prepareStack := func(name string, ocid uint, containers ...containerServiceInterfaces.CreateRequest) containerServiceInterfaces.StackRequest {
		stack, err := service.PrepareStack(ctx, containerServiceInterfaces.StackRequest{Stack: name, Pool: pool, OCID: ocid, Containers: containers})
		if err != nil {
			t.Fatal(err)
		}
		for _, create := range stack.Containers {
			t.Cleanup(func() { _ = service.Delete(context.Background(), create.OCID, true) })
		}
		return stack
	}

	kept := busybox("itfailed-web", "sleep", "600")
	kept.Mounts = []containerModels.Mount{{Type: containerModels.MountVolume, Source: "data", Target: "/data"}}
	missing := busybox("itfailed-db", "sleep", "600")
	missing.Image = "docker.io/library/busybox:no-such-tag"
	failed := prepareStack("itfailed", 9120, kept, missing)
	if err := service.createStack(ctx, failed); err == nil || !strings.HasPrefix(err.Error(), "container_stack_failed: itfailed-db: podman_pull_failed") {
		t.Fatal("a stack fails with the container that could not be created", err)
	}
	if _, err := service.record(ctx, 9120); err == nil {
		t.Fatal("the stack's created container is deleted again")
	}
	if dataset, _ := client.ZFS.Get(ctx, containerModels.VolumesDataset(pool, 9120), false); dataset != nil {
		t.Fatal("the volumes a failed stack made are left behind")
	}
	if _, err := service.networkByName(ctx, "itfailed"); err == nil {
		t.Fatal("a failed stack's network is left behind")
	}

	stack := prepareStack("itstack", 9110,
		busybox("itstack-web", "sh", "-c", "mkdir -p /www && echo stacked > /www/index.html && exec httpd -f -p 80 -h /www"),
		busybox("itstack-client", "sleep", "600"))
	stackNetwork, err := service.networkByName(ctx, "itstack")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := net.InterfaceByName(stackNetwork.Bridge); err == nil {
		t.Fatalf("refusing to share the host's existing bridge %s", stackNetwork.Bridge)
	}
	if err := service.createStack(ctx, stack); err != nil {
		t.Fatal(err)
	}
	for _, create := range stack.Containers {
		waitRunning(create.OCID)
	}
	stackWeb, stackClient := stack.Containers[0].OCID, stack.Containers[1].OCID
	if page := execOutput(stackClient, "wget", "-q", "-T", "5", "-O", "-", "http://itstack-web/"); page != "stacked" {
		t.Fatal("a stack's containers reach each other by name", page)
	}
	if err := service.Delete(ctx, stackWeb, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.networkByName(ctx, "itstack"); err != nil {
		t.Fatal("the stack's network stays while it has containers", err)
	}
	if err := service.Delete(ctx, stackClient, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.networkByName(ctx, "itstack"); err == nil {
		t.Fatal("the stack's network goes with its last container")
	}
	if _, err := net.InterfaceByName(stackNetwork.Bridge); err == nil {
		t.Fatal("the stack's bridge is left behind", stackNetwork.Bridge)
	}

	interrupted := prepareStack("itcrash", 9130, busybox("itcrash-web", "sleep", "600"), busybox("itcrash-db", "sleep", "600"))
	if err := service.Create(ctx, interrupted.Containers[0]); err != nil {
		t.Fatal(err)
	}
	pending := interrupted.Containers[1]
	if err := service.DB.Create(&containerModels.Container{OCID: pending.OCID, Name: pending.Name, Pool: pool,
		Image: pending.Image, Stack: "itcrash", CreationPending: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	for _, create := range interrupted.Containers {
		if _, err := service.record(ctx, create.OCID); err == nil {
			t.Fatal("an interrupted stack's container is left behind", create.Name)
		}
	}
	if _, err := service.networkByName(ctx, "itcrash"); err == nil {
		t.Fatal("an interrupted stack's network is left behind")
	}

	if cmd, err := service.Command(ctx, 9101, "exec", "--detach", "{}", "sleep", "600"); err != nil {
		t.Fatal(err)
	} else if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("detached exec: %v: %s", err, output)
	}
	crashed, _ := service.record(ctx, 9101)
	if pid := mainPID(crashed); pid == 0 || syscall.Kill(pid, syscall.SIGKILL) != nil {
		t.Fatal("no main process to kill", pid)
	}
	for range 50 {
		if listed(9101) == "stopped" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if state := listed(9101); state != "stopped" {
		t.Fatal("a container whose main process exited must be listed as stopped, not", state)
	}
	if err := service.Action(ctx, 9101, "restart"); err != nil {
		t.Fatal("restarting a container left to its exec sessions:", err)
	}
	waitRunning(9101)

	current, _ := service.Get(ctx, 9101)
	for _, action := range []string{"restart", "stop"} {
		cmd, err := service.Command(ctx, 9101, "exec", "--detach", "{}", "sleep", "600")
		if err != nil {
			t.Fatal(err)
		}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("detached exec before %s: %v: %s", action, err, output)
		}
		if err := service.Action(ctx, 9101, action); err != nil {
			t.Fatalf("%s with an exec session: %v", action, err)
		}
	}
	for _, ocid := range []uint{9102, 9103} {
		if err := service.Action(ctx, ocid, "stop"); err != nil {
			t.Fatal(err)
		}
	}
	if output, err := exec.Command("jls", "-j", current.RuntimeID, "jid").CombinedOutput(); err == nil {
		t.Fatalf("the stopped container's jail is left behind: %s", output)
	}
	if state := listed(9101); state != "stopped" {
		t.Fatal("a stopped container must be listed as stopped, not", state)
	}
	for _, ocid := range []uint{9102, 9103} {
		if err := service.Delete(ctx, ocid, ocid == 9103); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.Delete(ctx, 9101, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rules(updated.RuntimeID), "deny") {
		t.Fatal("limits left behind after delete")
	}
	if dataset, _ := client.ZFS.Get(ctx, containerModels.VolumesDataset(pool, 9101), false); dataset != nil {
		t.Fatal("volumes were not deleted")
	}
	networks, _ := service.ListNetworks(ctx)
	for _, network := range networks {
		if err := service.DeleteNetwork(ctx, network.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.PruneImages(ctx, pool); err != nil {
		t.Fatal(err)
	}
}
