package repl

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

func TestJailBootstrapName(t *testing.T) {
	if got := jailBootstrapName(15, 0, "base"); got != "15-0-Base" {
		t.Fatalf("base bootstrap name = %q, want %q", got, "15-0-Base")
	}
	if got := jailBootstrapName(15, 0, "minimal"); got != "15-0-Minimal" {
		t.Fatalf("minimal bootstrap name = %q, want %q", got, "15-0-Minimal")
	}
}

func TestBuildConsoleJailCreateRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jail.json")
	contents := `{
  "name": "HAOS-W",
  "ctId": 101,
  "pool": "zroot",
  "bootstrapName": "15-0-Base",
  "switchName": "INHERIT",
  "type": "FreeBSD"
}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write jail create file: %v", err)
	}

	request, err := buildConsoleJailCreateRequest([]string{"--file", path})
	if err != nil {
		t.Fatalf("buildConsoleJailCreateRequest: %v", err)
	}
	if request.CTID == nil || *request.CTID != 101 {
		t.Fatalf("CTID = %v, want 101", request.CTID)
	}
	if request.Name != "HAOS-W" || request.Pool != "zroot" {
		t.Fatalf("request core fields = %#v", request)
	}
	if request.BootstrapName != "15-0-Base" || request.Base != "" {
		t.Fatalf("request source = base %q, bootstrap %q", request.Base, request.BootstrapName)
	}
	if request.SwitchName != "inherit" || request.Type != jailModels.JailTypeFreeBSD {
		t.Fatalf("request network/type = %q/%q", request.SwitchName, request.Type)
	}
}

func TestBuildConsoleJailCreateRequestRejectsInvalidSource(t *testing.T) {
	_, err := buildConsoleJailCreateRequest([]string{
		"--ctid", "101", "--name", "jail-101", "--pool", "zroot",
		"--base", "base-id", "--bootstrap", "15-0-Base", "--switch", "none", "--type", "freebsd",
	})
	if err == nil {
		t.Fatal("expected invalid source type error")
	}
}

func TestBuildConsoleJailCreateRequestOverridesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jail.json")
	contents := `{
  "name": "file-jail",
  "ctId": 101,
  "pool": "zroot",
  "bootstrapName": "15-0-Base",
  "switchName": "inherit",
  "type": "freebsd"
}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write jail create file: %v", err)
	}

	request, err := buildConsoleJailCreateRequest([]string{
		"--file", path, "--ctid", "102", "--name", "override-jail",
	})
	if err != nil {
		t.Fatalf("buildConsoleJailCreateRequest: %v", err)
	}
	if request.CTID == nil || *request.CTID != 102 || request.Name != "override-jail" || request.Pool != "zroot" {
		t.Fatalf("unexpected overridden request: %#v", request)
	}
}

func TestHandleJailsRejectsExtraArgumentsAndInvalidIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"list", []string{"list", "extra"}, "Usage: jails list"},
		{"get", []string{"get", "12", "extra"}, "Usage: jails get <ctid>"},
		{"start", []string{"start", "12", "extra"}, "Usage: jails start <ctid|all>"},
		{"delete", []string{"delete", "12", "extra"}, "Usage: jails delete <ctid> [--purge]"},
		{"networks", []string{"networks", "12", "extra"}, "Usage: jails networks <ctid>"},
		{"rmnet", []string{"rmnet", "12", "1", "extra"}, "Usage: jails rmnet <ctid> <net_id>"},
		{"zero", []string{"get", "0"}, "Invalid CTID '0'"},
		{"overflow", []string{"get", "18446744073709551616"}, "Invalid CTID '18446744073709551616'"},
		{"network zero", []string{"rmnet", "1", "0"}, "Invalid network ID '0'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			handleJails(&Context{Out: &out}, tc.args)
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("output = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestHandleJailBootstrapUsesMajorMinorVersion(t *testing.T) {
	var out bytes.Buffer
	ctx := &Context{Out: &out}

	handleJailBootstrap(ctx, []string{"create", "zroot", "15.0", "base"}, false)
	if !strings.Contains(out.String(), "jail_service_unavailable") {
		t.Fatalf("new version syntax was not accepted: %q", out.String())
	}

	out.Reset()
	handleJailBootstrap(ctx, []string{"create", "zroot", "15", "0", "base"}, false)
	if !strings.Contains(out.String(), "invalid bootstrap version") {
		t.Fatalf("legacy version syntax was accepted: %q", out.String())
	}
}

func TestFormatJailListIncludesResourceLimits(t *testing.T) {
	enabled := true
	disabled := false
	output := formatJailList([]jailServiceInterfaces.SimpleList{
		{
			CTID:           101,
			Name:           "limited",
			State:          "ACTIVE",
			ResourceLimits: &enabled,
			Cores:          1,
			Memory:         64 * 1024 * 1024,
		},
		{
			CTID:           102,
			Name:           "open",
			State:          "INACTIVE",
			ResourceLimits: &disabled,
		},
	})

	for _, want := range []string{"Limits", "1 CPU, 64 MiB", "unrestricted"} {
		if !strings.Contains(output, want) {
			t.Fatalf("jail list missing %q:\n%s", want, output)
		}
	}
}
