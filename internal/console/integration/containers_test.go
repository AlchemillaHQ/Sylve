// SPDX-License-Identifier: BSD-2-Clause

//go:build freebsd

package integration

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/assets"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
)

const (
	consoleContainerBusybox = "docker.io/library/busybox:1.36"
	consoleContainerHost    = "198.18.91.1"
	consoleContainerClient  = "198.18.91.2"
)

func TestAcceptanceContainersCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	if err := suite.container.Reconcile(t.Context()); err != nil {
		t.Fatalf("prepare the container host: %v", err)
	}
	if rules, _ := exec.Command("pfctl", "-sn").CombinedOutput(); !strings.Contains(string(rules), `rdr-anchor "cni-rdr/*"`) {
		t.Fatalf("pf must load the <cni-nat> table and cni-rdr anchors, as the Sylve firewall does:\n%s", rules)
	}
	if local, _ := exec.Command("sysctl", "-n", "net.pf.filter_local").Output(); strings.TrimSpace(string(local)) != "1" {
		t.Fatal("pf must filter local delivery for the host to reach published ports, as the Sylve firewall has it with containers: set net.pf.filter_local=1, then enable pf")
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, ocid := range []uint{9203, 9202, 9201} {
			_ = suite.container.Delete(ctx, ocid, true)
		}
		networks, _ := suite.container.ListNetworks(ctx)
		for _, network := range networks {
			_ = suite.container.DeleteNetwork(ctx, network.ID)
		}
		_ = os.RemoveAll(filepath.Join("/var/run/sylve/podman", suite.poolName))
	})
	catalog, err := fs.Sub(assets.CatalogFiles, "catalog")
	if err == nil {
		err = os.CopyFS(filepath.Join(suite.container.DataDir, "catalog"), catalog)
	}
	if err != nil {
		t.Fatalf("install the built-in catalog: %v", err)
	}
	addConsoleStackApp(t, filepath.Join(suite.container.DataDir, "catalog", "daemonless"))

	containers := func(args ...string) string {
		t.Helper()
		return strings.TrimSpace(runSylve(t, suite.binaryPath, suite.configPath, append([]string{"containers"}, args...)...))
	}
	task := func(action string, args ...string) {
		t.Helper()
		output := containers(append([]string{action, "--json"}, args...)...)
		var result struct {
			TaskID uint `json:"taskId"`
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil || result.TaskID == 0 {
			t.Fatalf("container %s result: %v\n%s", action, err, output)
		}
		waitForConsoleLifecycleTask(t, suite, result.TaskID)
	}
	detail := func(ocid string) containerServiceInterfaces.Detail {
		t.Helper()
		var detail containerServiceInterfaces.Detail
		if output := containers("get", "--ocid", ocid, "--json"); json.Unmarshal([]byte(output), &detail) != nil {
			t.Fatalf("decode container %s: %s", ocid, output)
		}
		return detail
	}
	running := func(ocid string) bool {
		t.Helper()
		detail := detail(ocid)
		return detail.Runtime != nil && detail.Runtime.State == "running"
	}
	inside := func(ocid string, args ...string) string {
		t.Helper()
		return containers(append([]string{"exec", "--ocid", ocid, "--"}, args...)...)
	}
	expect := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %q, want %q", what, got, want)
		}
	}
	if err := suite.database.Exec("INSERT INTO sqlite_sequence (name, seq) VALUES ('container_networks', 9000)").Error; err != nil {
		t.Fatal(err)
	}
	network := "it-" + suite.runID
	containers("networks", "create", "--name", network)
	var networks []containerModels.Network
	if output := containers("networks", "list", "--json"); json.Unmarshal([]byte(output), &networks) != nil || len(networks) != 1 {
		t.Fatalf("networks:\n%s", output)
	}
	if _, err := net.InterfaceByName(networks[0].Bridge); err == nil {
		t.Fatalf("refusing to share the host's existing bridge %s", networks[0].Bridge)
	}
	lan := consoleContainerLANClient(t, suite.runID)

	if output := containers("catalog", "list", "--search", "redis"); !strings.Contains(output, "daemonless/redis") {
		t.Fatalf("catalog list:\n%s", output)
	}
	if output := containers("catalog", "show", "--app", "daemonless/redis"); !strings.Contains(output, "WEB_PORT") ||
		!strings.Contains(output, "containers create --app daemonless/redis") {
		t.Fatalf("catalog show:\n%s", output)
	}
	var template containerServiceInterfaces.CatalogTemplate
	if output := containers("catalog", "show", "--app", "daemonless/redis", "--json"); json.Unmarshal([]byte(output), &template) != nil {
		t.Fatalf("catalog show --json:\n%s", output)
	}
	var redisImage string
	for _, variant := range template.App.Variants {
		if variant.Default {
			redisImage = variant.Image
		}
	}
	for image, system := range map[string]string{consoleContainerBusybox: "linux", redisImage: "freebsd"} {
		containers("images", "pull", "--pool", suite.poolName, "--image", image, "--os", system)
	}

	task("create", "--ocid", "9201", "--name", "smoke", "--pool", suite.poolName, "--network", network,
		"--image", consoleContainerBusybox, "--os", "linux",
		"--description", "Sylve acceptance", "--start-order", "5", "--hostname", "smokeweb", "--workdir", "/www", "--stop-timeout", "5",
		"--port", "18081:80", "--volume", "www:/www", "--volume-owner", "1000:1000",
		"--env", "GREETING=hello-sylve", "--env", "LIST=a,b", "--add-host", "db.internal:192.0.2.5",
		"--annotation", "org.freebsd.jail.allow.mlock=true", "--start",
		"--", "sh", "-c", `echo "$GREETING" > /www/index.html; exec httpd -f -p 80 -h /www`)
	web := detail("9201")
	if !running("9201") || web.Description != "Sylve acceptance" || web.StartOrder != 5 || web.StopTimeout != 5 {
		t.Fatalf("created container = %#v", web)
	}
	expect("hostname", inside("9201", "hostname"), "smokeweb")
	expect("working directory", inside("9201", "pwd"), "/www")
	expect("environment with commas", inside("9201", "sh", "-c", "echo $LIST"), "a,b")
	expect("volume owner", inside("9201", "stat", "-c", "%u:%g", "/www"), "1000:1000")
	expect("extra host", inside("9201", "awk", "/db.internal/ { print $1 }", "/etc/hosts"), "192.0.2.5")
	allowed, _ := exec.Command("jls", "-j", web.RuntimeID, "allow.mlock").Output()
	expect("jail annotation", strings.TrimSpace(string(allowed)), "true")
	var used []containerServiceInterfaces.UsedPort
	if output := containers("ports", "--json"); json.Unmarshal([]byte(output), &used) != nil ||
		!slices.Contains(used, containerServiceInterfaces.UsedPort{Protocol: "tcp", Port: 18081, By: "container smoke"}) {
		t.Errorf("published port not listed as used:\n%s", output)
	}
	expect("published port", lan("fetch -qo - http://"+consoleContainerHost+":18081/", "hello-sylve"), "hello-sylve")
	expect("published port from the host", consoleRunner()("fetch -qo - http://127.0.0.1:18081/", "hello-sylve"), "hello-sylve")
	if page := inside("9201", "wget", "-q", "-T", "10", "-O", "-", "http://example.com"); !strings.Contains(strings.ToLower(page), "example domain") {
		t.Errorf("outbound HTTP through NAT:\n%s", page)
	}

	task("create", "--ocid", "9202", "--name", "client", "--pool", suite.poolName, "--network", network,
		"--image", consoleContainerBusybox, "--os", "linux", "--start", "--", "sleep", "600")
	expect("container DNS", inside("9202", "wget", "-q", "-T", "5", "-O", "-", "http://smoke/"), "hello-sylve")

	inside("9201", "sh", "-c", "echo kept > /www/kept.txt")
	task("update", "--ocid", "9201", "--unset-env", "LIST", "--remove-port", "18081", "--port", "18082:80")
	if !running("9201") {
		t.Fatal("container not running after update")
	}
	expect("volume after update", lan("fetch -qo - http://"+consoleContainerHost+":18082/kept.txt", "kept"), "kept")
	expect("removed port", lan("fetch -T 3 -qo - http://"+consoleContainerHost+":18081/ 2>/dev/null || echo closed", "closed"), "closed")
	expect("removed environment", inside("9201", "sh", "-c", "echo ${LIST:-unset}"), "unset")

	task("create", "--app", "daemonless/redis", "--ocid", "9203", "--pool", suite.poolName, "--network", network,
		"--set", "WEB_PORT=16380")
	redis := detail("9203")
	if redis.Name != "redis" || redis.Image != redisImage || redis.CatalogApp != "daemonless/redis" {
		t.Fatalf("catalog container = %#v", redis.Container)
	}
	expect("catalog app answers", lan(`printf 'PING\r\n' | nc -w 2 `+consoleContainerHost+` 16380 | tr -d '\r'`, "+PONG"), "+PONG")
	expect("catalog volume owner", inside("9203", "stat", "-f", "%u:%g", "/config"), "1000:1000")

	containers("networks", "create", "--name", "itstack")
	if output := containers("networks", "list", "--json"); json.Unmarshal([]byte(output), &networks) != nil || len(networks) != 2 {
		t.Fatalf("networks:\n%s", output)
	}
	for _, stackNetwork := range networks {
		if _, err := net.InterfaceByName(stackNetwork.Bridge); stackNetwork.Name == "itstack" && err == nil {
			t.Fatalf("refusing to share the host's existing bridge %s", stackNetwork.Bridge)
		}
	}
	var stack struct {
		TaskID     uint `json:"taskId"`
		Containers []struct {
			OCID uint   `json:"ocId"`
			Name string `json:"name"`
		} `json:"containers"`
	}
	if output := containers("create", "--app", "daemonless/itstack", "--ocid", "9210", "--pool", suite.poolName, "--json"); json.Unmarshal([]byte(output), &stack) != nil ||
		stack.TaskID == 0 || len(stack.Containers) != 2 {
		t.Fatalf("stack create:\n%s", output)
	}
	waitForConsoleLifecycleTask(t, suite, stack.TaskID)
	for _, member := range stack.Containers {
		if got := detail(strconv.Itoa(int(member.OCID))); got.Stack != "itstack" || got.Network == nil || got.Network.Name != "itstack" {
			t.Fatalf("stack container %s = %#v", member.Name, got.Container)
		}
	}
	decoder := json.NewDecoder(strings.NewReader(containers("delete", "--ocid", "9210", "--stack", "--json")))
	for deleted := 0; ; deleted++ {
		var result struct {
			TaskID uint `json:"taskId"`
		}
		if err := decoder.Decode(&result); err != nil {
			if deleted != 2 {
				t.Fatalf("stack delete queued %d tasks: %v", deleted, err)
			}
			break
		}
		waitForConsoleLifecycleTask(t, suite, result.TaskID)
	}

	for _, step := range []struct {
		action  string
		running bool
	}{{"stop", false}, {"start", true}, {"restart", true}, {"kill", false}} {
		task(step.action, "--ocid", "9201")
		if running("9201") != step.running {
			t.Fatalf("container running after %s = %t", step.action, !step.running)
		}
	}
	containers("logs", "--ocid", "9201", "--tail", "5")

	task("delete", "--ocid", "9202")
	task("delete", "--ocid", "9203", "--delete-volumes")
	task("delete", "--ocid", "9201", "--delete-volumes")
	for _, ocid := range []uint{9201, 9203} {
		assertConsoleZFSDataset(t, containerModels.VolumesDataset(suite.poolName, ocid), false)
	}
	var left []containerServiceInterfaces.ListItem
	if output := containers("list", "--json"); json.Unmarshal([]byte(output), &left) != nil || len(left) != 0 {
		t.Fatalf("containers left after deletion:\n%s", output)
	}
}

func consoleContainerLANClient(t *testing.T, runID string) func(command, want string) string {
	t.Helper()
	addresses, _ := net.InterfaceAddrs()
	for _, address := range addresses {
		if strings.HasPrefix(address.String(), "198.18.91.") {
			t.Fatalf("refusing to use 198.18.91.0/30, already on the host as %s", address)
		}
	}
	output, err := exec.Command("ifconfig", "epair", "create").Output()
	if err != nil {
		t.Fatalf("create epair: %v", err)
	}
	host := strings.TrimSpace(string(output))
	t.Cleanup(func() { _ = exec.Command("ifconfig", host, "destroy").Run() })
	jail, peer := "it-lan-"+runID, strings.TrimSuffix(host, "a")+"b"
	for _, command := range [][]string{
		{"ifconfig", host, "inet", consoleContainerHost + "/30", "up"},
		{"jail", "-c", "name=" + jail, "path=/", "vnet", "vnet.interface=" + peer, "persist"},
		{"jexec", jail, "ifconfig", peer, "inet", consoleContainerClient + "/30", "up"},
	} {
		if output, err := exec.Command(command[0], command[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(command, " "), err, output)
		}
		if command[0] == "jail" {
			t.Cleanup(func() { _ = exec.Command("jail", "-r", jail).Run() })
		}
	}
	return consoleRunner("jexec", jail)
}

func consoleRunner(prefix ...string) func(command, want string) string {
	return func(command, want string) string {
		var got string
		for range 20 {
			args := append(slices.Clone(prefix), "sh", "-c", command)
			output, _ := exec.Command(args[0], args[1:]...).CombinedOutput()
			if got = strings.TrimSpace(string(output)); got == want {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		return got
	}
}

func addConsoleStackApp(t *testing.T, catalog string) {
	t.Helper()
	manifest := "name: itstack\nservices:\n  web:\n    image: " + consoleContainerBusybox + "\n  db:\n    image: " + consoleContainerBusybox +
		"\nx-fjord:\n  version: \"0.1\"\n  info:\n    id: itstack\n    name: Acceptance stack\n    class: stack\n"
	if err := os.WriteFile(filepath.Join(catalog, "manifests", "itstack.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(catalog, "catalog.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var index map[string]any
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	index["apps"] = append(index["apps"].([]any), map[string]any{"id": "itstack", "name": "Acceptance stack", "class": "stack",
		"os": []string{"linux"}, "manifest_url": "manifests/itstack.yaml"})
	if data, err = json.Marshal(index); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
