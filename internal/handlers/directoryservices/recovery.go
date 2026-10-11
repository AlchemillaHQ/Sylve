// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryServiceHandlers

import (
	"context"
	"time"

	"github.com/alchemillahq/sylve/internal/services/directoryservices"
	"github.com/gin-gonic/gin"
)

func GetRecoveryInfo(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		respond(c, directoryservices.RecoveryInfo{Pending: s.RecoveryPending()}, nil)
	}
}

func ExportRecovery(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input directoryservices.RecoveryInput
		if !bind(c, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
		defer cancel()
		file, err := s.ExportRecovery(ctx, input)
		warning := ""
		if err != nil && len(file) > 0 {
			// A completed encrypted backup remains downloadable if resuming
			// services fails. Surface that failure separately from its contents.
			warning, err = err.Error(), nil
		}
		c.Header("Cache-Control", "no-store")
		respond(c, struct {
			File    []byte `json:"file"`
			Warning string `json:"warning,omitempty"`
		}{File: file, Warning: warning}, err)
	}
}

func RestoreRecovery(s *directoryservices.Service) gin.HandlerFunc {
	return recoveryMutation(s, false)
}

func RollbackRecovery(s *directoryservices.Service) gin.HandlerFunc {
	return recoveryMutation(s, true)
}

func recoveryMutation(s *directoryservices.Service, rollback bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input directoryservices.RecoveryInput
		if !bind(c, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
		defer cancel()
		var err error
		if rollback {
			err = s.RollbackRecovery(ctx, input)
		} else {
			err = s.RestoreRecovery(ctx, input)
		}
		c.Header("Cache-Control", "no-store")
		respond(c, nil, err)
	}
}
