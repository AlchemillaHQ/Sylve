// SPDX-License-Identifier: BSD-2-Clause

package remoteexec

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"
)

type Command struct{ script string }

func NewCommand(argv ...string) (Command, error) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return Command{}, errors.New("remote_command_required")
	}

	var script strings.Builder
	script.WriteString("exec")
	for _, arg := range argv {
		if !validShellText(arg) {
			return Command{}, errors.New("remote_command_argument_invalid")
		}
		script.WriteByte(' ')
		script.WriteString(quotePOSIX(arg))
	}
	return Command{script: script.String()}, nil
}

func NewScript(script string) (Command, error) {
	if strings.TrimSpace(script) == "" {
		return Command{}, errors.New("remote_script_required")
	}
	if !validShellText(script) {
		return Command{}, errors.New("remote_script_invalid")
	}
	return Command{script: script}, nil
}

func (command Command) SSHArgument() (string, error) {
	if command.script == "" {
		return "", errors.New("remote_command_required")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(command.script))
	return `/bin/sh -c 'eval "$(/usr/bin/printf %s "$1" | /usr/bin/base64 -d)"' sh ` + encoded, nil
}

func (command Command) SSHArgs(base []string, destination SSHDestination, readsStdin bool) ([]string, error) {
	if destination.String() == "" {
		return nil, errors.New("invalid_ssh_destination")
	}
	remoteArgument, err := command.SSHArgument()
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, len(base)+2)
	for _, arg := range base {
		if readsStdin && arg == "-n" {
			continue
		}
		args = append(args, arg)
	}
	return append(args, destination.String(), remoteArgument), nil
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validShellText(value string) bool {
	return !strings.ContainsRune(value, 0) && utf8.ValidString(value)
}
