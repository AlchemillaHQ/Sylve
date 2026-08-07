// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkHandlers

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAllNetworkSourceRoutesMountWithoutConflictsAndDocumentAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list network handler sources: %v", err)
	}

	routerAnnotation := regexp.MustCompile(`^// @Router (/network/\S+) \[(get|post|put|patch|delete)\]$`)
	pathParameter := regexp.MustCompile(`\{([^}/]+)\}`)
	registered := make(map[string]struct{})
	router := gin.New()

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}

		var block []string
		for _, line := range strings.Split(string(source), "\n") {
			if strings.HasPrefix(line, "// @Summary ") {
				block = []string{line}
				continue
			}
			if block == nil {
				continue
			}
			block = append(block, line)

			match := routerAnnotation.FindStringSubmatch(line)
			if match == nil {
				continue
			}

			comment := strings.Join(block, "\n")
			if !strings.Contains(comment, "// @Security BearerAuth") {
				t.Errorf("%s %s is missing BearerAuth documentation", strings.ToUpper(match[2]), match[1])
			}
			if !strings.Contains(comment, `// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"`) {
				t.Errorf("%s %s is missing its 401 response", strings.ToUpper(match[2]), match[1])
			}

			method := strings.ToUpper(match[2])
			key := method + " " + match[1]
			if _, duplicate := registered[key]; duplicate {
				t.Errorf("duplicate source route annotation: %s", key)
				block = nil
				continue
			}
			registered[key] = struct{}{}

			ginPath := "/api" + pathParameter.ReplaceAllString(match[1], ":$1")
			router.Handle(method, ginPath, func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			block = nil
		}
	}

	if len(registered) != 59 {
		t.Fatalf("network source route annotations=%d want=59", len(registered))
	}
}
