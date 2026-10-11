// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"strings"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
)

func validateJoinUsername(username string) error {
	if username == "" || len(username) > 512 || strings.ContainsAny(username, "\x00\r\n%\\") || strings.HasPrefix(username, "-") {
		return invalid("join username and password are required; use user or user@REALM")
	}
	return nil
}

func validateJoinCredentials(username, password string) error {
	if err := validateJoinUsername(username); err != nil {
		return err
	}
	if password == "" || len(password) > 4096 || strings.ContainsAny(password, "\x00\r\n") {
		return invalid("join username and password are required; use user or user@REALM")
	}
	return nil
}

func joinPasswordPurpose(ad directoryModels.ActiveDirectory) string {
	return "sylve-ad-join-password-v1\x00" + ad.Domain + "\x00" + ad.JoinUsername
}

func (s *Service) saveJoinCredentials(row *directoryModels.ActiveDirectory, old directoryModels.ActiveDirectory, input ADInput) error {
	password := ""
	if input.JoinPassword != nil {
		password = *input.JoinPassword
	}
	if row.KeytabID != 0 {
		if password != "" || (input.JoinUsername != nil && strings.TrimSpace(*input.JoinUsername) != "") {
			return invalid("select either a keytab or username/password enrollment")
		}
		return nil
	}
	row.JoinUsername = old.JoinUsername
	if input.JoinUsername != nil {
		row.JoinUsername = strings.TrimSpace(*input.JoinUsername)
	}
	if input.ClearJoinPassword && password != "" {
		return invalid("cannot replace and clear the join password together")
	}
	if row.JoinUsername != "" {
		// Validate the username even when the password is being retained or cleared.
		if err := validateJoinUsername(row.JoinUsername); err != nil {
			return err
		}
	}
	if password != "" {
		if err := validateJoinCredentials(row.JoinUsername, password); err != nil {
			return err
		}
		ciphertext, err := s.encryptSecret([]byte(password), joinPasswordPurpose(*row))
		if err != nil {
			return err
		}
		row.JoinPasswordCiphertext = ciphertext
	} else if !input.ClearJoinPassword && row.JoinUsername != "" {
		if row.Domain != old.Domain || row.JoinUsername != old.JoinUsername {
			return invalid("enter a new join password when changing the username or domain, or explicitly clear it")
		}
		row.JoinPasswordCiphertext = old.JoinPasswordCiphertext
	}
	row.JoinPasswordConfigured = len(row.JoinPasswordCiphertext) > 0
	return nil
}
