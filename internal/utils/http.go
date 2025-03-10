// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func GetTokenFromHeader(r http.Header) (string, error) {
	token := r.Get("Authorization")

	if token == "" {
		return "", fmt.Errorf("no token provided")
	}

	if strings.HasPrefix(token, "Bearer ") {
		token = RemoveSpaces(token[7:])
	}

	return token, nil
}

func SendJSONResponse(c *gin.Context, httpCode int, data interface{}) {
	if data == nil {
		data = gin.H{}
	}

	c.JSON(httpCode, data)
}
