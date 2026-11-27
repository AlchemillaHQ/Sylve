// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"strings"

	"github.com/alchemillahq/sylve/pkg/utils"
)

const sambaControlPath = "/usr/local/bin/smbcontrol"

var sambaControlCommand = utils.RunCommand

func sambaControl(daemon, message string) (string, error) {
	return sambaControlCommand(sambaControlPath, daemon, message, "--configfile="+sambaConfigFilePath)
}

func sambaSmbdResponds() bool {
	output, err := sambaControl("smbd", "ping")
	return err == nil && strings.Contains(output, "PONG")
}

func reloadSambaDaemons() error {
	_, err := sambaControl("all", "reload-config")
	return err
}
