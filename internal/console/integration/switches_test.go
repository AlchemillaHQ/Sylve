//go:build freebsd

package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"gorm.io/gorm"
)

type switchMutationResult struct {
	Created bool   `json:"created"`
	Deleted bool   `json:"deleted"`
	ID      uint   `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
}

type switchListResult struct {
	Standard []networkModels.StandardSwitch `json:"standard"`
	Manual   []networkModels.ManualSwitch   `json:"manual"`
}

func TestSwitchesCLIAndREPLIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	standardName := "switch-standard-" + suite.runID
	manualName := "switch-manual-" + suite.runID
	networkName := "switch-network-" + suite.runID
	gatewayName := "switch-gateway-" + suite.runID
	network4, gateway4, route4 := switchTestNetwork(suite.runID)
	if switchRouteExists(t, route4) {
		t.Fatalf("refusing to use existing route %s", route4)
	}
	t.Cleanup(func() {
		cleanupManualSwitch(t, suite, manualName)
		cleanupStandardSwitch(t, suite, standardName)
		cleanupSwitchRoute(t, route4, gateway4)
		cleanupObject(t, suite, networkName)
		cleanupObject(t, suite, gatewayName)
	})

	output := runSylve(t, suite.binaryPath, suite.configPath,
		"objects", "create", "--name", gatewayName, "--type", "host", "--value", gateway4)
	if !strings.Contains(output, "created successfully") {
		t.Fatalf("CLI gateway object create output = %q", output)
	}
	gatewayObject := objectByName(t, suite.database, gatewayName)

	output = runREPLCommand(t, suite.socketPath,
		"objects create "+networkName+" network "+network4)
	if !strings.Contains(output, "created successfully") {
		t.Fatalf("REPL network object create output = %q", output)
	}
	networkObject := objectByName(t, suite.database, networkName)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"switches", "create", "--type", "standard", "--name", standardName,
		"--network4", strconv.FormatUint(uint64(networkObject.ID), 10),
		"--gateway4", strconv.FormatUint(uint64(gatewayObject.ID), 10),
		"--private", "--disable-ipv6", "--json")
	var createdStandard switchMutationResult
	if err := json.Unmarshal([]byte(output), &createdStandard); err != nil {
		t.Fatalf("decode CLI standard switch create: %v\noutput: %s", err, output)
	}
	if !createdStandard.Created || createdStandard.ID == 0 || createdStandard.Type != "standard" || createdStandard.Name != standardName {
		t.Fatalf("CLI standard switch create result = %#v", createdStandard)
	}

	var standard networkModels.StandardSwitch
	if err := suite.database.Preload("Ports").First(&standard, createdStandard.ID).Error; err != nil {
		t.Fatalf("load created standard switch: %v", err)
	}
	if standard.Name != standardName || !standard.Private || !standard.DisableIPv6 || len(standard.Ports) != 0 ||
		standard.NetworkID == nil || *standard.NetworkID != networkObject.ID ||
		standard.GatewayAddressID == nil || *standard.GatewayAddressID != gatewayObject.ID {
		t.Fatalf("created standard switch = %#v", standard)
	}
	bridge, err := iface.Get(standard.BridgeName)
	if err != nil {
		t.Fatalf("get created bridge %s: %v", standard.BridgeName, err)
	}
	if !hasInterfaceGroup(bridge.Groups, "bridge") || len(bridge.BridgeMembers) != 0 {
		t.Fatalf("created bridge = %#v", bridge)
	}
	if !switchRouteExists(t, route4) {
		t.Fatalf("route %s was not created for standard switch", route4)
	}

	output = runREPLCommand(t, suite.socketPath,
		"switches create manual "+manualName+" "+standard.BridgeName+" --json")
	var createdManual switchMutationResult
	if err := json.Unmarshal([]byte(output), &createdManual); err != nil {
		t.Fatalf("decode REPL manual switch create: %v\noutput: %s", err, output)
	}
	if !createdManual.Created || createdManual.ID == 0 || createdManual.Type != "manual" || createdManual.Name != manualName {
		t.Fatalf("REPL manual switch create result = %#v", createdManual)
	}

	var manual networkModels.ManualSwitch
	if err := suite.database.First(&manual, createdManual.ID).Error; err != nil {
		t.Fatalf("load created manual switch: %v", err)
	}
	if manual.Name != manualName || manual.Bridge != standard.BridgeName {
		t.Fatalf("created manual switch = %#v", manual)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath, "switches", "list", "--json")
	var listed switchListResult
	if err := json.Unmarshal([]byte(output), &listed); err != nil {
		t.Fatalf("decode CLI switches list: %v\noutput: %s", err, output)
	}
	if !containsStandardSwitch(listed.Standard, standard.ID) || !containsManualSwitch(listed.Manual, manual.ID) {
		t.Fatalf("CLI switches list = %#v", listed)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"switches", "delete", "--type", "manual", "--id", strconv.FormatUint(uint64(manual.ID), 10), "--json")
	var deletedManual switchMutationResult
	if err := json.Unmarshal([]byte(output), &deletedManual); err != nil {
		t.Fatalf("decode CLI manual switch delete: %v\noutput: %s", err, output)
	}
	if !deletedManual.Deleted || deletedManual.ID != manual.ID || deletedManual.Type != "manual" {
		t.Fatalf("CLI manual switch delete result = %#v", deletedManual)
	}
	if err := suite.database.First(&networkModels.ManualSwitch{}, manual.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("manual switch after CLI delete error = %v, want not found", err)
	}

	output = runREPLCommand(t, suite.socketPath,
		"switches delete standard "+strconv.FormatUint(uint64(standard.ID), 10)+" --json")
	var deletedStandard switchMutationResult
	if err := json.Unmarshal([]byte(output), &deletedStandard); err != nil {
		t.Fatalf("decode REPL standard switch delete: %v\noutput: %s", err, output)
	}
	if !deletedStandard.Deleted || deletedStandard.ID != standard.ID || deletedStandard.Type != "standard" {
		t.Fatalf("REPL standard switch delete result = %#v", deletedStandard)
	}
	if err := suite.database.First(&networkModels.StandardSwitch{}, standard.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("standard switch after REPL delete error = %v, want not found", err)
	}
	if _, err := iface.Get(standard.BridgeName); err == nil || !isMissingInterfaceError(err) {
		t.Fatalf("bridge %s after standard switch delete error = %v, want not found", standard.BridgeName, err)
	}
	if switchRouteExists(t, route4) {
		t.Fatalf("route %s remains after standard switch delete", route4)
	}

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"objects", "delete", "--id", strconv.FormatUint(uint64(networkObject.ID), 10))
	if !strings.Contains(output, "deleted successfully") {
		t.Fatalf("CLI network object delete output = %q", output)
	}
	output = runREPLCommand(t, suite.socketPath,
		"objects delete "+strconv.FormatUint(uint64(gatewayObject.ID), 10))
	if !strings.Contains(output, "deleted successfully") {
		t.Fatalf("REPL gateway object delete output = %q", output)
	}
	if err := suite.database.First(&networkModels.Object{}, networkObject.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("network object after delete error = %v, want not found", err)
	}
	if err := suite.database.First(&networkModels.Object{}, gatewayObject.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("gateway object after delete error = %v, want not found", err)
	}
}

func cleanupManualSwitch(t *testing.T, suite *consoleIntegrationSuite, name string) {
	t.Helper()
	var manual networkModels.ManualSwitch
	if err := suite.database.Where("name = ?", name).First(&manual).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("load manual switch %s for cleanup: %v", name, err)
		}
		return
	}
	if err := suite.network.DeleteManualSwitch(manual.ID); err != nil {
		t.Errorf("delete manual switch %s during cleanup: %v", name, err)
	}
}

func cleanupStandardSwitch(t *testing.T, suite *consoleIntegrationSuite, name string) {
	t.Helper()
	var standard networkModels.StandardSwitch
	if err := suite.database.Where("name = ?", name).First(&standard).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("load standard switch %s for cleanup: %v", name, err)
		}
		return
	}
	if err := suite.network.DeleteStandardSwitch(int(standard.ID)); err != nil {
		t.Errorf("delete standard switch %s during cleanup: %v", name, err)
	}
}

func cleanupObject(t *testing.T, suite *consoleIntegrationSuite, name string) {
	t.Helper()
	var object networkModels.Object
	if err := suite.database.Where("name = ?", name).First(&object).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("load object %s for cleanup: %v", name, err)
		}
		return
	}
	if err := suite.network.DeleteObject(object.ID); err != nil {
		t.Errorf("delete object %s during cleanup: %v", name, err)
	}
}

func switchTestNetwork(runID string) (network, gateway, route string) {
	value, _ := strconv.ParseUint(runID, 16, 64)
	block := value % (1 << 15)
	second := 18 + block/(256*64)
	withinSecond := block % (256 * 64)
	third := withinSecond / 64
	fourth := (withinSecond % 64) * 4
	route = fmt.Sprintf("198.%d.%d.%d/30", second, third, fourth)
	network = fmt.Sprintf("198.%d.%d.%d/30", second, third, fourth+1)
	gateway = fmt.Sprintf("198.%d.%d.%d", second, third, fourth+2)
	return network, gateway, route
}

func switchRouteExists(t *testing.T, route string) bool {
	t.Helper()
	output, err := exec.Command("netstat", "-rn", "-f", "inet").CombinedOutput()
	if err != nil {
		t.Fatalf("list IPv4 routes: %v\n%s", err, output)
	}
	return strings.Contains(string(output), route)
}

func cleanupSwitchRoute(t *testing.T, route, gateway string) {
	t.Helper()
	output, err := exec.Command("netstat", "-rn", "-f", "inet").CombinedOutput()
	if err != nil {
		t.Errorf("list IPv4 routes during cleanup: %v\n%s", err, output)
		return
	}
	if !strings.Contains(string(output), route) {
		return
	}
	output, err = exec.Command("/sbin/route", "delete", "-net", route, gateway).CombinedOutput()
	if err != nil {
		t.Errorf("delete owned route %s via %s during cleanup: %v\n%s", route, gateway, err, output)
	}
}

func hasInterfaceGroup(groups []string, want string) bool {
	for _, group := range groups {
		if group == want {
			return true
		}
	}
	return false
}

func containsStandardSwitch(switches []networkModels.StandardSwitch, id uint) bool {
	for _, standard := range switches {
		if standard.ID == id {
			return true
		}
	}
	return false
}

func containsManualSwitch(switches []networkModels.ManualSwitch, id uint) bool {
	for _, manual := range switches {
		if manual.ID == id {
			return true
		}
	}
	return false
}

func isMissingInterfaceError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not found") || strings.Contains(message, "does not exist")
}
