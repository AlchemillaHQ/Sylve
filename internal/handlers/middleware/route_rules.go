// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"net/http"
	"sort"
	"strings"

	authSvc "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/gin-gonic/gin"
)

type ruleKind int

const (
	rulePrivilege ruleKind = iota
	rulePublic
	ruleNodeOnly
	ruleAnyUser
	ruleInHandler
	ruleSuperuser
)

// A route needs privs on path; ":param" segments come from the route and {node} is this node.
// Handlers may require more than the route does.
type routeRule struct {
	kind  ruleKind
	path  string
	privs []string
}

func need(path string, privs ...string) routeRule {
	return routeRule{kind: rulePrivilege, path: path, privs: privs}
}

const (
	pathRoot       = "/"
	pathNode       = "/nodes/{node}"
	pathAccess     = "/access"
	pathVMs        = "/vm"
	pathVM         = "/vm/:rid"
	pathJails      = "/jail"
	pathJail       = "/jail/:ctid"
	pathContainers = "/container"
	pathContainer  = "/container/:ocid"
)

var (
	public    = routeRule{kind: rulePublic}
	nodeOnly  = routeRule{kind: ruleNodeOnly}
	anyUser   = routeRule{kind: ruleAnyUser}
	inHandler = routeRule{kind: ruleInHandler}
	superuser = routeRule{kind: ruleSuperuser}

	hostAdmin = need(pathNode, authSvc.SysModify)

	vm        = func(priv string) routeRule { return need(pathVM, priv) }
	jail      = func(priv string) routeRule { return need(pathJail, priv) }
	container = func(priv string) routeRule { return need(pathContainer, priv) }
)

var routeRules = map[string]routeRule{
	"POST /api/auth/login":                          public,
	"GET /api/auth/login/config":                    public,
	"POST /api/auth/passkeys/login/begin":           public,
	"POST /api/auth/oidc/:realm/start":              public,
	"POST /api/auth/oidc/callback":                  public,
	"POST /api/auth/passkeys/login/finish":          public,
	"GET /api/health/basic":                         public,
	"POST /api/cluster/accept-join":                 public,
	"POST /api/cluster/membership-status":           public,
	"POST /api/cluster/rebuild":                     public,
	"GET /api/events/stream":                        public,
	"GET /api/utilities/downloads/:uuid":            public,
	"HEAD /api/utilities/downloads/:uuid":           public,
	"GET /api/utilities/downloads/:uuid/:filename":  public,
	"HEAD /api/utilities/downloads/:uuid/:filename": public,

	"POST /api/intra-cluster/leader/:name": nodeOnly,
	"POST /api/intra-cluster/node/:name":   nodeOnly,
	"POST /api/intra-cluster/raft/apply":   nodeOnly,
	"POST /api/intra-cluster/stream/:name": nodeOnly,

	"POST /api/auth/logout":                          anyUser,
	"POST /api/auth/sse-tokens":                      anyUser,
	"GET /api/auth/preferences":                      anyUser,
	"GET /api/auth/permissions":                      anyUser,
	"GET /api/auth/roles":                            anyUser,
	"GET /api/health/http":                           anyUser,
	"GET /api/basic/settings":                        anyUser,
	"GET /api/system/basic-settings":                 anyUser,
	"GET /api/info/basic":                            anyUser,
	"GET /api/cluster":                               anyUser,
	"GET /api/container/catalog":                     anyUser,
	"GET /api/container/catalog/:source/icon":        anyUser,
	"GET /api/container/catalog/:source/:id":         anyUser,
	"GET /api/container/catalog/:source/:id/icon":    anyUser,
	"POST /api/container/catalog/:source/:id/render": anyUser,
	"GET /api/container/ports":                       anyUser,
	"GET /api/jail/bootstraps":                       anyUser,
	"GET /api/utilities/downloads":                   anyUser,
	"GET /api/utilities/downloads/paths":             anyUser,
	"GET /api/utilities/downloads/storage":           anyUser,
	"GET /api/utilities/downloads/utype":             anyUser,
	"GET /api/utilities/cloud-init/templates":        anyUser,

	"POST /api/auth/passkeys/register/begin": inHandler,
	// The challenge it finishes was checked at begin.
	"POST /api/auth/passkeys/register/finish":               anyUser,
	"GET /api/auth/users/:userId/passkeys":                  inHandler,
	"DELETE /api/auth/users/:userId/passkeys/:credentialId": inHandler,

	// Lists show what the user may audit; creates and updates check the IDs, pools, switches and
	// fields they touch (handlers/*/access.go), and settings that reach the host need host admin.
	"GET /api/cluster/nodes":                              anyUser,
	"GET /api/cluster/guest-ids/next":                     anyUser,
	"GET /api/cluster/resources":                          inHandler,
	"GET /api/network/switch":                             inHandler,
	"GET /api/zfs/pools":                                  inHandler,
	"GET /api/zfs/datasets":                               inHandler,
	"GET /api/zfs/datasets/paginated":                     need("/storage", authSvc.DatastoreAudit),
	"GET /api/tasks/lifecycle/active":                     inHandler,
	"GET /api/tasks/lifecycle/active/:guestType/:guestId": inHandler,
	"GET /api/tasks/lifecycle/recent":                     inHandler,
	"GET /api/tasks/lifecycle/:taskId":                    inHandler,
	"POST /api/tasks/migration/:taskId/cancel":            need(pathRoot, authSvc.SysModify),
	"GET /api/tasks/migration/validate":                   need(pathRoot, authSvc.SysAudit),
	"GET /api/vnc/:port":                                  inHandler,
	"GET /api/vm":                                         inHandler,
	"GET /api/vm/simple":                                  inHandler,
	"POST /api/vm":                                        inHandler,
	"POST /api/vm/templates/:templateId/vms":              inHandler,
	"POST /api/vm/:rid/storage":                           inHandler,
	"POST /api/vm/:rid/storage/from-image":                inHandler,
	"PATCH /api/vm/:rid/storage/:storageId":               inHandler,
	"POST /api/vm/:rid/networks":                          inHandler,
	"PATCH /api/vm/:rid/networks/:networkId":              inHandler,
	"GET /api/jail":                                       inHandler,
	"GET /api/jail/simple":                                inHandler,
	"POST /api/jail":                                      inHandler,
	"POST /api/jail/validate":                             inHandler,
	"POST /api/jail/templates/:templateId/jails":          inHandler,
	"PUT /api/jail/:ctid/network/inheritance":             hostAdmin,
	"POST /api/jail/:ctid/networks":                       inHandler,
	"PATCH /api/jail/:ctid/networks/:networkId":           inHandler,
	"GET /api/container":                                  inHandler,
	"GET /api/container/simple":                           inHandler,
	"POST /api/container":                                 inHandler,
	"POST /api/container/stack":                           inHandler,
	"PATCH /api/container/stack/:stack":                   inHandler,
	"PUT /api/container/:ocid":                            inHandler,
	"PATCH /api/container/:ocid/options":                  inHandler,

	"POST /api/basic/initialize":                        superuser,
	"POST /api/cluster":                                 superuser,
	"POST /api/cluster/join":                            superuser,
	"POST /api/cluster/join-key":                        superuser,
	"POST /api/cluster/leave/cancel":                    superuser,
	"POST /api/cluster/remove-node":                     superuser,
	"POST /api/cluster/remove-node/force":               superuser,
	"DELETE /api/cluster/reset-node":                    superuser,
	"DELETE /api/cluster/reset-node/force":              superuser,
	"GET /api/system/file-explorer":                     superuser,
	"POST /api/system/file-explorer":                    superuser,
	"POST /api/system/file-explorer/copy-or-move-batch": superuser,
	"POST /api/system/file-explorer/delete":             superuser,
	"GET /api/system/file-explorer/download":            superuser,
	"POST /api/system/file-explorer/rename":             superuser,
	"POST /api/system/file-explorer/upload":             superuser,
	"DELETE /api/system/file-explorer/upload":           superuser,

	"GET /api/auth/users":                      inHandler,
	"GET /api/auth/grants":                     need(pathAccess, authSvc.SysAudit),
	"POST /api/auth/grants":                    inHandler,
	"DELETE /api/auth/grants":                  inHandler,
	"PUT /api/auth/roles/:name":                need(pathRoot, authSvc.PermissionsModify), // reaches every grant naming it
	"DELETE /api/auth/roles/:name":             need(pathRoot, authSvc.PermissionsModify),
	"GET /api/auth/acl-groups":                 need(pathAccess, authSvc.SysAudit),
	"PUT /api/auth/acl-groups/:name":           need(pathRoot, authSvc.PermissionsModify),
	"DELETE /api/auth/acl-groups/:name":        need(pathRoot, authSvc.PermissionsModify),
	"GET /api/auth/realms":                     need(pathAccess, authSvc.SysAudit),
	"PUT /api/auth/realms/:name":               need(pathRoot, authSvc.PermissionsModify), // group mappings are membership
	"DELETE /api/auth/realms/:name":            need(pathRoot, authSvc.PermissionsModify),
	"POST /api/auth/realms/discover":           need(pathRoot, authSvc.PermissionsModify),
	"POST /api/auth/realms/:name/users":        need(pathAccess, authSvc.UserModify),
	"GET /api/basic/initialize/host-state":     need(pathNode, authSvc.SysAudit),
	"POST /api/basic/system/reboot":            need(pathNode, authSvc.SysPowerMgmt),
	"GET /api/info/audit-records":              inHandler,
	"GET /api/info/terminal":                   need(pathNode, authSvc.SysConsole),
	"GET /api/network/firewall/logs/live":      need(pathNode, authSvc.SysSyslog),
	"GET /api/cluster/join-status":             need(pathRoot, authSvc.SysAudit),
	"GET /api/cluster/leave-status":            need(pathRoot, authSvc.SysAudit),
	"POST /api/utilities/downloads/signed-url": need(pathRoot, authSvc.SysAudit),
	"GET /api/zfs/pools/:guid/status":          need("/storage/:guid", authSvc.DatastoreAudit),
	"GET /api/zfs/pools/:guid/scrub/runs":      need("/storage/:guid", authSvc.DatastoreAudit),
	"GET /api/jail/zfs-sources":                need(pathNode, authSvc.SysAudit),
	"POST /api/jail/bootstraps":                inHandler,
	"DELETE /api/jail/bootstraps/:name":        inHandler,
	"GET /api/container/networks":              need(pathNode, authSvc.SysAudit),
	"POST /api/container/networks":             need(pathNode, authSvc.SysModify),
	"DELETE /api/container/networks/:id":       need(pathNode, authSvc.SysModify),
	"POST /api/container/updates/check":        need(pathNode, authSvc.SysModify),
	"GET /api/container/images/:pool":          need("/storage/:pool", authSvc.DatastoreAudit),
	"POST /api/container/images/:pool/pull":    need("/storage/:pool", authSvc.DatastoreAllocateTemplate),
	"POST /api/container/images/:pool/prune":   need("/storage/:pool", authSvc.DatastoreAllocateTemplate),
	"DELETE /api/container/images/:pool/:id":   need("/storage/:pool", authSvc.DatastoreAllocateTemplate),

	// Reads that hold secrets either blank them for auditors (inHandler) or, when the secret is the
	// whole point, need the same rights as changing them.
	"GET /api/container/registry-credentials":        need(pathNode, authSvc.SysAudit),
	"PUT /api/container/registry-credentials":        need(pathNode, authSvc.SysModify),
	"DELETE /api/container/registry-credentials/:id": need(pathNode, authSvc.SysModify),
	"GET /api/iscsi/config":                          need(pathNode, authSvc.SysModify),
	"GET /api/certificates/:id/archive":              need(pathNode, authSvc.SysModify),
	"GET /api/network/wireguard/server":              inHandler,
	"GET /api/network/wireguard/clients":             inHandler,
	"GET /api/directory-services/recovery":           need(pathNode, authSvc.SysModify),
	"GET /api/notifications/transports":              inHandler,

	"GET /api/vm/simple/:rid":                          vm(authSvc.GuestAudit),
	"GET /api/vm/:rid":                                 vm(authSvc.GuestAudit),
	"GET /api/vm/:rid/domain":                          vm(authSvc.GuestAudit),
	"GET /api/vm/:rid/guest-agent":                     vm(authSvc.GuestAudit),
	"GET /api/vm/:rid/logs":                            vm(authSvc.GuestAudit),
	"GET /api/vm/:rid/stats":                           vm(authSvc.GuestAudit),
	"GET /api/vm/:rid/stats/:step":                     vm(authSvc.GuestAudit),
	"GET /api/vm/:rid/snapshots":                       vm(authSvc.GuestAudit),
	"POST /api/vm/:rid/actions/:action":                vm(authSvc.GuestPowerMgmt),
	"GET /api/vm/:rid/console":                         vm(authSvc.GuestConsole),
	"PUT /api/vm/:rid/hardware/cpu":                    vm(authSvc.GuestConfigCPU),
	"PUT /api/vm/:rid/hardware/ram":                    vm(authSvc.GuestConfigMemory),
	"PUT /api/vm/:rid/hardware/vnc":                    vm(authSvc.GuestConfigHWType),
	"PUT /api/vm/:rid/options/boot-rom":                vm(authSvc.GuestConfigHWType),
	"PUT /api/vm/:rid/options/ignore-umsrs":            vm(authSvc.GuestConfigHWType),
	"PUT /api/vm/:rid/options/tpm":                     vm(authSvc.GuestConfigHWType),
	"DELETE /api/vm/:rid/storage/:storageId":           vm(authSvc.GuestConfigDisk),
	"DELETE /api/vm/:rid/networks/:networkId":          vm(authSvc.GuestConfigNetwork),
	"PUT /api/vm/:rid/options/cloud-init":              vm(authSvc.GuestConfigCloudinit),
	"PATCH /api/vm/:rid/description":                   vm(authSvc.GuestConfigOptions),
	"PATCH /api/vm/:rid/name":                          vm(authSvc.GuestConfigOptions),
	"PUT /api/vm/:rid/options/boot-order":              vm(authSvc.GuestConfigOptions),
	"PUT /api/vm/:rid/options/clock":                   vm(authSvc.GuestConfigOptions),
	"PUT /api/vm/:rid/options/qemu-guest-agent":        vm(authSvc.GuestConfigOptions),
	"PUT /api/vm/:rid/options/serial-console":          vm(authSvc.GuestConfigOptions),
	"PUT /api/vm/:rid/options/shutdown-wait-time":      vm(authSvc.GuestConfigOptions),
	"PUT /api/vm/:rid/options/wol":                     vm(authSvc.GuestConfigOptions),
	"POST /api/vm/:rid/snapshots":                      vm(authSvc.GuestSnapshot),
	"DELETE /api/vm/:rid/snapshots/:snapshotId":        vm(authSvc.GuestSnapshot),
	"POST /api/vm/:rid/snapshots/:snapshotId/rollback": vm(authSvc.GuestSnapshotRollback),
	"POST /api/vm/:rid/migrations":                     vm(authSvc.GuestMigrate),
	"DELETE /api/vm/:rid":                              vm(authSvc.GuestAllocate),
	"DELETE /api/vm/:rid/registration":                 vm(authSvc.GuestAllocate),
	"POST /api/vm/:rid/domain/reinitialize":            vm(authSvc.GuestAllocate),
	"POST /api/vm/:rid/templates":                      vm(authSvc.GuestAllocate),
	"PUT /api/vm/:rid/hardware/pci-devices":            hostAdmin,
	"PUT /api/vm/:rid/options/extra-bhyve-options":     hostAdmin,
	"GET /api/vm/templates":                            need(pathVMs, authSvc.GuestAudit),
	"GET /api/vm/templates/:templateId":                need(pathVMs, authSvc.GuestAudit),
	"DELETE /api/vm/templates/:templateId":             need(pathVMs, authSvc.GuestAllocate),

	"GET /api/jail/simple/:ctid":                          jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid":                                 jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/guest-info":                      jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/logs":                            jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/root-mountpoint":                 jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/state":                           jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/stats":                           jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/stats/:step":                     jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/snapshots":                       jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/packages":                        jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/packages/audit":                  jail(authSvc.GuestAudit),
	"POST /api/jail/:ctid/packages/audit":                 jail(authSvc.GuestAudit),
	"POST /api/jail/:ctid/packages/check-updates":         jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/packages/installed/:name":        jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/packages/jobs/:taskId":           jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/packages/jobs/:taskId/logs":      jail(authSvc.GuestAudit),
	"GET /api/jail/:ctid/packages/monitoring":             jail(authSvc.GuestAudit),
	"POST /api/jail/:ctid/packages/upgrade/preview":       jail(authSvc.GuestAudit),
	"POST /api/jail/:ctid/actions/:action":                jail(authSvc.GuestPowerMgmt),
	"GET /api/jail/:ctid/console":                         jail(authSvc.GuestConsole),
	"PUT /api/jail/:ctid/hardware/cpu":                    jail(authSvc.GuestConfigCPU),
	"PUT /api/jail/:ctid/hardware/resource-limits":        jail(authSvc.GuestConfigCPU),
	"PUT /api/jail/:ctid/hardware/ram":                    jail(authSvc.GuestConfigMemory),
	"DELETE /api/jail/:ctid/networks/:networkId":          jail(authSvc.GuestConfigNetwork),
	"PATCH /api/jail/:ctid/description":                   jail(authSvc.GuestConfigOptions),
	"PATCH /api/jail/:ctid/name":                          jail(authSvc.GuestConfigOptions),
	"PUT /api/jail/:ctid/options/boot-order":              jail(authSvc.GuestConfigOptions),
	"PUT /api/jail/:ctid/options/execution-timeout":       jail(authSvc.GuestConfigOptions),
	"PUT /api/jail/:ctid/options/metadata":                jail(authSvc.GuestConfigOptions),
	"PUT /api/jail/:ctid/options/resolv-conf":             jail(authSvc.GuestConfigOptions),
	"PUT /api/jail/:ctid/options/wol":                     jail(authSvc.GuestConfigOptions),
	"PUT /api/jail/:ctid/packages/monitoring":             jail(authSvc.GuestConfigOptions),
	"POST /api/jail/:ctid/packages/upgrade":               jail(authSvc.GuestConfigOptions),
	"POST /api/jail/:ctid/packages/jobs/:taskId/cancel":   jail(authSvc.GuestConfigOptions),
	"POST /api/jail/:ctid/snapshots":                      jail(authSvc.GuestSnapshot),
	"DELETE /api/jail/:ctid/snapshots/:snapshotId":        jail(authSvc.GuestSnapshot),
	"POST /api/jail/:ctid/snapshots/:snapshotId/rollback": jail(authSvc.GuestSnapshotRollback),
	"POST /api/jail/:ctid/migrations":                     jail(authSvc.GuestMigrate),
	"DELETE /api/jail/:ctid":                              jail(authSvc.GuestAllocate),
	"POST /api/jail/:ctid/templates":                      jail(authSvc.GuestAllocate),
	"PUT /api/jail/:ctid/options/additional-options":      hostAdmin,
	"PUT /api/jail/:ctid/options/allowed-options":         hostAdmin,
	"PUT /api/jail/:ctid/options/devfs-rules":             hostAdmin,
	"PUT /api/jail/:ctid/options/fstab":                   hostAdmin,
	"PUT /api/jail/:ctid/options/lifecycle-hooks":         hostAdmin,
	"GET /api/jail/templates":                             need(pathJails, authSvc.GuestAudit),
	"GET /api/jail/templates/:templateId":                 need(pathJails, authSvc.GuestAudit),
	"DELETE /api/jail/templates/:templateId":              need(pathJails, authSvc.GuestAllocate),

	"GET /api/container/:ocid":                  container(authSvc.GuestAudit),
	"GET /api/container/:ocid/image":            container(authSvc.GuestAudit),
	"GET /api/container/:ocid/logs":             container(authSvc.GuestAudit),
	"GET /api/container/:ocid/stats":            container(authSvc.GuestAudit),
	"GET /api/container/:ocid/stats/:step":      container(authSvc.GuestAudit),
	"POST /api/container/:ocid/actions/:action": container(authSvc.GuestPowerMgmt),
	"GET /api/container/:ocid/console":          container(authSvc.GuestConsole),
	"DELETE /api/container/:ocid":               container(authSvc.GuestAllocate),
}

// Host configuration: reads need Sys.Audit and changes Sys.Modify. A route in neither this list
// nor routeRules is refused.
var routeGroups = []struct {
	prefix string
	path   string
}{
	{"/api/auth/groups", pathNode}, // Unix groups
	{"/api/certificates", pathNode},
	{"/api/directory-services", pathNode},
	{"/api/disk", pathNode},
	{"/api/dynamic-dns", pathNode},
	{"/api/info", pathNode},
	{"/api/iscsi", pathNode},
	{"/api/mdns", pathNode},
	{"/api/network", pathNode},
	{"/api/nfs", pathNode},
	{"/api/samba", pathNode},
	{"/api/system", pathNode},
	{"/api/zfs", pathNode},
	{"/api/cluster/backups", pathRoot},
	{"/api/cluster/notes", pathRoot},
	{"/api/cluster/replication", pathRoot},
	{"/api/notifications", pathRoot},
	{"/api/utilities", pathRoot},
}

var accessGroups = []string{"/api/auth/users"}

func routeRuleFor(method, path string) (routeRule, bool) {
	if rule, ok := routeRules[method+" "+path]; ok {
		return rule, true
	}
	// The node the request is forwarded to checks it.
	if path == "/api/nodes/:node/*path" {
		return anyUser, true
	}
	under := func(prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+"/") }
	for _, prefix := range accessGroups {
		if under(prefix) {
			return need(pathAccess, authSvc.UserModify), true
		}
	}
	for _, group := range routeGroups {
		if !under(group.prefix) {
			continue
		}
		if method == http.MethodGet || method == http.MethodHead {
			return need(group.path, authSvc.SysAudit), true
		}
		return need(group.path, authSvc.SysModify), true
	}
	return routeRule{}, false
}

func RouteRuleCoverage(routes gin.RoutesInfo) (missing, stale []string) {
	registered := make(map[string]bool, len(routes))
	for _, route := range routes {
		if !strings.HasPrefix(route.Path, "/api/") {
			continue
		}
		key := route.Method + " " + route.Path
		registered[key] = true
		if _, ok := routeRuleFor(route.Method, route.Path); !ok {
			missing = append(missing, key)
		}
	}
	for key := range routeRules {
		if !registered[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}
