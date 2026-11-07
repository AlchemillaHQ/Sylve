// SPDX-License-Identifier: BSD-2-Clause

package network

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
)

func TestPFHostState(t *testing.T) {
	previousPaths := []string{pfMainConfPath, pfBackupConfPath, firewallRCConfPath}
	previousRun := firewallRunCommand
	t.Cleanup(func() {
		pfMainConfPath, pfBackupConfPath, firewallRCConfPath = previousPaths[0], previousPaths[1], previousPaths[2]
		firewallRunCommand = previousRun
	})

	pfctlDown := func(string, ...string) (string, error) {
		return "pfctl: /dev/pf: No such file or directory", errors.New("exit status 1")
	}
	pfctlUp := func(_ string, args ...string) (string, error) {
		switch args[len(args)-1] {
		case "-si":
			return "Status: Enabled for 0 days 01:00:00\n", nil
		case "-sr":
			return "block in on em0 all\npass out quick all\n\n", nil
		case "-sn":
			return "nat on em0 inet from 10.0.0.0/24 to any -> (em0) round-robin\n", nil
		}
		return "", errors.New("unexpected pfctl call")
	}

	tests := []struct {
		name   string
		pfConf string
		backup bool
		rcConf string
		pfctl  func(string, ...string) (string, error)
		want   networkServiceInterfaces.PFHostState
	}{
		{
			name:  "fresh host",
			pfctl: pfctlDown,
			want:  networkServiceInterfaces.PFHostState{Conf: "missing"},
		},
		{
			name:   "hand-written setup",
			pfConf: "ext_if = \"em0\"\nnat on $ext_if from 10.0.0.0/24 to any -> ($ext_if)\n",
			rcConf: "hostname=\"box\"\npf_enable=\"NO\"\npf_enable=\"YES\"\n",
			pfctl:  pfctlUp,
			want: networkServiceInterfaces.PFHostState{
				Conf: "unmanaged", Running: true, Rules: 2, NATRules: 1, EnabledInRCConf: true,
			},
		},
		{
			name:   "already managed by Sylve",
			pfConf: pfManagedHeader + "\npass all\n",
			backup: true,
			rcConf: "pf_enable=\"NO\"\n",
			pfctl:  pfctlDown,
			want:   networkServiceInterfaces.PFHostState{Conf: "managed", BackupExists: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			pfMainConfPath = filepath.Join(root, "pf.conf")
			pfBackupConfPath = filepath.Join(root, "pf.conf.pre-sylve")
			firewallRCConfPath = filepath.Join(root, "rc.conf")
			firewallRunCommand = tt.pfctl

			for path, content := range map[string]string{pfMainConfPath: tt.pfConf, firewallRCConfPath: tt.rcConf} {
				if content == "" {
					continue
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tt.backup {
				if err := os.WriteFile(pfBackupConfPath, []byte("pass all\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got, err := (&Service{}).PFHostState()
			if err != nil {
				t.Fatalf("PFHostState: %v", err)
			}
			if got != tt.want {
				t.Fatalf("PFHostState = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDHCPConfigCustom(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "missing", want: false},
		{name: "stock sample", content: "# Configuration file for dnsmasq.\n#\n#port=5353\n\n", want: false},
		{name: "hand-written", content: "# lan\ninterface=em1\ndhcp-range=10.0.0.50,10.0.0.150,12h\n", want: true},
		{name: "managed by Sylve", content: dhcpManagedHeader + "\n\nport=0\n", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{}
			svc.dhcpRuntime.configPath = filepath.Join(t.TempDir(), "dnsmasq.conf")
			if tt.content != "" {
				if err := os.WriteFile(svc.dhcpRuntime.configPath, []byte(tt.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got, err := svc.DHCPConfigCustom()
			if err != nil {
				t.Fatalf("DHCPConfigCustom: %v", err)
			}
			if got != tt.want {
				t.Fatalf("DHCPConfigCustom = %v, want %v", got, tt.want)
			}
		})
	}
}
