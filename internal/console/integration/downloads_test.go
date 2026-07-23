//go:build freebsd

package integration

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"gorm.io/gorm"
)

func TestDownloadsCLIAndREPLIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	contents := []byte("console integration download\n")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/fixture.txt" {
			http.NotFound(writer, request)
			return
		}
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/plain")
		writer.Header().Set("Content-Length", strconv.Itoa(len(contents)))
		_, _ = writer.Write(contents)
	}))
	defer server.Close()

	filename := "console-" + suite.runID + ".txt"
	output := runSylve(t, suite.binaryPath, suite.configPath,
		"downloads", "start", "--url", server.URL+"/fixture.txt", "--filename", filename, "--type", "other", "--json")
	var started struct {
		ID      uint `json:"id"`
		Started bool `json:"started"`
	}
	if err := json.Unmarshal([]byte(output), &started); err != nil {
		t.Fatalf("decode CLI download start: %v\noutput: %s", err, output)
	}
	if started.ID == 0 || !started.Started {
		t.Fatalf("CLI download start result = %#v", started)
	}

	download := waitForConsoleDownload(t, suite, started.ID)
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d, want 1", requests.Load())
	}
	expectedPath := filepath.Join(suite.dataPath, "downloads", "http", filename)
	if download.Path != expectedPath || download.Status != utilitiesModels.DownloadStatusDone || download.Progress != 100 {
		t.Fatalf("completed download = %#v", download)
	}
	gotContents, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(gotContents) != string(contents) {
		t.Fatalf("downloaded contents = %q, want %q", gotContents, contents)
	}

	output = runREPLCommand(t, suite.socketPath, "downloads list --json")
	var downloads []utilitiesModels.Downloads
	if err := json.Unmarshal([]byte(output), &downloads); err != nil {
		t.Fatalf("decode REPL download list: %v\noutput: %s", err, output)
	}
	if len(downloads) != 1 || downloads[0].ID != started.ID || downloads[0].Status != utilitiesModels.DownloadStatusDone {
		t.Fatalf("REPL downloads = %#v", downloads)
	}

	output = runREPLCommand(t, suite.socketPath,
		"downloads delete "+strconv.FormatUint(uint64(started.ID), 10)+" --json")
	var deleted struct {
		Deleted bool `json:"deleted"`
		ID      uint `json:"id"`
	}
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode REPL download delete: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.ID != started.ID {
		t.Fatalf("REPL download delete result = %#v", deleted)
	}

	var remaining utilitiesModels.Downloads
	if err := suite.database.First(&remaining, started.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("download record after delete error = %v, want not found", err)
	}
	if _, err := os.Stat(expectedPath); !os.IsNotExist(err) {
		t.Fatalf("downloaded file after delete error = %v, want not exist", err)
	}
}

func waitForConsoleDownload(t *testing.T, suite *consoleIntegrationSuite, id uint) utilitiesModels.Downloads {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last utilitiesModels.Downloads
	for time.Now().Before(deadline) {
		output := runREPLCommand(t, suite.socketPath, "downloads list --json")
		var downloads []utilitiesModels.Downloads
		if err := json.Unmarshal([]byte(output), &downloads); err != nil {
			t.Fatalf("decode REPL download poll: %v\noutput: %s", err, output)
		}

		found := false
		for _, download := range downloads {
			if download.ID != id {
				continue
			}
			last = download
			found = true
			break
		}
		if !found {
			t.Fatalf("download %d disappeared while polling: %#v", id, downloads)
		}

		switch last.Status {
		case utilitiesModels.DownloadStatusDone:
			return last
		case utilitiesModels.DownloadStatusFailed:
			t.Fatalf("download %d failed: %s", id, last.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("download %d did not finish within 30 seconds: %#v", id, last)
	return utilitiesModels.Downloads{}
}
