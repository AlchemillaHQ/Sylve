//go:build freebsd

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	consolepath "github.com/alchemillahq/sylve/internal/console"
	models "github.com/alchemillahq/sylve/internal/db/models"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/repl"
	"github.com/alchemillahq/sylve/internal/services/network"
	"gorm.io/gorm"
)

func TestObjectsCLIAndREPLIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("run console integration tests with make test-integration as root")
	}
	t.Setenv("SYLVE_DATA_PATH", "")

	dataPath := t.TempDir()
	database := openConsoleDatabase(t, filepath.Join(dataPath, "sylve.db"),
		&models.BasicSettings{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.ObjectListSnapshot{},
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.DHCPRange{},
		&networkModels.DHCPStaticLease{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
		&networkModels.StaticRoute{},
		&jailModels.Network{},
	)
	if err := database.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}
	networkService := &network.Service{DB: database, TelemetryDB: database}

	socketPath := consolepath.SocketPath(dataPath)
	server, err := repl.StartSocketServer(&repl.Context{Network: networkService}, socketPath)
	if err != nil {
		t.Fatalf("start console socket: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close console socket: %v", err)
		}
	})

	configPath := writeConsoleConfig(t, dataPath)
	binaryPath := buildSylveBinary(t)

	cliOutput := runSylve(t, binaryPath, configPath,
		"objects", "create", "--name", "cli-host", "--type", "host", "--value", "192.0.2.10")
	if !strings.Contains(cliOutput, "created successfully") {
		t.Fatalf("CLI create output = %q", cliOutput)
	}
	cliObject := objectByName(t, database, "cli-host")

	replOutput := runREPLCommand(t, socketPath, "objects create repl-network network 198.51.100.0/24")
	if !strings.Contains(replOutput, "created successfully") {
		t.Fatalf("REPL create output = %q", replOutput)
	}
	replObject := objectByName(t, database, "repl-network")

	listOutput := runSylve(t, binaryPath, configPath, "objects", "list", "--json")
	var objects []networkModels.Object
	if err := json.Unmarshal([]byte(listOutput), &objects); err != nil {
		t.Fatalf("decode CLI object list: %v\noutput: %s", err, listOutput)
	}
	if len(objects) != 2 {
		t.Fatalf("CLI listed %d objects, want 2: %#v", len(objects), objects)
	}

	replOutput = runREPLCommand(t, socketPath,
		"objects edit "+strconv.FormatUint(uint64(cliObject.ID), 10)+" --value 192.0.2.20")
	if !strings.Contains(replOutput, "updated successfully") {
		t.Fatalf("REPL edit output = %q", replOutput)
	}
	cliObject = objectByName(t, database, "cli-host")
	if cliObject.Type != "Host" || len(cliObject.Entries) != 1 || cliObject.Entries[0].Value != "192.0.2.20" {
		t.Fatalf("REPL partial edit did not preserve object state: %#v", cliObject)
	}

	cliOutput = runSylve(t, binaryPath, configPath,
		"objects", "edit", "--id", strconv.FormatUint(uint64(replObject.ID), 10),
		"--name", "repl-network-updated", "--value", "198.51.100.0/25")
	if !strings.Contains(cliOutput, "updated successfully") {
		t.Fatalf("CLI edit output = %q", cliOutput)
	}
	replObject = objectByName(t, database, "repl-network-updated")
	if replObject.Type != "Network" || len(replObject.Entries) != 1 || replObject.Entries[0].Value != "198.51.100.0/25" {
		t.Fatalf("CLI partial edit did not preserve object state: %#v", replObject)
	}

	deleteOutput := runSylve(t, binaryPath, configPath,
		"objects", "delete", "--id", strconv.FormatUint(uint64(replObject.ID), 10))
	if !strings.Contains(deleteOutput, "deleted successfully") {
		t.Fatalf("CLI delete output = %q", deleteOutput)
	}

	replOutput = runREPLCommand(t, socketPath,
		"objects delete "+strconv.FormatUint(uint64(cliObject.ID), 10))
	if !strings.Contains(replOutput, "deleted successfully") {
		t.Fatalf("REPL delete output = %q", replOutput)
	}

	var objectCount, entryCount int64
	if err := database.Model(&networkModels.Object{}).Count(&objectCount).Error; err != nil {
		t.Fatalf("count remaining objects: %v", err)
	}
	if err := database.Model(&networkModels.ObjectEntry{}).Count(&entryCount).Error; err != nil {
		t.Fatalf("count remaining object entries: %v", err)
	}
	if objectCount != 0 || entryCount != 0 {
		t.Fatalf("remaining objects/entries = %d/%d, want 0/0", objectCount, entryCount)
	}
}

func objectByName(t *testing.T, database *gorm.DB, name string) networkModels.Object {
	t.Helper()
	var object networkModels.Object
	if err := database.Preload("Entries").Where("name = ?", name).First(&object).Error; err != nil {
		t.Fatalf("find object %q: %v", name, err)
	}
	return object
}
