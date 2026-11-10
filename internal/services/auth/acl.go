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
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type Proposer func(ctx context.Context, commandType, action string, payload any) error

func (s *Service) SetProposer(propose Proposer) {
	s.proposeMu.Lock()
	s.proposer = propose
	s.proposeMu.Unlock()
}

func (s *Service) propose(ctx context.Context, commandType, action string, payload any) error {
	s.proposeMu.RLock()
	propose := s.proposer
	s.proposeMu.RUnlock()
	if propose == nil {
		return fmt.Errorf("acl_writes_unavailable")
	}
	return propose(ctx, commandType, action, payload)
}

// Guest kinds take a guest ID below them; /access has nothing below it.
var grantPathKinds = map[string]bool{
	"nodes": true, "vm": true, "jail": true, "container": true, "storage": true, "switch": true, "access": false,
}

func validGrantPath(path string) bool {
	if path == "/" {
		return true
	}
	if !strings.HasPrefix(path, "/") {
		return false
	}
	parts := strings.Split(path[1:], "/")
	hasChildren, ok := grantPathKinds[parts[0]]
	switch {
	case !ok || len(parts) > 2:
		return false
	case len(parts) == 1:
		return true
	case !hasChildren || parts[1] == "":
		return false
	}
	switch parts[0] {
	case "vm", "jail", "container":
		id, err := strconv.Atoi(parts[1])
		return err == nil && id >= 1 && id <= 9999
	}
	return true
}

// A realm user must exist, so no grant waits for a name the provider could give to someone else.
func (s *Service) validGrantSubject(subjectType, subject string) (bool, error) {
	switch subjectType {
	case clusterModels.ACLSubjectUser:
		name, realm := splitSubject(subject)
		if name == "" || len(name) > 128 {
			return false, nil
		}
		if realm == RealmPAM || realm == RealmSylve {
			return true, nil
		}
		if !validRealmName(realm) {
			return false, nil
		}
		var count int64
		err := s.DB.Model(&clusterModels.OIDCUser{}).Where("realm = ? AND username = ?", realm, name).Count(&count).Error
		return count > 0, err
	case clusterModels.ACLSubjectGroup:
		return subject != "" && len(subject) <= 128, nil
	}
	return false, nil
}

func (s *Service) roleExists(name string) (bool, error) {
	if _, ok := BuiltinRoles[name]; ok {
		return true, nil
	}
	var count int64
	if err := s.DB.Model(&clusterModels.ACLRole{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return false, fmt.Errorf("failed_to_load_roles: %w", err)
	}
	return count > 0, nil
}

func (s *Service) ListGrants() ([]clusterModels.ACLGrant, error) {
	grants := []clusterModels.ACLGrant{}
	if err := s.DB.Order("path, subject_type, subject, role").Find(&grants).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_grants: %w", err)
	}
	return grants, nil
}

func (s *Service) SetGrant(ctx context.Context, grant clusterModels.ACLGrant) error {
	if !validGrantPath(grant.Path) {
		return fmt.Errorf("invalid_acl_path")
	}
	if valid, err := s.validGrantSubject(grant.SubjectType, grant.Subject); err != nil {
		return fmt.Errorf("failed_to_check_subject: %w", err)
	} else if !valid {
		return fmt.Errorf("invalid_acl_subject")
	}
	exists, err := s.roleExists(grant.Role)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("unknown_role")
	}
	if grant.SubjectType == clusterModels.ACLSubjectGroup {
		var count int64
		if err := s.DB.Model(&clusterModels.ACLGroup{}).Where("name = ?", grant.Subject).Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_load_groups: %w", err)
		}
		if count == 0 {
			return fmt.Errorf("unknown_group")
		}
	}
	grant.ID = 0
	return s.propose(ctx, "acl_grant", "set", grant)
}

func (s *Service) DeleteGrant(ctx context.Context, grant clusterModels.ACLGrant) error {
	grant.ID = 0
	return s.propose(ctx, "acl_grant", "delete", grant)
}

type RoleInfo struct {
	Name       string   `json:"name"`
	Privileges []string `json:"privileges"`
	Builtin    bool     `json:"builtin"`
}

func (s *Service) ListRoles() ([]RoleInfo, error) {
	roles := make([]RoleInfo, 0, len(BuiltinRoles))
	for name, privileges := range BuiltinRoles {
		roles = append(roles, RoleInfo{Name: name, Privileges: privileges, Builtin: true})
	}
	var custom []clusterModels.ACLRole
	if err := s.DB.Find(&custom).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_roles: %w", err)
	}
	for _, role := range custom {
		roles = append(roles, RoleInfo{Name: role.Name, Privileges: role.Privileges})
	}
	slices.SortFunc(roles, func(a, b RoleInfo) int { return strings.Compare(a.Name, b.Name) })
	return roles, nil
}

var aclNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,63}$`)

func (s *Service) SetRole(ctx context.Context, name string, privileges []string) error {
	if !aclNamePattern.MatchString(name) {
		return fmt.Errorf("invalid_role_name")
	}
	if _, ok := BuiltinRoles[name]; ok {
		return fmt.Errorf("builtin_role")
	}
	if len(privileges) == 0 {
		return fmt.Errorf("role_privileges_required")
	}
	for _, privilege := range privileges {
		if !slices.Contains(Privileges, privilege) {
			return fmt.Errorf("unknown_privilege: %s", privilege)
		}
	}
	privileges = slices.Compact(slices.Sorted(slices.Values(privileges)))
	return s.propose(ctx, "acl_role", "set", clusterModels.ACLRole{Name: name, Privileges: privileges})
}

func (s *Service) DeleteRole(ctx context.Context, name string) error {
	if _, ok := BuiltinRoles[name]; ok {
		return fmt.Errorf("builtin_role")
	}
	return s.propose(ctx, "acl_role", "delete", clusterModels.ACLRole{Name: name})
}

func (s *Service) ListACLGroups() ([]clusterModels.ACLGroup, error) {
	groups := []clusterModels.ACLGroup{}
	if err := s.DB.Order("name").Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_groups: %w", err)
	}
	return groups, nil
}

// Members are user@realm names and, outside OIDC realms, needn't exist here: PAM users only exist on
// their own nodes.
func (s *Service) SetACLGroup(ctx context.Context, name string, members []string) error {
	if !aclNamePattern.MatchString(name) {
		return fmt.Errorf("invalid_group_name")
	}
	for _, member := range members {
		if valid, err := s.validGrantSubject(clusterModels.ACLSubjectUser, member); err != nil {
			return fmt.Errorf("failed_to_check_subject: %w", err)
		} else if !valid {
			return fmt.Errorf("invalid_group_member: %s", member)
		}
	}
	members = slices.Compact(slices.Sorted(slices.Values(members)))
	return s.propose(ctx, "acl_group", "set", clusterModels.ACLGroup{Name: name, Members: append([]string{}, members...)})
}

func (s *Service) DeleteACLGroup(ctx context.Context, name string) error {
	return s.propose(ctx, "acl_group", "delete", clusterModels.ACLGroup{Name: name})
}

func (s *Service) referenced(subjectType, subject string) (bool, error) {
	var count int64
	if err := s.DB.Model(&clusterModels.ACLGrant{}).Where("subject_type = ? AND subject = ?", subjectType, subject).Count(&count).Error; err != nil {
		return false, fmt.Errorf("failed_to_load_grants: %w", err)
	}
	if count > 0 || subjectType != clusterModels.ACLSubjectUser {
		return count > 0, nil
	}
	groups, err := s.groupsOf(subject)
	return len(groups) > 0, err
}

// A user or group created later under the same name mustn't inherit a deleted one's grants.
func (s *Service) revokeSubject(subjectType, subject string) error {
	held, err := s.referenced(subjectType, subject)
	if err != nil || !held {
		return err
	}
	return s.propose(context.Background(), "acl_subject", "delete", clusterModels.ACLSubject{Type: subjectType, Name: subject})
}

func (s *Service) revokeUserGrants(user *models.User) error {
	if err := s.revokeSubject(clusterModels.ACLSubjectUser, PrincipalOf(user).Subject()); err != nil {
		return userInternalError("user_grants_revoke_failed", err)
	}
	return nil
}

// A local account this node hasn't shared doesn't own the grants of the shared user with its name.
func (s *Service) ownsNameGrants(user *models.User) (bool, error) {
	if user.Source != "local" || user.Shared {
		return true, nil
	}
	shared, err := s.nameShared(user.Username)
	return !shared, err
}

func (s *Service) revokeOwnGrants(user *models.User) error {
	owns, err := s.ownsNameGrants(user)
	if err != nil {
		return userInternalError("user_grants_revoke_failed", err)
	}
	if !owns {
		return nil
	}
	return s.revokeUserGrants(user)
}

func (s *Service) renameSubject(subjectType, from, to string) error {
	held, err := s.referenced(subjectType, from)
	if err != nil || !held {
		return err
	}
	return s.propose(context.Background(), "acl_subject", "rename", clusterModels.ACLSubjectRename{Type: subjectType, From: from, To: to})
}

type PathPrivileges struct {
	Privileges []string `json:"privileges"`
	Below      []string `json:"below"`
}

// Permissions lists, for "/" and every path p has a grant on, the privileges held on that path and
// on paths below it that have no grant of their own.
func (s *Service) Permissions(p Principal) (map[string]PathPrivileges, error) {
	if p.Superuser || !s.RBACActive() {
		return map[string]PathPrivileges{"/": {Privileges: Privileges, Below: Privileges}}, nil
	}
	grants, err := s.grantsFor(p)
	if err != nil {
		return nil, err
	}
	paths := map[string]PathPrivileges{"/": {}}
	for _, grant := range grants {
		paths[grant.Path] = PathPrivileges{}
	}
	sorted := func(roles []string) ([]string, error) {
		held, err := s.rolePrivileges(roles)
		return append([]string{}, slices.Sorted(maps.Keys(held))...), err
	}
	for path := range paths {
		here, err := sorted(rolesOn(grants, path))
		if err != nil {
			return nil, err
		}
		// No grant names "*", so this is any path below with none of its own.
		below, err := sorted(rolesOn(grants, strings.TrimSuffix(path, "/")+"/*"))
		if err != nil {
			return nil, err
		}
		paths[path] = PathPrivileges{Privileges: here, Below: below}
	}
	return paths, nil
}

func (s *Service) NodeIDs() (map[string]string, error) {
	var nodes []clusterModels.ClusterNode
	if err := s.DB.Select("hostname", "node_uuid").Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_nodes: %w", err)
	}
	ids := make(map[string]string, len(nodes)+1)
	for _, node := range nodes {
		ids[node.Hostname] = node.NodeUUID
	}
	hostname, err := utils.GetSystemHostname()
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_hostname: %w", err)
	}
	id, err := utils.GetSystemUUID()
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_node_id: %w", err)
	}
	ids[hostname] = id
	return ids, nil
}
