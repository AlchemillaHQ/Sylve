// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	"github.com/alchemillahq/sylve/pkg/utils"
)

var sambaVersion = regexp.MustCompile(`(?m)^Version ([0-9]+)\.([0-9]+)(?:\.|$)`)

func supportsMachineKeytabSync(version string) (bool, error) {
	parts := sambaVersion.FindStringSubmatch(strings.TrimSpace(version))
	if len(parts) != 3 {
		return false, fmt.Errorf("%w: unable to determine Samba version", ErrUnavailable)
	}
	major, majorErr := strconv.Atoi(parts[1])
	minor, minorErr := strconv.Atoi(parts[2])
	if majorErr != nil || minorErr != nil {
		return false, fmt.Errorf("%w: unable to determine Samba version", ErrUnavailable)
	}
	if major < 4 || (major == 4 && minor < 16) {
		return false, invalid("Active Directory integration requires Samba 4.16 or newer")
	}
	return major > 4 || minor >= 21, nil
}

func (s *Service) machineKeytabSyncSupported(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := s.command(ctx, "/usr/local/bin/net", []string{"--version"}, "", nil)
	if err != nil {
		return false, err
	}
	return supportsMachineKeytabSync(version)
}

func (s *Service) netCommand(ctx context.Context, ad directoryModels.ActiveDirectory, env []string, args ...string) (string, error) {
	syncKeytab, err := s.machineKeytabSyncSupported(ctx)
	if err != nil {
		return "", err
	}
	var hostnames []string
	if syncKeytab {
		hostnames, err = s.sambaKeytabHostnames(ctx, ad)
		if err != nil {
			return "", err
		}
	}
	return s.command(ctx, "/usr/local/bin/net", s.netArgs(ad, syncKeytab, hostnames, args...), "", env)
}

func (s *Service) refreshMachineKeytab(ctx context.Context, ad directoryModels.ActiveDirectory, previous []byte, native bool) ([]byte, error) {
	var hostnames []string
	if native {
		var err error
		hostnames, err = s.sambaKeytabHostnames(ctx, ad)
		if err != nil {
			return nil, err
		}
	}
	dir := filepath.Dir(s.machineKeytabPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(dir, "sylve-keytab-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "machine.keytab")
	defer func() {
		_ = os.Remove(path)
		_ = os.Remove(dir)
	}()
	if len(previous) == 0 {
		previous = []byte{5, 2}
	}
	// Seed the staging file so Samba can retain the previous KVNO and refresh
	// existing service principals. A failed refresh must not partially overwrite
	// the live keytab or Sylve's merged system keytab.
	if err := os.WriteFile(path, previous, 0600); err != nil {
		return nil, err
	}
	if native {
		args := []string{"--option=dedicated keytab file=" + path,
			"--option=sync machine password to keytab=" + machineKeytabSync(path, ad, hostnames...),
			"--machine-pass", "ads", "keytab", "create"}
		if _, err := s.command(ctx, "/usr/local/bin/net", s.netArgs(ad, true, hostnames, args...), "", nil); err != nil {
			return nil, err
		}
	} else {
		for _, command := range [][]string{{"create"}, {"add", "host", "cifs"}} {
			args := []string{"--option=kerberos method=dedicated keytab", "--option=dedicated keytab file=" + path,
				"--machine-pass", "--use-kerberos=required", "ads", "keytab"}
			args = append(args, command...)
			if _, err := s.command(ctx, "/usr/local/bin/net", s.netArgs(ad, false, nil, args...), "", nil); err != nil {
				return nil, err
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if native {
		err = validateNativeMachineKeytab(data, ad, hostnames...)
	} else {
		err = validateLegacyMachineKeytab(data, ad)
	}
	if err != nil {
		return nil, err
	}
	if err := utils.AtomicWriteFile(s.machineKeytabPath, data, 0600); err != nil {
		return nil, err
	}
	return data, nil
}

// SambaKeytabHostnames reads canonical testparm output, not raw administrator
// configuration. Alias flags apply only to spn_prefixes, so explicit spns rules
// must expand the effective hostname settings themselves.
func SambaKeytabHostnames(text, domain string) ([]string, error) {
	hostnames := []string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.Join(strings.Fields(key), ""))
		switch key {
		case "dnshostname", "additionaldnshostnames":
			for _, host := range strings.Fields(value) {
				if !hostname.MatchString(host) {
					return nil, invalid("unsupported Samba DNS hostname in machine keytab configuration")
				}
				hostnames = append(hostnames, host)
			}
		case "netbiosaliases":
			for _, alias := range strings.Fields(value) {
				if !netbiosName.MatchString(alias) {
					return nil, invalid("unsupported Samba NetBIOS alias in machine keytab configuration")
				}
				hostnames = append(hostnames, alias, strings.ToLower(alias+"."+domain))
			}
		}
	}
	return hostnames, nil
}

func (s *Service) sambaKeytabHostnames(ctx context.Context, ad directoryModels.ActiveDirectory) ([]string, error) {
	text, err := s.command(ctx, "/usr/local/bin/testparm", []string{"-s", s.smbPath,
		"--option=realm=" + ad.Domain, "--option=netbios name=" + ad.ComputerName}, "", nil)
	if err != nil {
		return nil, err
	}
	return SambaKeytabHostnames(text, ad.Domain)
}

func validateNativeMachineKeytab(data []byte, ad directoryModels.ActiveDirectory, hostnames ...string) error {
	entries, err := parseKeytab(data)
	if err != nil {
		return err
	}
	principals := machineKeytabPrincipals(ad, hostnames...)
	current := map[string]uint32{}
	var kvno uint32
	for _, entry := range entries {
		if !slices.Contains(principals, entry.principal) {
			continue
		}
		if entry.kvno > kvno {
			kvno = entry.kvno
		}
		if entry.kvno > current[entry.principal] {
			current[entry.principal] = entry.kvno
		}
	}
	for _, principal := range principals {
		if kvno == 0 || current[principal] != kvno {
			return fmt.Errorf("AD machine keytab must contain current machine, host, and CIFS keys for every configured hostname")
		}
	}
	return nil
}

func validateLegacyMachineKeytab(data []byte, ad directoryModels.ActiveDirectory) error {
	entries, err := parseKeytab(data)
	if err != nil {
		return err
	}
	var kvno uint32
	machine := ad.ComputerName + "$@" + ad.Domain
	for _, entry := range entries {
		if strings.EqualFold(entry.principal, machine) && entry.kvno > kvno {
			kvno = entry.kvno
		}
	}
	var hostKey, cifsKey bool
	for _, entry := range entries {
		name, realm, _ := strings.Cut(entry.principal, "@")
		service, host, ok := strings.Cut(name, "/")
		// Bare service prefixes generate both short and DNS names. Require the
		// member's own short-name keys, not unrelated or previous-KVNO SPNs.
		if ok && strings.EqualFold(host, ad.ComputerName) && strings.EqualFold(realm, ad.Domain) && entry.kvno == kvno {
			switch strings.ToLower(service) {
			case "host":
				hostKey = true
			case "cifs":
				cifsKey = true
			}
		}
	}
	if kvno == 0 || !hostKey || !cifsKey {
		return fmt.Errorf("AD machine keytab must contain current machine, host, and CIFS keys")
	}
	return nil
}
