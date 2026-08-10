// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"

	"github.com/gin-gonic/gin"
)

func bindJailJSON(c *gin.Context, target any, invalidMessage string) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		status := http.StatusBadRequest
		message := invalidMessage
		detail := err.Error()

		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			status = http.StatusRequestEntityTooLarge
			message = "request_body_too_large"
			detail = "request_body_too_large"
		}

		c.JSON(status, internal.APIResponse[any]{
			Status:  "error",
			Message: message,
			Error:   detail,
			Data:    nil,
		})
		return false
	}

	return true
}
