//go:build freebsd

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

type jailCreateResult struct {
	Created bool   `json:"created"`
	CTID    uint   `json:"ctId"`
	Name    string `json:"name"`
}

type jailDeleteResult struct {
	Deleted bool `json:"deleted"`
	CTID    uint `json:"ctId"`
}

func TestJailWithoutNetworkFromBootstrapIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	base := ensureConsoleBaseBootstrap(t, suite)
	ctid := consoleIntegrationJailCTID(t, suite, 1)
	name := "jail-none-" + suite.runID
	t.Cleanup(func() { cleanupConsoleJail(t, suite, ctid) })

	resourceLimits := false
	cleanEnvironment := true
	startAtBoot := false
	request := jailServiceInterfaces.CreateJailRequest{
		Name:             name,
		CTID:             &ctid,
		Hostname:         name + ".example.invalid",
		Description:      "console integration jail without a network",
		Pool:             suite.poolName,
		BootstrapName:    base.Name,
		SwitchName:       "none",
		Type:             jailModels.JailTypeFreeBSD,
		ResourceLimits:   &resourceLimits,
		CleanEnvironment: &cleanEnvironment,
		StartAtBoot:      &startAtBoot,
		ResolvConf:       "nameserver 192.0.2.53\n",
	}
	requestPath := writeConsoleJailRequest(t, suite, "none", request)

	output := runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "create", "--file", requestPath, "--json")
	var created jailCreateResult
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("decode CLI jail create: %v\noutput: %s", err, output)
	}
	if !created.Created || created.CTID != ctid || created.Name != name {
		t.Fatalf("CLI jail create result = %#v", created)
	}

	jail := consoleJailByCTID(t, suite, ctid)
	if jail.Name != name || jail.Type != jailModels.JailTypeFreeBSD || len(jail.Networks) != 0 || len(jail.Storages) != 1 {
		t.Fatalf("created jail = %#v", jail)
	}
	if jail.Storages[0].Pool != suite.poolName || !jail.Storages[0].IsBase {
		t.Fatalf("created jail storage = %#v", jail.Storages)
	}

	dataset := fmt.Sprintf("%s/sylve/jails/%d", suite.poolName, ctid)
	mountPoint := fmt.Sprintf("/%s/sylve/jails/%d", suite.poolName, ctid)
	if output, err := exec.Command("zfs", "list", "-H", "-o", "name", dataset).CombinedOutput(); err != nil {
		t.Fatalf("zfs list jail dataset %s: %v\n%s", dataset, err, output)
	}
	if _, err := os.Stat(mountPoint); err != nil {
		t.Fatalf("stat jail mount %s: %v", mountPoint, err)
	}

	configPath := filepath.Join(suite.dataPath, "jails", strconv.FormatUint(uint64(ctid), 10), strconv.FormatUint(uint64(ctid), 10)+".conf")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read jail config: %v", err)
	}
	if strings.Contains(string(config), "vnet;") || strings.Contains(string(config), "vnet.interface") {
		t.Fatalf("networkless jail config unexpectedly enables VNET:\n%s", config)
	}
	if !strings.Contains(string(config), "exec.clean;") || !strings.Contains(string(config), "persist;") {
		t.Fatalf("jail config is missing requested toggles:\n%s", config)
	}
	resolvConf, err := os.ReadFile(filepath.Join(mountPoint, "etc", "resolv.conf"))
	if err != nil {
		t.Fatalf("read jail resolv.conf: %v", err)
	}
	if string(resolvConf) != request.ResolvConf {
		t.Fatalf("jail resolv.conf = %q, want %q", resolvConf, request.ResolvConf)
	}

	output = runREPLCommand(t, suite.socketPath, "jails start "+strconv.FormatUint(uint64(ctid), 10)+" --json")
	assertJailAction(t, output, ctid, "start")
	waitForConsoleJailRunning(t, suite, ctid, true)
	waitForConsoleJailLifecycleIdle(t, suite, ctid)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "stop", "--ctid", strconv.FormatUint(uint64(ctid), 10), "--json")
	assertJailAction(t, output, ctid, "stop")
	waitForConsoleJailRunning(t, suite, ctid, false)
	waitForConsoleJailLifecycleIdle(t, suite, ctid)

	output = runREPLCommand(t, suite.socketPath,
		"jails delete "+strconv.FormatUint(uint64(ctid), 10)+" --purge --json")
	var deleted jailDeleteResult
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode REPL jail delete: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.CTID != ctid {
		t.Fatalf("REPL jail delete result = %#v", deleted)
	}
	assertConsoleJailDeleted(t, suite, ctid, dataset, configPath)
}

func TestJailWithInheritedNetworkIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	base := ensureConsoleBaseBootstrap(t, suite)
	ctid := consoleIntegrationJailCTID(t, suite, 2)
	name := "jail-inherit-" + suite.runID
	t.Cleanup(func() { cleanupConsoleJail(t, suite, ctid) })

	inheritIPv4 := true
	inheritIPv6 := true
	startAtBoot := true
	request := jailServiceInterfaces.CreateJailRequest{
		Name:          name,
		CTID:          &ctid,
		Pool:          suite.poolName,
		BootstrapName: base.Name,
		SwitchName:    "inherit",
		InheritIPv4:   &inheritIPv4,
		InheritIPv6:   &inheritIPv6,
		StartAtBoot:   &startAtBoot,
		StartOrder:    7,
		Type:          jailModels.JailTypeFreeBSD,
		MetadataMeta:  "console-integration=inherited-network",
	}
	requestPath := writeConsoleJailRequest(t, suite, "inherit", request)

	output := runREPLCommand(t, suite.socketPath, "jails create --file "+requestPath+" --json")
	var created jailCreateResult
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("decode REPL inherited jail create: %v\noutput: %s", err, output)
	}
	if !created.Created || created.CTID != ctid || created.Name != name {
		t.Fatalf("REPL inherited jail create result = %#v", created)
	}

	jail := consoleJailByCTID(t, suite, ctid)
	if !jail.InheritIPv4 || !jail.InheritIPv6 || jail.StartAtBoot == nil || !*jail.StartAtBoot || jail.StartOrder != 7 || len(jail.Networks) != 0 {
		t.Fatalf("created inherited jail = %#v", jail)
	}

	dataset := fmt.Sprintf("%s/sylve/jails/%d", suite.poolName, ctid)
	configPath := filepath.Join(suite.dataPath, "jails", strconv.FormatUint(uint64(ctid), 10), strconv.FormatUint(uint64(ctid), 10)+".conf")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read inherited jail config: %v", err)
	}
	for _, expected := range []string{"ip4=\"inherit\";", "ip6=\"inherit\";", "meta = \"console-integration=inherited-network\";"} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("inherited jail config missing %q:\n%s", expected, config)
		}
	}
	if strings.Contains(string(config), "vnet;") || strings.Contains(string(config), "vnet.interface") {
		t.Fatalf("inherited jail config unexpectedly enables VNET:\n%s", config)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "delete", "--ctid", strconv.FormatUint(uint64(ctid), 10), "--purge", "--json")
	var deleted jailDeleteResult
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode CLI inherited jail delete: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.CTID != ctid {
		t.Fatalf("CLI inherited jail delete result = %#v", deleted)
	}
	assertConsoleJailDeleted(t, suite, ctid, dataset, configPath)
}

func TestJailWithStaticVNETNetworkIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	base := ensureConsoleBaseBootstrap(t, suite)
	ctid := consoleIntegrationJailCTID(t, suite, 3)
	addresses := consoleJailVNETAddresses(suite.runID)
	switchName := "jail-vnet-switch-" + suite.runID
	jailName := "jail-vnet-" + suite.runID

	// Register the switch cleanup first so the jail and its epairs are removed before it.
	t.Cleanup(func() { cleanupStandardSwitch(t, suite, switchName) })
	t.Cleanup(func() { cleanupConsoleJail(t, suite, ctid) })

	output := runSylve(t, suite.binaryPath, suite.configPath,
		"switches", "create", "--type", "standard", "--name", switchName,
		"--network4-manual", addresses.bridgeIPv4CIDR,
		"--network6-manual", addresses.bridgeIPv6CIDR,
		"--private", "--json")
	var createdSwitch struct {
		Created bool   `json:"created"`
		ID      uint   `json:"id"`
		Type    string `json:"type"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal([]byte(output), &createdSwitch); err != nil {
		t.Fatalf("decode CLI VNET switch create: %v\noutput: %s", err, output)
	}
	if !createdSwitch.Created || createdSwitch.ID == 0 || createdSwitch.Type != "standard" || createdSwitch.Name != switchName {
		t.Fatalf("CLI VNET switch create result = %#v", createdSwitch)
	}

	var standard networkModels.StandardSwitch
	if err := suite.database.First(&standard, createdSwitch.ID).Error; err != nil {
		t.Fatalf("load VNET standard switch: %v", err)
	}
	bridge := consoleBridge(t, standard.BridgeName)
	if !hasInterfaceGroup(bridge.Groups, "bridge") || len(bridge.BridgeMembers) != 0 {
		t.Fatalf("VNET bridge must be generated and memberless before jail start: %#v", bridge)
	}

	resourceLimits := false
	request := jailServiceInterfaces.CreateJailRequest{
		Name:           jailName,
		CTID:           &ctid,
		Pool:           suite.poolName,
		BootstrapName:  base.Name,
		SwitchName:     switchName,
		IPv4Raw:        addresses.jailIPv4CIDR,
		IPv4GwRaw:      addresses.bridgeIPv4,
		IPv6Raw:        addresses.jailIPv6CIDR,
		IPv6GwRaw:      addresses.bridgeIPv6,
		Type:           jailModels.JailTypeFreeBSD,
		ResourceLimits: &resourceLimits,
		MetadataMeta:   "console-integration=static-vnet",
	}
	requestPath := writeConsoleJailRequest(t, suite, "static-vnet", request)

	output = runREPLCommand(t, suite.socketPath, "jails create --file "+requestPath+" --json")
	var created jailCreateResult
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("decode REPL static VNET jail create: %v\noutput: %s", err, output)
	}
	if !created.Created || created.CTID != ctid || created.Name != jailName {
		t.Fatalf("REPL static VNET jail create result = %#v", created)
	}

	jail := consoleJailByCTID(t, suite, ctid)
	if len(jail.Networks) != 1 || jail.Networks[0].SwitchID != standard.ID || jail.Networks[0].SwitchType != "standard" ||
		jail.Networks[0].IPv4ID == nil || jail.Networks[0].IPv4GwID == nil || jail.Networks[0].IPv6ID == nil || jail.Networks[0].IPv6GwID == nil {
		t.Fatalf("created static VNET jail = %#v", jail)
	}
	network := jail.Networks[0]
	epairBase := utils.HashIntToNLetters(int(ctid), 5) + "_net" + strconv.FormatUint(uint64(network.ID), 10)
	epairA := epairBase + "a"
	epairB := epairBase + "b"

	dataset := fmt.Sprintf("%s/sylve/jails/%d", suite.poolName, ctid)
	mountPoint := fmt.Sprintf("/%s/sylve/jails/%d", suite.poolName, ctid)
	configPath := filepath.Join(suite.dataPath, "jails", strconv.FormatUint(uint64(ctid), 10), strconv.FormatUint(uint64(ctid), 10)+".conf")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read static VNET jail config: %v", err)
	}
	for _, expected := range []string{
		"vnet;",
		"vnet.interface = \"" + epairB + "\";",
		"exec.start = \"/bin/sh /etc/rc\";",
		"exec.stop = \"/bin/sh /etc/rc.shutdown\";",
		"meta = \"console-integration=static-vnet\";",
	} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("static VNET jail config missing %q:\n%s", expected, config)
		}
	}
	rcConf, err := os.ReadFile(filepath.Join(mountPoint, "etc", "rc.conf"))
	if err != nil {
		t.Fatalf("read static VNET rc.conf: %v", err)
	}
	for _, expected := range []string{
		"ifconfig_" + epairB + "=\"inet " + addresses.jailIPv4 + " netmask 255.255.255.0\"",
		"defaultrouter=\"" + addresses.bridgeIPv4 + "\"",
		"ifconfig_" + epairB + "_ipv6=\"inet6 " + addresses.jailIPv6CIDR + "\"",
		"ipv6_defaultrouter=\"" + addresses.bridgeIPv6 + "\"",
	} {
		if !strings.Contains(string(rcConf), expected) {
			t.Fatalf("static VNET rc.conf missing %q:\n%s", expected, rcConf)
		}
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "start", "--ctid", strconv.FormatUint(uint64(ctid), 10), "--json")
	assertJailAction(t, output, ctid, "start")
	waitForConsoleJailRunning(t, suite, ctid, true)
	waitForConsoleJailLifecycleIdle(t, suite, ctid)

	hostEpair := consoleInterface(t, epairA)
	if !hasInterfaceGroup(hostEpair.Groups, "sylve") {
		t.Fatalf("host epair is not marked as Sylve-managed: %#v", hostEpair)
	}
	bridge = consoleBridge(t, standard.BridgeName)
	if len(bridge.BridgeMembers) != 1 || bridge.BridgeMembers[0].Name != epairA {
		t.Fatalf("VNET bridge members = %#v, want only %s", bridge.BridgeMembers, epairA)
	}
	waitForConsoleJailInterfaceAddresses(t, utils.HashIntToNLetters(int(ctid), 5), epairB, addresses.jailIPv4, addresses.jailIPv6)

	output = runREPLCommand(t, suite.socketPath, "jails stop "+strconv.FormatUint(uint64(ctid), 10)+" --json")
	assertJailAction(t, output, ctid, "stop")
	waitForConsoleJailRunning(t, suite, ctid, false)
	waitForConsoleJailLifecycleIdle(t, suite, ctid)
	if !hasInterfaceGroup(consoleInterface(t, epairA).Groups, "sylve") {
		t.Fatalf("stopped VNET host epair %s is not marked as Sylve-managed", epairA)
	}
	consoleInterface(t, epairB)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "delete", "--ctid", strconv.FormatUint(uint64(ctid), 10), "--purge", "--json")
	var deleted jailDeleteResult
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode CLI static VNET jail delete: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.CTID != ctid {
		t.Fatalf("CLI static VNET jail delete result = %#v", deleted)
	}
	assertConsoleJailDeleted(t, suite, ctid, dataset, configPath)
	for _, epair := range []string{epairA, epairB} {
		assertConsoleInterfaceMissing(t, epair)
	}
	bridge = consoleBridge(t, standard.BridgeName)
	if len(bridge.BridgeMembers) != 0 {
		t.Fatalf("VNET bridge still has members after jail deletion: %#v", bridge.BridgeMembers)
	}

	output = runREPLCommand(t, suite.socketPath,
		"switches delete standard "+strconv.FormatUint(uint64(standard.ID), 10)+" --json")
	var deletedSwitch struct {
		Deleted bool   `json:"deleted"`
		ID      uint   `json:"id"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal([]byte(output), &deletedSwitch); err != nil {
		t.Fatalf("decode REPL VNET switch delete: %v\noutput: %s", err, output)
	}
	if !deletedSwitch.Deleted || deletedSwitch.ID != standard.ID || deletedSwitch.Type != "standard" {
		t.Fatalf("REPL VNET switch delete result = %#v", deletedSwitch)
	}
	assertConsoleInterfaceMissing(t, standard.BridgeName)
}

func TestJailObjectReferenceWorkflowIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	base := ensureConsoleBaseBootstrap(t, suite)
	ctid := consoleIntegrationJailCTID(t, suite, 6)
	addresses := consoleJailVNETAddressesWithOffset(suite.runID, 1)
	jailName := "jail-objects-" + suite.runID
	switchName := "jail-objects-switch-" + suite.runID
	namedMACName := "jail-objects-mac-" + suite.runID
	addressName := "jail-objects-address-" + suite.runID
	gatewayName := "jail-objects-gateway-" + suite.runID
	networkName := "jail-objects-network-" + suite.runID
	namedMACValue := fmt.Sprintf("02:ca:%s:%s:%s:%s", suite.runID[0:2], suite.runID[2:4], suite.runID[4:6], suite.runID[6:8])
	autoMACValue := fmt.Sprintf("02:cb:%s:%s:%s:%s", suite.runID[4:6], suite.runID[6:8], suite.runID[8:10], suite.runID[10:12])

	t.Cleanup(func() { cleanupObject(t, suite, namedMACName) })
	t.Cleanup(func() { cleanupObject(t, suite, addressName) })
	t.Cleanup(func() { cleanupObject(t, suite, gatewayName) })
	t.Cleanup(func() { cleanupObject(t, suite, networkName) })
	t.Cleanup(func() { cleanupStandardSwitch(t, suite, switchName) })
	t.Cleanup(func() { cleanupConsoleJail(t, suite, ctid) })

	output := runSylve(t, suite.binaryPath, suite.configPath,
		"objects", "create", "--name", namedMACName, "--type", "mac", "--value", namedMACValue)
	if !strings.Contains(output, "created successfully") {
		t.Fatalf("CLI MAC object create output = %q", output)
	}
	namedMAC := objectByName(t, suite.database, namedMACName)

	output = runREPLCommand(t, suite.socketPath,
		"objects create "+addressName+" network "+addresses.jailIPv4CIDR)
	if !strings.Contains(output, "created successfully") {
		t.Fatalf("REPL address object create output = %q", output)
	}
	addressObject := objectByName(t, suite.database, addressName)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"objects", "create", "--name", gatewayName, "--type", "host", "--value", addresses.bridgeIPv4)
	if !strings.Contains(output, "created successfully") {
		t.Fatalf("CLI gateway object create output = %q", output)
	}
	gatewayObject := objectByName(t, suite.database, gatewayName)

	output = runREPLCommand(t, suite.socketPath,
		"objects create "+networkName+" network "+addresses.bridgeIPv4CIDR)
	if !strings.Contains(output, "created successfully") {
		t.Fatalf("REPL switch network object create output = %q", output)
	}
	networkObject := objectByName(t, suite.database, networkName)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"switches", "create", "--type", "standard", "--name", switchName,
		"--network4", strconv.FormatUint(uint64(networkObject.ID), 10),
		"--private", "--disable-ipv6", "--json")
	var createdSwitch switchMutationResult
	if err := json.Unmarshal([]byte(output), &createdSwitch); err != nil {
		t.Fatalf("decode CLI object-reference switch create: %v\noutput: %s", err, output)
	}
	if !createdSwitch.Created || createdSwitch.ID == 0 || createdSwitch.Type != "standard" || createdSwitch.Name != switchName {
		t.Fatalf("CLI object-reference switch create result = %#v", createdSwitch)
	}
	var standard networkModels.StandardSwitch
	if err := suite.database.First(&standard, createdSwitch.ID).Error; err != nil {
		t.Fatalf("load object-reference switch: %v", err)
	}
	bridge := consoleBridge(t, standard.BridgeName)
	if standard.NetworkID == nil || *standard.NetworkID != networkObject.ID || !hasInterfaceGroup(bridge.Groups, "bridge") || len(bridge.BridgeMembers) != 0 {
		t.Fatalf("object-reference switch = %#v, bridge = %#v", standard, bridge)
	}

	resourceLimits := false
	addressID := int(addressObject.ID)
	gatewayID := int(gatewayObject.ID)
	request := jailServiceInterfaces.CreateJailRequest{
		Name:           jailName,
		CTID:           &ctid,
		Pool:           suite.poolName,
		BootstrapName:  base.Name,
		SwitchName:     switchName,
		IPv4:           &addressID,
		IPv4Gw:         &gatewayID,
		MACRaw:         autoMACValue,
		Type:           jailModels.JailTypeFreeBSD,
		ResourceLimits: &resourceLimits,
		MetadataMeta:   "console-integration=object-references",
	}
	requestPath := writeConsoleJailRequest(t, suite, "object-references", request)

	output = runREPLCommand(t, suite.socketPath, "jails create --file "+requestPath+" --json")
	var created jailCreateResult
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("decode REPL object-reference jail create: %v\noutput: %s", err, output)
	}
	if !created.Created || created.CTID != ctid || created.Name != jailName {
		t.Fatalf("REPL object-reference jail create result = %#v", created)
	}

	jail := consoleJailByCTID(t, suite, ctid)
	if len(jail.Storages) != 1 || jail.Storages[0].Pool != suite.poolName || !jail.Storages[0].IsBase || len(jail.Networks) != 1 {
		t.Fatalf("created object-reference jail = %#v", jail)
	}
	jailNetwork := jail.Networks[0]
	if jailNetwork.SwitchID != standard.ID || jailNetwork.SwitchType != "standard" ||
		jailNetwork.MacID == nil || jailNetwork.IPv4ID == nil || jailNetwork.IPv4GwID == nil ||
		*jailNetwork.IPv4ID != addressObject.ID || *jailNetwork.IPv4GwID != gatewayObject.ID {
		t.Fatalf("created object-reference jail network = %#v", jailNetwork)
	}
	autoMACID := *jailNetwork.MacID
	if autoMACID == namedMAC.ID {
		t.Fatal("jail reused the named MAC object instead of creating the requested raw MAC object")
	}
	var autoMAC networkModels.Object
	if err := suite.database.Preload("Entries").First(&autoMAC, autoMACID).Error; err != nil {
		t.Fatalf("load auto-created MAC object: %v", err)
	}
	if autoMAC.Type != "Mac" || len(autoMAC.Entries) != 1 || autoMAC.Entries[0].Value != autoMACValue {
		t.Fatalf("auto-created MAC object = %#v", autoMAC)
	}

	dataset := fmt.Sprintf("%s/sylve/jails/%d", suite.poolName, ctid)
	mountPoint := fmt.Sprintf("/%s/sylve/jails/%d", suite.poolName, ctid)
	configPath := filepath.Join(suite.dataPath, "jails", strconv.FormatUint(uint64(ctid), 10), strconv.FormatUint(uint64(ctid), 10)+".conf")
	epairBase := utils.HashIntToNLetters(int(ctid), 5) + "_net" + strconv.FormatUint(uint64(jailNetwork.ID), 10)
	epairA := epairBase + "a"
	epairB := epairBase + "b"
	if output, err := exec.Command("zfs", "list", "-H", "-o", "name", dataset).CombinedOutput(); err != nil {
		t.Fatalf("zfs list object-reference jail dataset %s: %v\n%s", dataset, err, output)
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read object-reference jail config: %v", err)
	}
	for _, expected := range []string{
		"vnet;",
		"vnet.interface = \"" + epairB + "\";",
		"meta = \"console-integration=object-references\";",
	} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("object-reference jail config missing %q:\n%s", expected, config)
		}
	}
	rcConfPath := filepath.Join(mountPoint, "etc", "rc.conf")
	assertConsoleFileContains(t, rcConfPath, "ifconfig_"+epairB+"=\"inet "+addresses.jailIPv4+" netmask 255.255.255.0\"")
	assertConsoleFileContains(t, rcConfPath, "defaultrouter=\""+addresses.bridgeIPv4+"\"")

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "get", "--ctid", strconv.FormatUint(uint64(ctid), 10), "--json")
	var inspected jailModels.Jail
	if err := json.Unmarshal([]byte(output), &inspected); err != nil {
		t.Fatalf("decode CLI object-reference jail get: %v\noutput: %s", err, output)
	}
	if inspected.CTID != ctid || inspected.Name != jailName {
		t.Fatalf("CLI object-reference jail get = %#v", inspected)
	}

	output = runREPLCommand(t, suite.socketPath,
		"jails networks "+strconv.FormatUint(uint64(ctid), 10)+" --json")
	var listedNetworks []jailModels.Network
	if err := json.Unmarshal([]byte(output), &listedNetworks); err != nil {
		t.Fatalf("decode REPL object-reference jail networks: %v\noutput: %s", err, output)
	}
	if len(listedNetworks) != 1 || listedNetworks[0].ID != jailNetwork.ID {
		t.Fatalf("REPL object-reference jail networks = %#v", listedNetworks)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "start", "--ctid", strconv.FormatUint(uint64(ctid), 10), "--json")
	assertJailAction(t, output, ctid, "start")
	waitForConsoleJailRunning(t, suite, ctid, true)
	waitForConsoleJailLifecycleIdle(t, suite, ctid)
	hostEpair := consoleInterface(t, epairA)
	if !hasInterfaceGroup(hostEpair.Groups, "sylve") {
		t.Fatalf("object-reference host epair is not Sylve-managed: %#v", hostEpair)
	}
	bridge = consoleBridge(t, standard.BridgeName)
	if len(bridge.BridgeMembers) != 1 || bridge.BridgeMembers[0].Name != epairA {
		t.Fatalf("object-reference bridge members = %#v, want only %s", bridge.BridgeMembers, epairA)
	}
	waitForConsoleJailInterfaceAddresses(t, utils.HashIntToNLetters(int(ctid), 5), epairB, addresses.jailIPv4)

	deleteOutput := runSylveFailure(t, suite.binaryPath, suite.configPath,
		"objects", "delete", "--id", strconv.FormatUint(uint64(addressObject.ID), 10))
	if !strings.Contains(strings.ToLower(deleteOutput), "in use") {
		t.Fatalf("CLI delete referenced address output = %q", deleteOutput)
	}
	deleteError := runREPLCommandFailure(t, suite.socketPath,
		"objects delete "+strconv.FormatUint(uint64(gatewayObject.ID), 10))
	if !strings.Contains(strings.ToLower(deleteError), "in use") {
		t.Fatalf("REPL delete referenced gateway error = %q", deleteError)
	}

	updatedIPv4 := strings.TrimSuffix(addresses.jailIPv4, ".10") + ".11"
	updatedIPv4CIDR := updatedIPv4 + "/24"
	output = runREPLCommand(t, suite.socketPath,
		"objects edit "+strconv.FormatUint(uint64(addressObject.ID), 10)+" --value "+updatedIPv4CIDR)
	if !strings.Contains(output, "updated successfully") {
		t.Fatalf("REPL address object update output = %q", output)
	}
	assertConsoleFileContains(t, rcConfPath, "ifconfig_"+epairB+"=\"inet "+updatedIPv4+" netmask 255.255.255.0\"")

	output = runREPLCommand(t, suite.socketPath,
		"jails stop "+strconv.FormatUint(uint64(ctid), 10)+" --json")
	assertJailAction(t, output, ctid, "stop")
	waitForConsoleJailRunning(t, suite, ctid, false)
	waitForConsoleJailLifecycleIdle(t, suite, ctid)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "start", "--ctid", strconv.FormatUint(uint64(ctid), 10), "--json")
	assertJailAction(t, output, ctid, "start")
	waitForConsoleJailRunning(t, suite, ctid, true)
	waitForConsoleJailLifecycleIdle(t, suite, ctid)
	waitForConsoleJailInterfaceAddresses(t, utils.HashIntToNLetters(int(ctid), 5), epairB, updatedIPv4)

	output = runREPLCommand(t, suite.socketPath,
		"jails delete "+strconv.FormatUint(uint64(ctid), 10)+" --purge --json")
	var deleted jailDeleteResult
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode REPL object-reference jail delete: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.CTID != ctid {
		t.Fatalf("REPL object-reference jail delete result = %#v", deleted)
	}
	assertConsoleJailDeleted(t, suite, ctid, dataset, configPath)
	var remainingNetworks int64
	if err := suite.database.Model(&jailModels.Network{}).Where("jid = ?", jail.ID).Count(&remainingNetworks).Error; err != nil {
		t.Fatalf("count deleted jail networks: %v", err)
	}
	if remainingNetworks != 0 {
		t.Fatalf("remaining object-reference jail networks = %d, want 0", remainingNetworks)
	}
	for _, epair := range []string{epairA, epairB} {
		assertConsoleInterfaceMissing(t, epair)
	}
	assertConsoleObjectDeleted(t, suite, autoMACID)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"switches", "delete", "--type", "standard", "--id", strconv.FormatUint(uint64(standard.ID), 10), "--json")
	var deletedSwitch switchMutationResult
	if err := json.Unmarshal([]byte(output), &deletedSwitch); err != nil {
		t.Fatalf("decode CLI object-reference switch delete: %v\noutput: %s", err, output)
	}
	if !deletedSwitch.Deleted || deletedSwitch.ID != standard.ID || deletedSwitch.Type != "standard" {
		t.Fatalf("CLI object-reference switch delete result = %#v", deletedSwitch)
	}
	assertConsoleInterfaceMissing(t, standard.BridgeName)

	output = runREPLCommand(t, suite.socketPath,
		"objects delete "+strconv.FormatUint(uint64(namedMAC.ID), 10))
	if !strings.Contains(output, "deleted successfully") {
		t.Fatalf("REPL named MAC object delete output = %q", output)
	}
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"objects", "delete", "--id", strconv.FormatUint(uint64(addressObject.ID), 10))
	if !strings.Contains(output, "deleted successfully") {
		t.Fatalf("CLI address object delete output = %q", output)
	}
	output = runREPLCommand(t, suite.socketPath,
		"objects delete "+strconv.FormatUint(uint64(gatewayObject.ID), 10))
	if !strings.Contains(output, "deleted successfully") {
		t.Fatalf("REPL gateway object delete output = %q", output)
	}
	output = runSylve(t, suite.binaryPath, suite.configPath,
		"objects", "delete", "--id", strconv.FormatUint(uint64(networkObject.ID), 10))
	if !strings.Contains(output, "deleted successfully") {
		t.Fatalf("CLI switch network object delete output = %q", output)
	}
	for _, objectID := range []uint{namedMAC.ID, addressObject.ID, gatewayObject.ID, networkObject.ID} {
		assertConsoleObjectDeleted(t, suite, objectID)
	}
}

func TestJailFromDownloadedBaseWithTogglesIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	ctid := consoleIntegrationJailCTID(t, suite, 4)
	jailName := "jail-download-" + suite.runID
	archivePath := createConsoleDownloadedBaseArchive(t, suite)
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read downloaded base archive: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/base.txz" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/x-xz")
		writer.Header().Set("Content-Length", strconv.Itoa(len(archive)))
		_, _ = writer.Write(archive)
	}))
	defer server.Close()

	var downloadID uint
	t.Cleanup(func() { cleanupConsoleDownload(t, suite, downloadID) })
	t.Cleanup(func() { cleanupConsoleJail(t, suite, ctid) })

	filename := "downloaded-base-" + suite.runID + ".txz"
	output := runSylve(t, suite.binaryPath, suite.configPath,
		"downloads", "start", "--url", server.URL+"/base.txz", "--filename", filename,
		"--type", "base-rootfs", "--extract", "--json")
	var started struct {
		ID      uint `json:"id"`
		Started bool `json:"started"`
	}
	if err := json.Unmarshal([]byte(output), &started); err != nil {
		t.Fatalf("decode CLI base download start: %v\noutput: %s", err, output)
	}
	if !started.Started || started.ID == 0 {
		t.Fatalf("CLI base download start result = %#v", started)
	}
	downloadID = started.ID
	download := waitForConsoleDownload(t, suite, started.ID)
	if download.UType != utilitiesModels.DownloadUTypeBase || !download.AutomaticExtraction || download.ExtractedPath == "" {
		t.Fatalf("completed base download = %#v", download)
	}
	if info, err := os.Stat(download.ExtractedPath); err != nil || !info.IsDir() {
		t.Fatalf("extracted base path %s: %v", download.ExtractedPath, err)
	}
	if marker, err := os.ReadFile(filepath.Join(download.ExtractedPath, "etc", "sylve-download-marker")); err != nil || string(marker) != "downloaded base\n" {
		t.Fatalf("read extracted base marker: %v, contents=%q", err, marker)
	}

	resourceLimits := true
	cleanEnvironment := true
	startAtBoot := true
	cores := 1
	memory := 64 * 1024 * 1024
	request := jailServiceInterfaces.CreateJailRequest{
		Name:              jailName,
		CTID:              &ctid,
		Pool:              suite.poolName,
		Base:              download.UUID,
		SwitchName:        "none",
		Type:              jailModels.JailTypeFreeBSD,
		ResourceLimits:    &resourceLimits,
		Cores:             &cores,
		Memory:            &memory,
		CleanEnvironment:  &cleanEnvironment,
		StartAtBoot:       &startAtBoot,
		StartOrder:        11,
		AllowedOptions:    []string{"allow.raw_sockets"},
		AdditionalOptions: "enforce_statfs = 2;",
		MetadataMeta:      "console-integration=downloaded-base",
		MetadataEnv:       "SYLVE_DOWNLOAD_BASE=1",
	}
	requestPath := writeConsoleJailRequest(t, suite, "downloaded-base", request)

	output = runREPLCommand(t, suite.socketPath, "jails create --file "+requestPath+" --json")
	var created jailCreateResult
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("decode REPL downloaded-base jail create: %v\noutput: %s", err, output)
	}
	if !created.Created || created.CTID != ctid || created.Name != jailName {
		t.Fatalf("REPL downloaded-base jail create result = %#v", created)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath, "jails", "list", "--json")
	var listedJails []jailServiceInterfaces.SimpleList
	if err := json.Unmarshal([]byte(output), &listedJails); err != nil {
		t.Fatalf("decode CLI jail list: %v\noutput: %s", err, output)
	}
	found := false
	for _, listed := range listedJails {
		if listed.CTID != ctid {
			continue
		}
		found = true
		if listed.ResourceLimits == nil || !*listed.ResourceLimits || listed.Cores != cores || listed.Memory != memory {
			t.Fatalf("CLI jail list entry = %#v", listed)
		}
	}
	if !found {
		t.Fatalf("CLI jail list did not include CTID %d: %#v", ctid, listedJails)
	}
	output = runREPLCommand(t, suite.socketPath, "jails list")
	if !strings.Contains(output, "1 CPU, 64 MiB") {
		t.Fatalf("REPL jail list did not show resource limits:\n%s", output)
	}

	jail := consoleJailByCTID(t, suite, ctid)
	if jail.ResourceLimits == nil || !*jail.ResourceLimits || jail.Cores != cores || jail.Memory != memory ||
		jail.StartAtBoot == nil || !*jail.StartAtBoot || jail.StartOrder != 11 || !jail.CleanEnvironment ||
		len(jail.AllowedOptions) != 1 || jail.AllowedOptions[0] != "allow.raw_sockets" || len(jail.Networks) != 0 {
		t.Fatalf("created downloaded-base jail = %#v", jail)
	}

	dataset := fmt.Sprintf("%s/sylve/jails/%d", suite.poolName, ctid)
	mountPoint := fmt.Sprintf("/%s/sylve/jails/%d", suite.poolName, ctid)
	configPath := filepath.Join(suite.dataPath, "jails", strconv.FormatUint(uint64(ctid), 10), strconv.FormatUint(uint64(ctid), 10)+".conf")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read downloaded-base jail config: %v", err)
	}
	for _, expected := range []string{
		"allow.raw_sockets;",
		"exec.clean;",
		"exec.start = \"/bin/sh /etc/rc\";",
		"exec.stop = \"/bin/sh /etc/rc.shutdown\";",
		"enforce_statfs = 2;",
		"meta = \"console-integration=downloaded-base\";",
		"env = \"SYLVE_DOWNLOAD_BASE=1\";",
	} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("downloaded-base jail config missing %q:\n%s", expected, config)
		}
	}
	postStart, err := os.ReadFile(filepath.Join(suite.dataPath, "jails", strconv.FormatUint(uint64(ctid), 10), "scripts", "post-start.sh"))
	if err != nil {
		t.Fatalf("read downloaded-base post-start script: %v", err)
	}
	if !strings.Contains(string(postStart), "memoryuse:deny=64M") {
		t.Fatalf("downloaded-base post-start script is missing the memory limit:\n%s", postStart)
	}
	if marker, err := os.ReadFile(filepath.Join(mountPoint, "etc", "sylve-download-marker")); err != nil || string(marker) != "downloaded base\n" {
		t.Fatalf("read copied downloaded-base marker: %v, contents=%q", err, marker)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "get", "--ctid", strconv.FormatUint(uint64(ctid), 10), "--json")
	var inspected jailModels.Jail
	if err := json.Unmarshal([]byte(output), &inspected); err != nil {
		t.Fatalf("decode CLI downloaded-base jail get: %v\noutput: %s", err, output)
	}
	if inspected.CTID != ctid || inspected.Name != jailName {
		t.Fatalf("CLI downloaded-base jail get = %#v", inspected)
	}

	output = runREPLCommand(t, suite.socketPath,
		"jails delete "+strconv.FormatUint(uint64(ctid), 10)+" --purge --json")
	var deleted jailDeleteResult
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode REPL downloaded-base jail delete: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.CTID != ctid {
		t.Fatalf("REPL downloaded-base jail delete result = %#v", deleted)
	}
	assertConsoleJailDeleted(t, suite, ctid, dataset, configPath)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"downloads", "delete", "--id", strconv.FormatUint(uint64(download.ID), 10), "--json")
	var deletedDownload struct {
		Deleted bool `json:"deleted"`
		ID      uint `json:"id"`
	}
	if err := json.Unmarshal([]byte(output), &deletedDownload); err != nil {
		t.Fatalf("decode CLI base download delete: %v\noutput: %s", err, output)
	}
	if !deletedDownload.Deleted || deletedDownload.ID != download.ID {
		t.Fatalf("CLI base download delete result = %#v", deletedDownload)
	}
	var remaining utilitiesModels.Downloads
	if err := suite.database.First(&remaining, download.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("base download after delete error = %v, want not found", err)
	}
	if _, err := os.Stat(download.Path); !os.IsNotExist(err) {
		t.Fatalf("downloaded base archive after delete error = %v, want not exist", err)
	}
	extractRoot := filepath.Join(suite.dataPath, "downloads", "extracted", download.UUID)
	if _, err := os.Stat(extractRoot); !os.IsNotExist(err) {
		t.Fatalf("downloaded base extraction after delete error = %v, want not exist", err)
	}
}

func TestJailWithDHCPSLAACNetworkConfigurationIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	base := ensureConsoleBaseBootstrap(t, suite)
	ctid := consoleIntegrationJailCTID(t, suite, 5)
	switchName := "jail-auto-switch-" + suite.runID
	jailName := "jail-auto-" + suite.runID

	t.Cleanup(func() { cleanupStandardSwitch(t, suite, switchName) })
	t.Cleanup(func() { cleanupConsoleJail(t, suite, ctid) })

	output := runREPLCommand(t, suite.socketPath, "switches create standard "+switchName+" --private --json")
	var createdSwitch struct {
		Created bool   `json:"created"`
		ID      uint   `json:"id"`
		Type    string `json:"type"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal([]byte(output), &createdSwitch); err != nil {
		t.Fatalf("decode REPL DHCP/SLAAC switch create: %v\noutput: %s", err, output)
	}
	if !createdSwitch.Created || createdSwitch.ID == 0 || createdSwitch.Type != "standard" || createdSwitch.Name != switchName {
		t.Fatalf("REPL DHCP/SLAAC switch create result = %#v", createdSwitch)
	}

	var standard networkModels.StandardSwitch
	if err := suite.database.First(&standard, createdSwitch.ID).Error; err != nil {
		t.Fatalf("load DHCP/SLAAC standard switch: %v", err)
	}
	bridge := consoleBridge(t, standard.BridgeName)
	if !hasInterfaceGroup(bridge.Groups, "bridge") || len(bridge.BridgeMembers) != 0 {
		t.Fatalf("DHCP/SLAAC bridge must be generated and memberless: %#v", bridge)
	}

	automatic := true
	resourceLimits := false
	request := jailServiceInterfaces.CreateJailRequest{
		Name:           jailName,
		CTID:           &ctid,
		Pool:           suite.poolName,
		BootstrapName:  base.Name,
		SwitchName:     switchName,
		DHCP:           &automatic,
		SLAAC:          &automatic,
		Type:           jailModels.JailTypeFreeBSD,
		ResourceLimits: &resourceLimits,
		MetadataMeta:   "console-integration=dhcp-slaac",
	}
	requestPath := writeConsoleJailRequest(t, suite, "dhcp-slaac", request)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "create", "--file", requestPath, "--json")
	var created jailCreateResult
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("decode CLI DHCP/SLAAC jail create: %v\noutput: %s", err, output)
	}
	if !created.Created || created.CTID != ctid || created.Name != jailName {
		t.Fatalf("CLI DHCP/SLAAC jail create result = %#v", created)
	}

	jail := consoleJailByCTID(t, suite, ctid)
	if len(jail.Networks) != 1 || !jail.Networks[0].DHCP || !jail.Networks[0].SLAAC ||
		jail.Networks[0].IPv4ID != nil || jail.Networks[0].IPv4GwID != nil || jail.Networks[0].IPv6ID != nil || jail.Networks[0].IPv6GwID != nil {
		t.Fatalf("created DHCP/SLAAC jail = %#v", jail)
	}
	network := jail.Networks[0]
	epairBase := utils.HashIntToNLetters(int(ctid), 5) + "_net" + strconv.FormatUint(uint64(network.ID), 10)
	epairA := epairBase + "a"
	epairB := epairBase + "b"
	assertConsoleInterfaceMissing(t, epairA)
	assertConsoleInterfaceMissing(t, epairB)

	dataset := fmt.Sprintf("%s/sylve/jails/%d", suite.poolName, ctid)
	configPath := filepath.Join(suite.dataPath, "jails", strconv.FormatUint(uint64(ctid), 10), strconv.FormatUint(uint64(ctid), 10)+".conf")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read DHCP/SLAAC jail config: %v", err)
	}
	for _, expected := range []string{"vnet;", "vnet.interface = \"" + epairB + "\";", "meta = \"console-integration=dhcp-slaac\";"} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("DHCP/SLAAC jail config missing %q:\n%s", expected, config)
		}
	}
	mountPoint := fmt.Sprintf("/%s/sylve/jails/%d", suite.poolName, ctid)
	rcConf, err := os.ReadFile(filepath.Join(mountPoint, "etc", "rc.conf"))
	if err != nil {
		t.Fatalf("read DHCP/SLAAC rc.conf: %v", err)
	}
	for _, expected := range []string{
		"ifconfig_" + epairB + "=\"SYNCDHCP\"",
		"ifconfig_" + epairB + "_ipv6=\"inet6 accept_rtadv\"",
		"rtsold_enable=\"YES\"",
	} {
		if !strings.Contains(string(rcConf), expected) {
			t.Fatalf("DHCP/SLAAC rc.conf missing %q:\n%s", expected, rcConf)
		}
	}

	output = runREPLCommand(t, suite.socketPath,
		"jails delete "+strconv.FormatUint(uint64(ctid), 10)+" --purge --json")
	var deleted jailDeleteResult
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode REPL DHCP/SLAAC jail delete: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.CTID != ctid {
		t.Fatalf("REPL DHCP/SLAAC jail delete result = %#v", deleted)
	}
	assertConsoleJailDeleted(t, suite, ctid, dataset, configPath)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"switches", "delete", "--type", "standard", "--id", strconv.FormatUint(uint64(standard.ID), 10), "--json")
	var deletedSwitch struct {
		Deleted bool   `json:"deleted"`
		ID      uint   `json:"id"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal([]byte(output), &deletedSwitch); err != nil {
		t.Fatalf("decode CLI DHCP/SLAAC switch delete: %v\noutput: %s", err, output)
	}
	if !deletedSwitch.Deleted || deletedSwitch.ID != standard.ID || deletedSwitch.Type != "standard" {
		t.Fatalf("CLI DHCP/SLAAC switch delete result = %#v", deletedSwitch)
	}
	assertConsoleInterfaceMissing(t, standard.BridgeName)
}

type consoleJailVNETAddressPlan struct {
	bridgeIPv4CIDR string
	bridgeIPv4     string
	jailIPv4CIDR   string
	jailIPv4       string
	bridgeIPv6CIDR string
	bridgeIPv6     string
	jailIPv6CIDR   string
	jailIPv6       string
}

func consoleJailVNETAddresses(runID string) consoleJailVNETAddressPlan {
	return consoleJailVNETAddressesWithOffset(runID, 0)
}

func consoleJailVNETAddressesWithOffset(runID string, offset uint64) consoleJailVNETAddressPlan {
	value, _ := strconv.ParseUint(runID, 16, 64)
	value += offset
	ipv4Prefix := fmt.Sprintf("198.%d.%d", 18+(value>>16)%2, value%256)
	ipv6Prefix := fmt.Sprintf("fd00:%x", value&0xffff)
	return consoleJailVNETAddressPlan{
		bridgeIPv4CIDR: ipv4Prefix + ".1/24",
		bridgeIPv4:     ipv4Prefix + ".1",
		jailIPv4CIDR:   ipv4Prefix + ".10/24",
		jailIPv4:       ipv4Prefix + ".10",
		bridgeIPv6CIDR: ipv6Prefix + "::1/64",
		bridgeIPv6:     ipv6Prefix + "::1",
		jailIPv6CIDR:   ipv6Prefix + "::10/64",
		jailIPv6:       ipv6Prefix + "::10",
	}
}

func consoleIntegrationJailCTID(t *testing.T, suite *consoleIntegrationSuite, offset uint) uint {
	t.Helper()
	value, err := strconv.ParseUint(suite.runID, 16, 64)
	if err != nil {
		t.Fatalf("parse suite run ID %q: %v", suite.runID, err)
	}
	return uint(1000 + value%8000 + uint64(offset))
}

func ensureConsoleBaseBootstrap(t *testing.T, suite *consoleIntegrationSuite) jailServiceInterfaces.BootstrapEntry {
	t.Helper()
	const baseName = "15-0-Base"
	var record jailModels.JailBootstrap
	err := suite.database.Where("pool = ? AND name = ?", suite.poolName, baseName).First(&record).Error
	if err == nil && record.Status == "completed" {
		entry := jailServiceInterfaces.BootstrapEntry{
			Pool:       record.Pool,
			Name:       record.Name,
			Dataset:    record.Dataset,
			MountPoint: record.MountPoint,
			Status:     record.Status,
			Exists:     true,
		}
		assertCompletedBootstrap(t, suite, entry)
		return entry
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("load base bootstrap: %v", err)
	}
	entry := createBootstrapThroughCLI(t, suite, "base")
	assertCompletedBootstrap(t, suite, entry)
	return entry
}

func writeConsoleJailRequest(t *testing.T, suite *consoleIntegrationSuite, suffix string, request jailServiceInterfaces.CreateJailRequest) string {
	t.Helper()
	contents, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal jail request: %v", err)
	}
	path := filepath.Join(suite.root, "jail-"+suffix+"-"+suite.runID+".json")
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatalf("write jail request %s: %v", path, err)
	}
	return path
}

func createConsoleDownloadedBaseArchive(t *testing.T, suite *consoleIntegrationSuite) string {
	t.Helper()
	root := filepath.Join(suite.root, "downloaded-base-"+suite.runID)
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0755); err != nil {
		t.Fatalf("create downloaded-base root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "rc.conf"), []byte(""), 0644); err != nil {
		t.Fatalf("write downloaded-base rc.conf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "sylve-download-marker"), []byte("downloaded base\n"), 0644); err != nil {
		t.Fatalf("write downloaded-base marker: %v", err)
	}

	archive := filepath.Join(suite.root, "downloaded-base-"+suite.runID+".txz")
	output, err := exec.Command("tar", "-C", root, "-cJf", archive, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("create downloaded-base archive: %v\n%s", err, output)
	}
	return archive
}

func consoleJailByCTID(t *testing.T, suite *consoleIntegrationSuite, ctid uint) jailModels.Jail {
	t.Helper()
	var jail jailModels.Jail
	if err := suite.database.Preload("Storages").Preload("Networks").Where("ct_id = ?", ctid).First(&jail).Error; err != nil {
		t.Fatalf("load jail %d: %v", ctid, err)
	}
	return jail
}

func assertJailAction(t *testing.T, output string, ctid uint, action string) {
	t.Helper()
	var result struct {
		CTID   uint   `json:"ctId"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode jail %s result: %v\noutput: %s", action, err, output)
	}
	if result.CTID != ctid || result.Action != action {
		t.Fatalf("jail %s result = %#v\noutput: %s", action, result, output)
	}
}

func waitForConsoleJailRunning(t *testing.T, suite *consoleIntegrationSuite, ctid uint, want bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		running, err := suite.jail.IsJailRunning(ctid)
		if err == nil && running == want {
			return
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("jail %d running state did not become %t: %v", ctid, want, lastErr)
}

func waitForConsoleJailInterfaceAddresses(t *testing.T, jailName, interfaceName string, addresses ...string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastOutput []byte
	var lastErr error
	for time.Now().Before(deadline) {
		lastOutput, lastErr = exec.Command("jexec", jailName, "/sbin/ifconfig", interfaceName).CombinedOutput()
		if lastErr == nil {
			configured := true
			for _, address := range addresses {
				if !strings.Contains(string(lastOutput), address) {
					configured = false
					break
				}
			}
			if configured {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("VNET interface %s did not receive %v: %v\n%s", interfaceName, addresses, lastErr, lastOutput)
}

func waitForConsoleJailLifecycleIdle(t *testing.T, suite *consoleIntegrationSuite, ctid uint) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastTask any
	var lastErr error
	for time.Now().Before(deadline) {
		task, err := suite.lifecycle.GetActiveTaskForGuest("jail", ctid)
		if err == nil && task == nil {
			return
		}
		lastTask = task
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("jail %d lifecycle task did not become idle: task=%#v err=%v", ctid, lastTask, lastErr)
}

func assertConsoleJailDeleted(t *testing.T, suite *consoleIntegrationSuite, ctid uint, dataset, configPath string) {
	t.Helper()
	var jail jailModels.Jail
	if err := suite.database.Where("ct_id = ?", ctid).First(&jail).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("jail %d after delete error = %v, want not found", ctid, err)
	}
	if output, err := exec.Command("zfs", "list", "-H", "-o", "name", dataset).CombinedOutput(); err == nil {
		t.Fatalf("deleted jail dataset still exists: %s", output)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("jail config after delete error = %v, want not exist", err)
	}
}

func cleanupConsoleJail(t *testing.T, suite *consoleIntegrationSuite, ctid uint) {
	t.Helper()
	var jail jailModels.Jail
	if err := suite.database.Where("ct_id = ?", ctid).First(&jail).Error; err == nil {
		if err := suite.jail.DeleteJail(context.Background(), ctid, true, true); err != nil {
			t.Errorf("purge jail %d during cleanup: %v", ctid, err)
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("load jail %d during cleanup: %v", ctid, err)
	}

	dataset := fmt.Sprintf("%s/sylve/jails/%d", suite.poolName, ctid)
	if _, err := exec.Command("zfs", "list", "-H", "-o", "name", dataset).CombinedOutput(); err == nil {
		if output, err := exec.Command("zfs", "destroy", "-r", dataset).CombinedOutput(); err != nil {
			t.Errorf("destroy owned jail dataset %s during cleanup: %v\n%s", dataset, err, output)
		}
	}
	if err := os.RemoveAll(filepath.Join(suite.dataPath, "jails", strconv.FormatUint(uint64(ctid), 10))); err != nil {
		t.Errorf("remove jail %d config directory during cleanup: %v", ctid, err)
	}
}

func cleanupConsoleDownload(t *testing.T, suite *consoleIntegrationSuite, id uint) {
	t.Helper()
	if id == 0 || suite.utilities == nil {
		return
	}
	var download utilitiesModels.Downloads
	if err := suite.database.First(&download, id).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return
	} else if err != nil {
		t.Errorf("load download %d during cleanup: %v", id, err)
		return
	}
	if err := suite.utilities.DeleteDownload(int(id)); err != nil {
		t.Errorf("delete download %d during cleanup: %v", id, err)
	}
}

func consoleBridge(t *testing.T, name string) *iface.Interface {
	t.Helper()
	bridge, err := iface.Get(name)
	if err != nil {
		t.Fatalf("get bridge %s: %v", name, err)
	}
	return bridge
}

func consoleInterface(t *testing.T, name string) *iface.Interface {
	t.Helper()
	interfaceValue, err := iface.Get(name)
	if err != nil {
		t.Fatalf("get interface %s: %v", name, err)
	}
	return interfaceValue
}

func assertConsoleInterfaceMissing(t *testing.T, name string) {
	t.Helper()
	if _, err := iface.Get(name); err == nil || !isMissingInterfaceError(err) {
		t.Fatalf("interface %s after cleanup error = %v, want not found", name, err)
	}
}

func assertConsoleFileContains(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var contents []byte
	var lastErr error
	for time.Now().Before(deadline) {
		contents, lastErr = os.ReadFile(path)
		if lastErr == nil && strings.Contains(string(contents), want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("file %s did not contain %q: %v\n%s", path, want, lastErr, contents)
}

func assertConsoleObjectDeleted(t *testing.T, suite *consoleIntegrationSuite, objectID uint) {
	t.Helper()
	if err := suite.database.First(&networkModels.Object{}, objectID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("object %d after delete error = %v, want not found", objectID, err)
	}
}
