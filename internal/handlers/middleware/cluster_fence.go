// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

type mutationAdmission interface {
	EnterMutation(context.Context) (context.Context, func(), error)
}

var mutationAdmissionBypass = map[string]map[string]struct{}{
	http.MethodPost: {
		"/api/auth/login":                 {},
		"/api/auth/passkeys/login/begin":  {},
		"/api/auth/passkeys/login/finish": {},
		"/api/auth/logout":                {},
		"/api/auth/sse-tokens":            {},
		"/api/basic/system/reboot":        {},
		"/api/intra-cluster/leave":        {},
		"/api/cluster/membership-status":  {},
	},
	http.MethodDelete: {
		"/api/cluster/reset-node":       {},
		"/api/cluster/reset-node/force": {},
	},
}

func clusterMutationRequiresAdmission(method string, path string) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return method == http.MethodGet && isConsoleWebSocketPath(path)
	}
	if paths, exists := mutationAdmissionBypass[method]; exists {
		if _, bypass := paths[path]; bypass {
			return false
		}
	}
	return true
}

func ClusterMutationAdmission(service mutationAdmission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !clusterMutationRequiresAdmission(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		ctx, release, err := service.EnterMutation(c.Request.Context())
		if err != nil {
			code := "node_leave_fenced"
			if !errors.Is(err, cluster.ErrNodeLeaveFenced) {
				code = "mutation_admission_failed"
			}
			c.AbortWithStatusJSON(http.StatusLocked, internal.APIResponse[any]{
				Status: "error", Message: code, Error: code, Data: nil,
			})
			return
		}
		defer release()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
