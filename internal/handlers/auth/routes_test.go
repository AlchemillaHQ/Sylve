// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package authHandlers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readAuthContractSource(t *testing.T, relativePath ...string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve auth route contract test path")
	}
	path := filepath.Join(append([]string{filepath.Dir(filename)}, relativePath...)...)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}

func TestLoginLifecycleRouteContract(t *testing.T) {
	routes := readAuthContractSource(t, "..", "routes.go")
	authHandlers := readAuthContractSource(t, "auth.go")
	passkeyHandlers := readAuthContractSource(t, "passkeys.go")
	eventHandlers := readAuthContractSource(t, "..", "events", "events.go")

	registrations := []string{
		`api.GET("/auth/login/config", authHandlers.LoginConfigHandler())`,
		`publicAuth.POST("/login", authHandlers.LoginHandler(authService))`,
		`publicAuth.POST("/passkeys/login/begin", authHandlers.BeginPasskeyLoginHandler(authService))`,
		`publicAuth.POST("/passkeys/login/finish", authHandlers.FinishPasskeyLoginHandler(authService))`,
		`authSession.POST("/logout", authHandlers.LogoutHandler(authService))`,
		`authSession.POST("/sse-tokens", eventsHandlers.CreateSSEToken(authService))`,
		`events.GET("/stream", eventsHandlers.StreamSSE())`,
	}
	for _, registration := range registrations {
		if !strings.Contains(routes, registration) {
			t.Errorf("missing route registration: %s", registration)
		}
	}
	for _, retired := range []string{`auth.GET("/logout"`, `auth.GET("/sse-token"`} {
		if strings.Contains(routes, retired) {
			t.Errorf("retired route is still registered: %s", retired)
		}
	}

	authIndex := strings.Index(routes, "authManagement.Use(middleware.EnsureAuthenticated(authService))")
	limitIndex := strings.Index(routes, "authManagement.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))")
	adminIndex := strings.Index(routes, "authManagement.Use(middleware.RequireLocalAdmin(authService))")
	routingIndex := strings.Index(routes, "authManagement.Use(EnsureCorrectHost(db, authService))")
	loggerIndex := strings.Index(routes, "authManagement.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))")
	if authIndex < 0 || limitIndex < 0 || adminIndex < 0 || routingIndex < 0 || loggerIndex < 0 ||
		!(authIndex < limitIndex && limitIndex < adminIndex && adminIndex < routingIndex && routingIndex < loggerIndex) {
		t.Error("auth management middleware must authenticate, limit, authorize, route, then log")
	}
	for _, middleware := range []string{
		`authSession.Use(middleware.EnsureAuthenticated(authService))`,
		`authSession.Use(middleware.RequireLocalSession())`,
	} {
		if !strings.Contains(routes, middleware) {
			t.Errorf("missing local-session middleware: %s", middleware)
		}
	}

	annotations := map[string]string{
		"GET /auth/login/config":           authHandlers,
		"POST /auth/login":                 authHandlers,
		"POST /auth/passkeys/login/begin":  passkeyHandlers,
		"POST /auth/passkeys/login/finish": passkeyHandlers,
		"POST /auth/logout":                authHandlers,
		"POST /auth/sse-tokens":            eventHandlers,
	}
	for contract, source := range annotations {
		method, path, _ := strings.Cut(contract, " ")
		annotation := "// @Router " + path + " [" + strings.ToLower(method) + "]"
		if !strings.Contains(source, annotation) {
			t.Errorf("missing source annotation: %s", annotation)
		}
	}
}

func TestUserCollectionRouteContract(t *testing.T) {
	routes := readAuthContractSource(t, "..", "routes.go")
	localHandlers := readAuthContractSource(t, "local.go")

	registrations := []string{
		`users.GET("", authHandlers.ListUsersHandler(authService))`,
		`users.POST("", authHandlers.CreateUserHandler(authService))`,
		`users.PUT("/:userId", authHandlers.EditUserHandler(authService))`,
		`users.DELETE("/:userId", authHandlers.DeleteUserHandler(authService))`,
		`users.GET("/uid/next", authHandlers.GetNextUIDHandler(authService))`,
		`users.GET("/capabilities", authHandlers.UserCapabilitiesHandler())`,
		`users.GET("/importable", authHandlers.ListImportableUsersHandler(authService))`,
		`users.POST("/import", authHandlers.ImportUserHandler(authService))`,
		`users.POST("/pam", authHandlers.CreatePamUserHandler(authService))`,
	}
	for _, registration := range registrations {
		if !strings.Contains(routes, registration) {
			t.Errorf("missing user route registration: %s", registration)
		}
	}
	for _, retired := range []string{`users.PUT(""`, `users.DELETE("/:id"`} {
		if strings.Contains(routes, retired) {
			t.Errorf("retired user route is still registered: %s", retired)
		}
	}

	for _, annotation := range []string{
		"// @Router /auth/users [get]",
		"// @Router /auth/users [post]",
		"// @Router /auth/users/{userId} [put]",
		"// @Router /auth/users/{userId} [delete]",
		"// @Router /auth/users/uid/next [get]",
		"// @Router /auth/users/capabilities [get]",
		"// @Router /auth/users/importable [get]",
		"// @Router /auth/users/import [post]",
		"// @Router /auth/users/pam [post]",
	} {
		if !strings.Contains(localHandlers, annotation) {
			t.Errorf("missing source annotation: %s", annotation)
		}
	}
}

func TestGroupRouteContract(t *testing.T) {
	routes := readAuthContractSource(t, "..", "routes.go")
	groupHandlers := readAuthContractSource(t, "groups.go")

	registrations := []string{
		`groups.GET("", authHandlers.ListGroupsHandler(authService))`,
		`groups.POST("", authHandlers.CreateGroupHandler(authService))`,
		`groups.DELETE("/:groupId", authHandlers.DeleteGroupHandler(authService))`,
		`groups.PUT("/:groupId/members", authHandlers.UpdateGroupMembersHandler(authService))`,
	}
	for _, registration := range registrations {
		if !strings.Contains(routes, registration) {
			t.Errorf("missing group route registration: %s", registration)
		}
	}
	for _, retired := range []string{`groups.POST("/users"`, `groups.PUT("/users"`, `groups.DELETE("/:id"`} {
		if strings.Contains(routes, retired) {
			t.Errorf("retired group route is still registered: %s", retired)
		}
	}

	for _, annotation := range []string{
		"// @Router /auth/groups [get]",
		"// @Router /auth/groups [post]",
		"// @Router /auth/groups/{groupId} [delete]",
		"// @Router /auth/groups/{groupId}/members [put]",
	} {
		if !strings.Contains(groupHandlers, annotation) {
			t.Errorf("missing source annotation: %s", annotation)
		}
	}
}

func TestPasskeyManagementRouteContract(t *testing.T) {
	routes := readAuthContractSource(t, "..", "routes.go")
	passkeyHandlers := readAuthContractSource(t, "passkeys.go")

	registrations := []string{
		`passkeys.POST("/register/begin", authHandlers.BeginPasskeyRegistrationHandler(authService))`,
		`passkeys.POST("/register/finish", authHandlers.FinishPasskeyRegistrationHandler(authService))`,
		`users.GET("/:userId/passkeys", authHandlers.ListUserPasskeysHandler(authService))`,
		`users.DELETE("/:userId/passkeys/:credentialId", authHandlers.DeleteUserPasskeyHandler(authService))`,
	}
	for _, registration := range registrations {
		if !strings.Contains(routes, registration) {
			t.Errorf("missing passkey route registration: %s", registration)
		}
	}
	for _, retired := range []string{
		`passkeys.GET("/users/:id"`,
		`passkeys.DELETE("/users/:id/:credentialId"`,
	} {
		if strings.Contains(routes, retired) {
			t.Errorf("retired passkey route is still registered: %s", retired)
		}
	}

	for _, annotation := range []string{
		"// @Router /auth/passkeys/register/begin [post]",
		"// @Router /auth/passkeys/register/finish [post]",
		"// @Router /auth/users/{userId}/passkeys [get]",
		"// @Router /auth/users/{userId}/passkeys/{credentialId} [delete]",
	} {
		if !strings.Contains(passkeyHandlers, annotation) {
			t.Errorf("missing source annotation: %s", annotation)
		}
	}
}
