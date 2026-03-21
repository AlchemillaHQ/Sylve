//go:build linux || freebsd

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"errors"

	"github.com/msteinert/pam"
)

func (s *Service) AuthenticatePAM(username, password string) (bool, error) {
	t, err := pam.StartFunc("login", username, func(s pam.Style, msg string) (string, error) {
		switch s {
		case pam.PromptEchoOff:
			return password, nil
		case pam.PromptEchoOn:
			return "", errors.New("unexpected prompt for input")
		case pam.ErrorMsg:
			return "", nil
		case pam.TextInfo:
			return "", nil
		default:
			return "", errors.New("unrecognized message style")
		}
	})

	if err != nil {
		return false, err
	}

	err = t.Authenticate(0)
	if err != nil {
		return false, err
	}

	return true, nil
}
