// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	"fmt"
	"strconv"
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

// JailCreateOverrides contains explicitly supplied core jail fields. Nil fields
// retain the corresponding value from a JSON request file.
type JailCreateOverrides struct {
	CTID      *uint
	Name      *string
	Pool      *string
	Base      *string
	Bootstrap *string
	Switch    *string
	Type      *string
}

// LoadJailCreateRequest reads a strict JSON jail creation request from path.
func LoadJailCreateRequest(path string) (jailServiceInterfaces.CreateJailRequest, error) {
	return loadStrictJSONFile[jailServiceInterfaces.CreateJailRequest](path, "jail create")
}

// ParseBootstrapVersion parses a FreeBSD version in major.minor form.
func ParseBootstrapVersion(version string) (int, int, error) {
	version = strings.TrimSpace(version)
	if strings.Count(version, ".") != 1 {
		return 0, 0, fmt.Errorf("invalid bootstrap version %q: expected major.minor (for example, 15.0)", version)
	}

	majorText, minorText, _ := strings.Cut(version, ".")
	major, err := strconv.Atoi(majorText)
	if err != nil || major < 1 {
		return 0, 0, fmt.Errorf("invalid bootstrap version %q: expected major.minor (for example, 15.0)", version)
	}
	minor, err := strconv.Atoi(minorText)
	if err != nil || minor < 0 {
		return 0, 0, fmt.Errorf("invalid bootstrap version %q: expected major.minor (for example, 15.0)", version)
	}

	return major, minor, nil
}

// BuildJailCreateRequest combines an optional complete JSON request with
// explicit core overrides. Without a file, the overrides must provide every
// core field.
func BuildJailCreateRequest(path string, overrides JailCreateOverrides) (jailServiceInterfaces.CreateJailRequest, error) {
	request := jailServiceInterfaces.CreateJailRequest{}
	if path = strings.TrimSpace(path); path != "" {
		var err error
		request, err = LoadJailCreateRequest(path)
		if err != nil {
			return jailServiceInterfaces.CreateJailRequest{}, err
		}
	}

	if overrides.CTID != nil {
		value := *overrides.CTID
		request.CTID = &value
	}
	if overrides.Name != nil {
		request.Name = *overrides.Name
	}
	if overrides.Pool != nil {
		request.Pool = *overrides.Pool
	}
	if overrides.Switch != nil {
		request.SwitchName = *overrides.Switch
	}
	if overrides.Type != nil {
		request.Type = jailModels.JailType(*overrides.Type)
	}
	if overrides.Base != nil && overrides.Bootstrap != nil {
		return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("specify exactly one of --base or --bootstrap")
	}
	if overrides.Base != nil {
		request.Base = *overrides.Base
		request.BootstrapName = ""
	}
	if overrides.Bootstrap != nil {
		request.Base = ""
		request.BootstrapName = *overrides.Bootstrap
	}

	return normalizeJailCreateCoreFields(request)
}

func normalizeJailCreateCoreFields(request jailServiceInterfaces.CreateJailRequest) (jailServiceInterfaces.CreateJailRequest, error) {
	if request.CTID == nil || *request.CTID < 1 || *request.CTID > 9999 {
		return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("jail CTID must be between 1 and 9999")
	}

	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("jail name is required")
	}
	request.Pool = strings.TrimSpace(request.Pool)
	if request.Pool == "" {
		return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("jail pool is required")
	}

	request.Base = strings.TrimSpace(request.Base)
	request.BootstrapName = strings.TrimSpace(request.BootstrapName)
	if (request.Base == "") == (request.BootstrapName == "") {
		return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("specify exactly one base or bootstrap source")
	}

	request.SwitchName = strings.TrimSpace(request.SwitchName)
	if request.SwitchName == "" {
		return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("jail switch is required")
	}
	if strings.EqualFold(request.SwitchName, "none") {
		request.SwitchName = "none"
	} else if strings.EqualFold(request.SwitchName, "inherit") {
		request.SwitchName = "inherit"
	}

	request.Type = jailModels.JailType(strings.ToLower(strings.TrimSpace(string(request.Type))))
	if request.Type != jailModels.JailTypeFreeBSD && request.Type != jailModels.JailTypeLinux {
		return jailServiceInterfaces.CreateJailRequest{}, fmt.Errorf("jail type must be either freebsd or linux")
	}

	return request, nil
}
