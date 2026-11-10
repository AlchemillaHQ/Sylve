// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
)

const (
	RealmPAM   = "pam"
	RealmSylve = "sylve"

	RoleAdministrator = "Administrator"
	RoleNoAccess      = "NoAccess"
)

var guestPrivileges = []string{
	GuestAudit, GuestPowerMgmt, GuestConsole, GuestConfigCPU, GuestConfigMemory, GuestConfigDisk,
	GuestConfigNetwork, GuestConfigCloudinit, GuestConfigHWType, GuestConfigOptions, GuestAllocate,
	GuestClone, GuestMigrate, GuestSnapshot, GuestSnapshotRollback,
}

var Privileges = slices.Concat(guestPrivileges, []string{
	DatastoreAudit, DatastoreAllocateSpace, DatastoreAllocateTemplate, NetworkAudit, NetworkUse,
	SysAudit, SysSyslog, SysConsole, SysModify, SysPowerMgmt, UserModify, PermissionsModify,
})

// Built-in roles can't be changed, so a privilege added later reaches them on upgrade.
var BuiltinRoles = map[string][]string{
	RoleAdministrator: Privileges,
	RoleNoAccess:      {},
	"Auditor":         {GuestAudit, DatastoreAudit, NetworkAudit, SysAudit, SysSyslog},
	"GuestAdmin":      slices.Concat(guestPrivileges, []string{DatastoreAudit, DatastoreAllocateSpace, NetworkAudit, NetworkUse}),
	"GuestUser":       {GuestAudit, GuestPowerMgmt, GuestConsole, GuestConfigCloudinit},
	"DatastoreUser":   {DatastoreAudit, DatastoreAllocateSpace, DatastoreAllocateTemplate},
	"SysAdmin":        {SysAudit, SysSyslog, SysConsole, SysModify, SysPowerMgmt},
}

type Principal struct {
	Username  string
	Realm     string
	Superuser bool
}

func (p Principal) Subject() string {
	return p.Username + "@" + p.Realm
}

// root@pam and the admin seeded from config.json skip every check, like Proxmox's root@pam.
func isSuperuser(username, realm string) bool {
	return (username == "root" && realm == RealmPAM) || (username == "admin" && realm == RealmSylve)
}

// A realm user's users row is named <username>@<realm>; native names can't contain "@".
func PrincipalOf(user *models.User) Principal {
	username, realm := user.Username, RealmSylve
	switch user.Source {
	case "pam":
		realm = RealmPAM
	case clusterModels.UserSourceOIDC:
		username, realm = splitSubject(user.Username)
	}
	return Principal{Username: username, Realm: realm, Superuser: isSuperuser(username, realm)}
}

// Realm names have no "@", so a realm user's name may: alice@example.com@google.
func splitSubject(subject string) (name, realm string) {
	at := strings.LastIndex(subject, "@")
	if at < 0 {
		return subject, ""
	}
	return subject[:at], subject[at+1:]
}

// A forwarded user is who the node they logged in at says they are.
func ForwardedPrincipal(user ProxiedUser) Principal {
	realm := user.Realm
	if realm == "" {
		realm = RealmSylve
		if user.AuthType == "pam" {
			realm = RealmPAM
		}
	}
	return Principal{Username: user.Username, Realm: realm, Superuser: isSuperuser(user.Username, realm)}
}

func (s *Service) groupsOf(subject string) ([]string, error) {
	var groups []clusterModels.ACLGroup
	if err := s.DB.Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("failed_to_load_groups: %w", err)
	}
	mapped, err := s.mappedGroups(subject)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, group := range groups {
		if slices.Contains(group.Members, subject) || mapped[group.Name] {
			names = append(names, group.Name)
		}
	}
	return names, nil
}

// mappedGroups come from the groups the provider gave at the realm user's last login.
func (s *Service) mappedGroups(subject string) (map[string]bool, error) {
	name, realmName := splitSubject(subject)
	if realmName == RealmPAM || realmName == RealmSylve {
		return nil, nil
	}
	var user clusterModels.OIDCUser
	if err := s.DB.Where("realm = ? AND username = ?", realmName, name).Limit(1).Find(&user).Error; err != nil {
		return nil, fmt.Errorf("failed_to_load_realm_user: %w", err)
	}
	var realm clusterModels.AuthRealm
	if err := s.DB.Where("name = ?", realmName).Limit(1).Find(&realm).Error; err != nil {
		return nil, fmt.Errorf("failed_to_load_realm: %w", err)
	}
	mapped := map[string]bool{}
	for _, mapping := range realm.GroupMappings {
		if slices.Contains(user.Groups, mapping.Claim) {
			mapped[mapping.Group] = true
		}
	}
	return mapped, nil
}

func (s *Service) grantsFor(p Principal) ([]clusterModels.ACLGrant, error) {
	groups, err := s.groupsOf(p.Subject())
	if err != nil {
		return nil, err
	}
	query := s.DB.Where("subject_type = ? AND subject = ?", clusterModels.ACLSubjectUser, p.Subject())
	if len(groups) > 0 {
		query = query.Or("subject_type = ? AND subject IN ?", clusterModels.ACLSubjectGroup, groups)
	}
	var grants []clusterModels.ACLGrant
	if err := query.Find(&grants).Error; err != nil {
		return nil, fmt.Errorf("failed_to_load_grants: %w", err)
	}
	return grants, nil
}

func pathPrefixes(path string) []string {
	prefixes := []string{"/"}
	current := ""
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part != "" {
			current += "/" + part
			prefixes = append(prefixes, current)
		}
	}
	return prefixes
}

// rolesOn applies rules from "/" down to path, over grants that already belong to the
// principal: a grant applies to its path and, if propagated, below it; a deeper path's grants
// replace inherited ones; on one path the user's grants replace their groups', and the groups'
// grants add up; NoAccess removes everything.
func rolesOn(grants []clusterModels.ACLGrant, path string) []string {
	var roles []string
	for _, prefix := range pathPrefixes(path) {
		var user, group []string
		for _, grant := range grants {
			if grant.Path != prefix || (prefix != path && !grant.Propagate) {
				continue
			}
			if grant.SubjectType == clusterModels.ACLSubjectUser {
				user = append(user, grant.Role)
			} else {
				group = append(group, grant.Role)
			}
		}
		if len(user) > 0 {
			roles = user
		} else if len(group) > 0 {
			roles = group
		}
	}
	if slices.Contains(roles, RoleNoAccess) {
		return nil
	}
	return roles
}

func (s *Service) rolePrivileges(roles []string) (map[string]bool, error) {
	held := make(map[string]bool)
	var custom []string
	for _, role := range roles {
		if privileges, ok := BuiltinRoles[role]; ok {
			for _, privilege := range privileges {
				held[privilege] = true
			}
		} else {
			custom = append(custom, role)
		}
	}
	if len(custom) > 0 {
		var stored []clusterModels.ACLRole
		if err := s.DB.Where("name IN ?", custom).Find(&stored).Error; err != nil {
			return nil, fmt.Errorf("failed_to_load_roles: %w", err)
		}
		for _, role := range stored {
			for _, privilege := range role.Privileges {
				held[privilege] = true
			}
		}
	}
	return held, nil
}

// Covers names a privilege q holds that p doesn't, and where; it returns "" when p holds all of
// q's. Privileges only change at a grant's path, so "/", each grant path of either and the paths
// just below them ("<path>/*", which no grant names) stand for every path.
func (s *Service) Covers(p, q Principal) (path, privilege string, err error) {
	if p.Superuser {
		return "", "", nil
	}
	pGrants, err := s.grantsFor(p)
	if err != nil {
		return "", "", err
	}
	qGrants, err := s.grantsFor(q)
	if err != nil {
		return "", "", err
	}
	paths := map[string]bool{"/": true}
	for _, grant := range slices.Concat(pGrants, qGrants) {
		paths[grant.Path] = true
	}
	for _, base := range slices.Sorted(maps.Keys(paths)) {
		for _, at := range []string{base, strings.TrimSuffix(base, "/") + "/*"} {
			qHeld, err := s.rolePrivileges(rolesOn(qGrants, at))
			if err != nil {
				return "", "", err
			}
			pHeld, err := s.rolePrivileges(rolesOn(pGrants, at))
			if err != nil {
				return "", "", err
			}
			for _, privilege := range Privileges {
				if (q.Superuser || qHeld[privilege]) && !pHeld[privilege] {
					return at, privilege, nil
				}
			}
		}
	}
	return "", "", nil
}

func (s *Service) RolePrivileges(role string) ([]string, error) {
	held, err := s.rolePrivileges([]string{role})
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(held)), nil
}

func (s *Service) Can(p Principal, path string, privileges ...string) (bool, error) {
	can, err := s.Checker(p)
	if err != nil {
		return false, err
	}
	return can(path, privileges...)
}

func (s *Service) Checker(p Principal) (func(path string, privileges ...string) (bool, error), error) {
	if p.Superuser {
		return func(string, ...string) (bool, error) { return true, nil }, nil
	}
	grants, err := s.grantsFor(p)
	if err != nil {
		return nil, err
	}
	return func(path string, privileges ...string) (bool, error) {
		held, err := s.rolePrivileges(rolesOn(grants, path))
		if err != nil {
			return false, err
		}
		for _, privilege := range privileges {
			if !held[privilege] {
				return false, nil
			}
		}
		return true, nil
	}, nil
}

const adminGrantsMigration = "rbac_admin_grants"

// Until this node's admins are migrated to grants, the admin flag decides who may log in, and
// whoever does may do anything. A nil service, as in handler tests, has no RBAC.
func (s *Service) RBACActive() bool {
	if s == nil {
		return false
	}
	if s.rbacActive.Load() {
		return true
	}
	var count int64
	if err := s.DB.Model(&models.Migrations{}).Where("name = ?", adminGrantsMigration).Count(&count).Error; err != nil || count == 0 {
		return false
	}
	s.rbacActive.Store(true)
	return true
}

func (s *Service) loginRefusal(user *models.User) (string, error) {
	if !s.RBACActive() {
		if user.Admin {
			return "", nil
		}
		return "admin_required", fmt.Errorf("only_admin_allowed")
	}
	allowed, err := s.HasAccess(PrincipalOf(user))
	if err != nil {
		return "access_check_failed", err
	}
	if !allowed {
		return "no_permissions", fmt.Errorf("no_permissions")
	}
	return "", nil
}

func (s *Service) migrateAdminGrants(ctx context.Context) error {
	if s.RBACActive() {
		return nil
	}
	var admins []models.User
	if err := s.DB.Where("admin = ?", true).Find(&admins).Error; err != nil {
		return fmt.Errorf("failed_to_list_admins: %w", err)
	}
	for i := range admins {
		principal := PrincipalOf(&admins[i])
		if principal.Superuser {
			continue
		}
		grant := clusterModels.ACLGrant{
			Path: "/", SubjectType: clusterModels.ACLSubjectUser, Subject: principal.Subject(),
			Role: RoleAdministrator, Propagate: true,
		}
		if err := s.propose(ctx, "acl_grant", "set", grant); err != nil {
			return fmt.Errorf("failed_to_grant_%s: %w", grant.Subject, err)
		}
	}
	if err := s.DB.Create(&models.Migrations{Name: adminGrantsMigration}).Error; err != nil {
		return fmt.Errorf("failed_to_record_admin_migration: %w", err)
	}
	s.rbacActive.Store(true)
	logger.L.Info().Int("admins", len(admins)).Msg("rbac_admins_migrated")
	return nil
}

// In a cluster the commands wait until every member runs a build that knows them, so this retries.
func (s *Service) MigrateAdminGrants(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		err := s.migrateAdminGrants(ctx)
		if err == nil {
			return
		}
		logger.L.Warn().Err(err).Msg("rbac_admin_migration_deferred")
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Anyone granted anything but NoAccess may log in, as may a superuser.
func (s *Service) HasAccess(p Principal) (bool, error) {
	if p.Superuser {
		return true, nil
	}
	grants, err := s.grantsFor(p)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(grants, func(grant clusterModels.ACLGrant) bool {
		return grant.Role != RoleNoAccess
	}), nil
}
