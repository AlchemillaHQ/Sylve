// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

const (
	MachineKeytabName        = "AD_MACHINE_ACCOUNT"
	MachineKeytabPath        = "/var/db/samba4/private/sylve.keytab"
	wildcardLow       uint32 = 900000
	wildcardHigh      uint32 = 999999
)

type ADInput struct {
	Domain              string                  `json:"domain"`
	Workgroup           string                  `json:"workgroup"`
	ComputerName        string                  `json:"computerName"`
	Site                string                  `json:"site"`
	ComputerOU          string                  `json:"computerOU"`
	DomainController    string                  `json:"domainController"`
	RealmID             uint                    `json:"realmId"`
	KeytabID            uint                    `json:"keytabId"`
	KerberosPrincipal   string                  `json:"kerberosPrincipal"`
	JoinUsername        *string                 `json:"joinUsername,omitempty"`
	JoinPassword        *string                 `json:"joinPassword,omitempty"`
	ClearJoinPassword   bool                    `json:"clearJoinPassword,omitempty"`
	AllowTrustedDomains bool                    `json:"allowTrustedDomains"`
	UseDefaultDomain    bool                    `json:"useDefaultDomain"`
	DisableCache        bool                    `json:"disableCache"`
	SkipDNSRegistration bool                    `json:"skipDNSRegistration"`
	IDMapBackend        string                  `json:"idmapBackend"`
	RangeLow            uint32                  `json:"rangeLow"`
	RangeHigh           uint32                  `json:"rangeHigh"`
	IDMapSchema         string                  `json:"idmapSchema"`
	TrustedDomains      []directoryModels.IDMap `json:"trustedDomains"`
}

type JoinInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LeaveInput struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	LocalOnly bool   `json:"localOnly"`
}

type ADStatus struct {
	controllerHealth
	State           string    `json:"state"`
	Joined          bool      `json:"joined"`
	Enabled         bool      `json:"enabled"`
	Domain          string    `json:"domain"`
	Error           string    `json:"error"`
	CheckedAt       time.Time `json:"checkedAt"`
	RecoveryPending bool      `json:"recoveryPending"`
}

func getAD(db *gorm.DB) (directoryModels.ActiveDirectory, error) {
	host, _ := os.Hostname()
	host = strings.ToUpper(strings.Split(host, ".")[0])
	if len(host) > 15 {
		host = host[:15]
	}
	row := directoryModels.ActiveDirectory{ID: 1, ComputerName: host, IDMapBackend: "rid", RangeLow: 1000000, RangeHigh: 1999999999, IDMapSchema: "rfc2307", TrustedDomains: []directoryModels.IDMap{}}
	err := db.First(&row, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	if row.TrustedDomains == nil {
		row.TrustedDomains = []directoryModels.IDMap{}
	}
	row.JoinPasswordConfigured = len(row.JoinPasswordCiphertext) > 0
	return row, err
}

func (s *Service) GetAD() (directoryModels.ActiveDirectory, error) { return getAD(s.DB) }

var netbiosName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,14}$`)

func normalizeAD(input ADInput) directoryModels.ActiveDirectory {
	trusted := slices.Clone(input.TrustedDomains)
	if trusted == nil {
		trusted = []directoryModels.IDMap{}
	}
	for i := range trusted {
		trusted[i].Domain = strings.ToUpper(strings.TrimSpace(trusted[i].Domain))
		trusted[i].Backend = strings.ToLower(trusted[i].Backend)
	}
	slices.SortFunc(trusted, func(a, b directoryModels.IDMap) int { return strings.Compare(a.Domain, b.Domain) })
	return directoryModels.ActiveDirectory{
		ID: 1, Domain: strings.ToUpper(strings.TrimSpace(input.Domain)), Workgroup: strings.ToUpper(strings.TrimSpace(input.Workgroup)), ComputerName: strings.ToUpper(strings.TrimSpace(input.ComputerName)),
		Site: strings.TrimSpace(input.Site), ComputerOU: strings.TrimSpace(input.ComputerOU), DomainController: strings.TrimSpace(input.DomainController), RealmID: input.RealmID,
		KeytabID: input.KeytabID, KerberosPrincipal: strings.TrimSpace(input.KerberosPrincipal), AllowTrustedDomains: input.AllowTrustedDomains, UseDefaultDomain: input.UseDefaultDomain,
		DisableCache: input.DisableCache, IDMapBackend: strings.ToLower(input.IDMapBackend), RangeLow: input.RangeLow, RangeHigh: input.RangeHigh, IDMapSchema: input.IDMapSchema, TrustedDomains: trusted,
		SkipDNSRegistration: input.SkipDNSRegistration,
	}
}

func (s *Service) validateAD(row directoryModels.ActiveDirectory) error {
	if !hostname.MatchString(row.Domain) || !strings.Contains(row.Domain, ".") || !netbiosName.MatchString(row.Workgroup) || !netbiosName.MatchString(row.ComputerName) {
		return invalid("a DNS domain, NetBIOS workgroup, and computer name (maximum 15 characters) are required")
	}
	for _, value := range []string{row.Site, row.ComputerOU, row.DomainController, row.KerberosPrincipal} {
		if len(value) > 512 || strings.ContainsAny(value, "\x00\r\n\"[]") {
			return invalid("invalid AD option characters")
		}
	}
	if row.DomainController != "" && !hostname.MatchString(row.DomainController) {
		return invalid("invalid domain controller hostname")
	}
	if row.RealmID != 0 {
		var realm directoryModels.KerberosRealm
		if err := s.DB.First(&realm, row.RealmID).Error; err != nil {
			return invalid("selected Kerberos realm does not exist")
		}
		if realm.Realm != row.Domain {
			return invalid("AD realm must match the DNS domain")
		}
	}
	if (row.KeytabID == 0) != (row.KerberosPrincipal == "") {
		return invalid("select both a keytab and its principal")
	}
	if row.KeytabID != 0 {
		var keytab directoryModels.KerberosKeytab
		if err := s.DB.First(&keytab, row.KeytabID).Error; err != nil {
			return invalid("selected keytab does not exist")
		}
		if !slices.Contains(keytab.Principals, row.KerberosPrincipal) {
			return invalid("principal is not present in the selected keytab")
		}
		if keytab.Managed {
			return invalid("use an uploaded keytab for enrollment, not the managed machine keytab")
		}
	}
	if row.Site != "" && (!realmName.MatchString(row.Site) || strings.Contains(row.Site, ".")) {
		return invalid("AD site must be a DNS site label")
	}
	if len(row.TrustedDomains) > 32 || (!row.AllowTrustedDomains && len(row.TrustedDomains) > 0) || (row.IDMapBackend == "autorid" && len(row.TrustedDomains) > 0) {
		return invalid("trusted mappings require trusted domains and a non-AUTORID primary backend")
	}
	mappings := append([]directoryModels.IDMap{{Domain: row.Workgroup, Backend: row.IDMapBackend, RangeLow: row.RangeLow, RangeHigh: row.RangeHigh, Schema: row.IDMapSchema}}, row.TrustedDomains...)
	seen := map[string]bool{}
	for i, mapping := range mappings {
		if !netbiosName.MatchString(mapping.Domain) || seen[mapping.Domain] || mapping.RangeLow < 1000000 || mapping.RangeHigh <= mapping.RangeLow || mapping.RangeHigh > 2147483647 {
			return invalid("ID map domains must be unique and ranges must be within 1000000–2147483647")
		}
		seen[mapping.Domain] = true
		if mapping.Backend != "rid" && mapping.Backend != "ad" && !(i == 0 && mapping.Backend == "autorid") {
			return invalid("unsupported ID map backend")
		}
		if mapping.Backend == "ad" && mapping.Schema != "rfc2307" && mapping.Schema != "sfu" && mapping.Schema != "sfu20" {
			return invalid("unsupported AD mapping schema")
		}
		if mapping.Backend == "autorid" && uint64(mapping.RangeHigh)-uint64(mapping.RangeLow)+1 < 200000 {
			return invalid("AUTORID requires at least two 100000-ID domain ranges")
		}
		for _, previous := range mappings[:i] {
			if mapping.RangeLow <= previous.RangeHigh && previous.RangeLow <= mapping.RangeHigh {
				return invalid("ID map ranges cannot overlap")
			}
		}
	}
	return nil
}

func sameMappingIdentity(a, b directoryModels.ActiveDirectory) bool {
	return a.Domain == b.Domain && a.Workgroup == b.Workgroup &&
		a.IDMapBackend == b.IDMapBackend && a.IDMapSchema == b.IDMapSchema &&
		a.RangeLow == b.RangeLow && a.RangeHigh == b.RangeHigh &&
		slices.Equal(a.TrustedDomains, b.TrustedDomains)
}

func onlyAddsTrustedMappings(old, next directoryModels.ActiveDirectory) bool {
	if len(next.TrustedDomains) <= len(old.TrustedDomains) {
		return false
	}
	for _, mapping := range old.TrustedDomains {
		if !slices.Contains(next.TrustedDomains, mapping) {
			return false
		}
	}
	old.TrustedDomains, next.TrustedDomains = nil, nil
	return sameMappingIdentity(old, next)
}

func (s *Service) SaveAD(ctx context.Context, input ADInput) (directoryModels.ActiveDirectory, error) {
	row := normalizeAD(input)
	unlock, err := s.LockConfiguration()
	if err != nil {
		return row, err
	}
	defer unlock()
	if row.Workgroup == "" {
		discovery, err := s.DiscoverAD(ctx, discoveryInput(row))
		if err != nil {
			return row, err
		}
		row.Workgroup = discovery.Workgroup
	}
	if err := s.validateAD(row); err != nil {
		return row, err
	}
	old, err := s.GetAD()
	if err != nil {
		return row, err
	}
	if old.MembershipPending != "" {
		return row, fmt.Errorf("%w: recover or finish the pending membership operation first", ErrConflict)
	}
	inUse := false
	if s.SharePrincipalsInUse != nil {
		inUse, err = s.SharePrincipalsInUse()
		if err != nil {
			return row, err
		}
	}
	if (old.Joined || inUse) && !sameMappingIdentity(old, row) && !onlyAddsTrustedMappings(old, row) {
		return row, fmt.Errorf("%w: domain and existing ID mappings are locked while joined or referenced by share permissions", ErrConflict)
	}
	if old.Joined && (old.ComputerName != row.ComputerName || old.RealmID != row.RealmID) {
		return row, fmt.Errorf("%w: leave the domain before changing the computer name or realm", ErrConflict)
	}
	if err := s.saveJoinCredentials(&row, old, input); err != nil {
		return row, err
	}
	row.Joined, row.Enabled = old.Joined, old.Enabled
	row.JoinWarning = old.JoinWarning
	if row.Enabled && s.ValidateSamba != nil {
		if err := s.ValidateSamba(ctx, row); err != nil {
			return row, err
		}
	}
	if err := s.DB.Save(&row).Error; err != nil {
		return row, err
	}
	s.principalCache.clear()
	defer s.principalCache.clear()
	if row.Joined {
		if err := s.applyMember(ctx, row); err != nil {
			rollback := s.DB.Save(&old).Error
			return row, errors.Join(err, rollback, s.applyMember(context.WithoutCancel(ctx), old))
		}
	}
	s.setState("", nil)
	return row, nil
}

// Uses Samba/Winbind rather than implementing directory/authentication protocols.
func SambaConfig(ad directoryModels.ActiveDirectory, version string, hostnames ...string) (string, error) {
	if !ad.Enabled || ad.Domain == "" {
		return "", nil
	}
	syncKeytab, err := supportsMachineKeytabSync(version)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n# Active Directory member configuration\nsecurity = ADS\nserver role = member server\nrealm = %s\nworkgroup = %s\nnetbios name = %s\n", ad.Domain, ad.Workgroup, ad.ComputerName)
	if syncKeytab {
		fmt.Fprintf(&b, "kerberos method = secrets and keytab\ndedicated keytab file = %s\nsync machine password to keytab = %s\n", MachineKeytabPath, machineKeytabSync(MachineKeytabPath, ad, hostnames...))
	} else {
		// Legacy secrets-and-keytab mode writes the system keytab on join and
		// password changes. Sylve owns that merged file; refresh the separate
		// machine keytab instead, without disabling Samba's password rotation.
		b.WriteString("kerberos method = secrets only\n")
	}
	b.WriteString("local master = no\ndomain master = no\npreferred master = no\nwinbind refresh tickets = yes\nwinbind enum users = no\nwinbind enum groups = no\nclient ldap sasl wrapping = seal\n")
	// Domain-specific ID mappings need Winbind's trusted-domain discovery list.
	fmt.Fprintf(&b, "allow trusted domains = %s\nwinbind scan trusted domains = %s\nwinbind use default domain = %s\n", yesNo(ad.AllowTrustedDomains), yesNo(ad.AllowTrustedDomains), yesNo(ad.UseDefaultDomain))
	if ad.DomainController != "" {
		fmt.Fprintf(&b, "password server = %s *\n", ad.DomainController)
	}
	if ad.DisableCache {
		b.WriteString("winbind cache time = 0\n")
	} else {
		b.WriteString("winbind cache time = 300\n")
	}
	if ad.IDMapBackend == "autorid" {
		fmt.Fprintf(&b, "idmap config * : backend = autorid\nidmap config * : range = %d-%d\nidmap config * : rangesize = 100000\n", ad.RangeLow, ad.RangeHigh)
	} else {
		fmt.Fprintf(&b, "idmap config * : backend = tdb\nidmap config * : range = %d-%d\n", wildcardLow, wildcardHigh)
		mappings := append([]directoryModels.IDMap{{Domain: ad.Workgroup, Backend: ad.IDMapBackend, RangeLow: ad.RangeLow, RangeHigh: ad.RangeHigh, Schema: ad.IDMapSchema}}, ad.TrustedDomains...)
		for _, mapping := range mappings {
			fmt.Fprintf(&b, "idmap config %s : backend = %s\nidmap config %s : range = %d-%d\n", mapping.Domain, mapping.Backend, mapping.Domain, mapping.RangeLow, mapping.RangeHigh)
			if mapping.Backend == "ad" {
				fmt.Fprintf(&b, "idmap config %s : schema_mode = %s\nidmap config %s : unix_nss_info = yes\n", mapping.Domain, mapping.Schema, mapping.Domain)
			}
		}
	}
	return b.String(), nil
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func machineKeytabPrincipals(ad directoryModels.ActiveDirectory, hostnames ...string) []string {
	hosts := append([]string{ad.ComputerName, strings.ToLower(ad.ComputerName + "." + ad.Domain)}, hostnames...)
	principals := []string{ad.ComputerName + "$@" + ad.Domain}
	for _, service := range []string{"host", "cifs"} {
		for _, host := range hosts {
			principals = append(principals, service+"/"+host+"@"+ad.Domain)
		}
	}
	slices.Sort(principals)
	return slices.Compact(principals)
}

func machineKeytabSync(path string, ad directoryModels.ActiveDirectory, hostnames ...string) string {
	// testparm in Samba 4.21–4.23 accepts only one SPN specifier per entry.
	// Separate rules for one file remove each other's keys. Use one explicit
	// list, quoting the cmdlist entry so its commas stay inside that rule.
	return strconv.Quote(path + ":spns=" + strings.Join(machineKeytabPrincipals(ad, hostnames...), ",") + ":sync_kvno:machine_password")
}

func (s *Service) netArgs(ad directoryModels.ActiveDirectory, syncKeytab bool, hostnames []string, args ...string) []string {
	// Membership tools must still work when AD is disabled and smb.conf is
	// back in standalone mode. These options affect this invocation only.
	method := "secrets only"
	if syncKeytab {
		method = "secrets and keytab"
	}
	out := []string{"--configfile=" + s.smbPath, "--realm=" + ad.Domain, "--workgroup=" + ad.Workgroup,
		"--option=security=ads", "--option=server role=member server", "--option=netbios name=" + ad.ComputerName,
		"--option=kerberos method=" + method}
	if syncKeytab {
		out = append(out, "--option=dedicated keytab file="+s.machineKeytabPath,
			"--option=sync machine password to keytab="+machineKeytabSync(s.machineKeytabPath, ad, hostnames...))
	}
	if ad.DomainController != "" {
		out = append(out, "--server="+ad.DomainController)
	}
	return append(out, args...)
}

func (s *Service) sambaEnabled() (bool, error) {
	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil {
		return false, err
	}
	return slices.Contains(settings.Services, models.SambaServer), nil
}

func (s *Service) requireSamba() error {
	enabled, err := s.sambaEnabled()
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("%w: enable the Samba service in System Settings before enabling or joining AD", ErrConflict)
	}
	return nil
}

func (s *Service) Preflight(ctx context.Context, ad directoryModels.ActiveDirectory) error {
	if err := s.validateAD(ad); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := s.machineKeytabSyncSupported(ctx); err != nil {
		return err
	}
	if _, _, err := s.controllers(ctx, discoveryInput(ad)); err != nil {
		return err
	}
	_, kdc, err := s.lookupSRV(ctx, "kerberos", "tcp", strings.ToLower(ad.Domain))
	if err != nil || len(kdc) == 0 {
		return fmt.Errorf("%w: Kerberos DNS SRV discovery failed", ErrUnavailable)
	}
	return nil
}

func (s *Service) checkClock(ctx context.Context, ad directoryModels.ActiveDirectory) error {
	out, err := s.netCommand(ctx, ad, nil, "ads", "info")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Server time offset") {
			fields := strings.Fields(value)
			if len(fields) == 0 {
				continue
			}
			offset, err := strconv.ParseInt(fields[0], 10, 64)
			if err == nil && (offset <= -300 || offset >= 300) {
				return invalid("domain controller clock differs by %d seconds; fix node time synchronization before joining", offset)
			}
			if err == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: unable to verify domain controller clock offset", ErrUnavailable)
}

func (s *Service) ticket(ctx context.Context, ad directoryModels.ActiveDirectory, input JoinInput) (string, func(), error) {
	if input.Username == "" && input.Password == "" && ad.KeytabID == 0 && len(ad.JoinPasswordCiphertext) > 0 {
		password, err := s.decryptSecret(ad.JoinPasswordCiphertext, joinPasswordPurpose(ad))
		if err != nil {
			return "", nil, fmt.Errorf("%w: cannot decrypt saved join password: %w", ErrUnavailable, err)
		}
		defer clear(password)
		input = JoinInput{Username: ad.JoinUsername, Password: string(password)}
	}
	dir, err := s.runtimeDir()
	if err != nil {
		return "", nil, err
	}
	dir, err = os.MkdirTemp(dir, "ticket-")
	if err != nil {
		return "", nil, err
	}
	cache := filepath.Join(dir, "ccache")
	cleanup := func() {
		_ = os.Remove(filepath.Join(dir, "join.keytab"))
		_ = os.Remove(cache)
		_ = os.Remove(dir)
	}
	args := []string{"-c", "FILE:" + cache}
	secret := ""
	if input.Username == "" && input.Password == "" && ad.KeytabID != 0 {
		var keytab directoryModels.KerberosKeytab
		if err := s.DB.First(&keytab, ad.KeytabID).Error; err != nil {
			cleanup()
			return "", nil, err
		}
		data, err := s.decryptKeytab(keytab.Ciphertext)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		path := filepath.Join(dir, "join.keytab")
		if err := utils.AtomicWriteFile(path, data, 0600); err != nil {
			cleanup()
			return "", nil, err
		}
		defer os.Remove(path)
		args = append(args, "-k", "-t", path, ad.KerberosPrincipal)
	} else {
		username := strings.TrimSpace(input.Username)
		if err := validateJoinCredentials(username, input.Password); err != nil {
			cleanup()
			return "", nil, err
		}
		if !strings.Contains(username, "@") {
			username += "@" + ad.Domain
		}
		args = append(args, username)
		secret = input.Password + "\n"
	}
	_, err = s.command(ctx, "/usr/bin/kinit", args, secret, []string{"KRB5CCNAME=FILE:" + cache})
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return cache, cleanup, nil
}

const joinDNSRegistrationWarning = "The domain was joined successfully, but AD DNS registration failed. Check the member's DNS record and registration permissions."

func recoveredJoinWarning(adWarning string, skipDNS bool) string {
	if adWarning == "" && !skipDNS {
		return "An interrupted domain join was recovered. Verify the member's AD DNS registration."
	}
	return adWarning
}

func joinDNSWarning(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "dns update failed") || strings.Contains(output, "failed to update dns") ||
		strings.Contains(output, "dns registration failed") || strings.Contains(output, "werr_dns_")
}

func (s *Service) Join(ctx context.Context, input JoinInput) (err error) {
	unlock, err := s.LockConfiguration()
	if err != nil {
		return err
	}
	defer unlock()
	old, err := s.GetAD()
	if err != nil {
		return err
	}
	if old.Joined && old.MembershipPending != "join" {
		return fmt.Errorf("%w: this node is already joined", ErrConflict)
	}
	if err := s.requireSamba(); err != nil {
		return err
	}
	if old.MembershipPending != "" && old.MembershipPending != "join" {
		return fmt.Errorf("%w: finish leaving the domain first", ErrConflict)
	}
	if old.MembershipPending == "join" {
		// A confirmed join whose local setup failed must not reset the remote
		// machine account again just to retry the local configuration.
		trustEstablished := old.Joined
		if !trustEstablished {
			_, trustErr := s.netCommand(ctx, old, nil, "ads", "testjoin")
			trustEstablished = trustErr == nil
		}
		if trustEstablished {
			old.Joined = true
			old.JoinWarning = recoveredJoinWarning(old.JoinWarning, old.SkipDNSRegistration)
			if err := s.DB.Save(&old).Error; err != nil {
				return err
			}
			err = s.finishJoin(ctx, old)
			if err != nil {
				s.setState("FAULTED", err)
			} else {
				s.setState("", nil)
			}
			return err
		}
	}
	if err := s.Preflight(ctx, old); err != nil {
		return err
	}
	discovery, err := s.DiscoverAD(ctx, discoveryInput(old))
	if err != nil {
		return err
	}
	if err := validateDiscoveredWorkgroup(old, discovery); err != nil {
		return err
	}
	if s.WriteSamba == nil || s.ValidateSamba == nil {
		return ErrUnavailable
	}
	ad := old
	ad.Enabled = true
	if err := s.ValidateSamba(ctx, ad); err != nil {
		return err
	}
	s.setState("JOINING", nil)
	joined := false
	attempted := false
	createdRealm := uint(0)
	defer func() {
		if err != nil {
			s.setState("FAULTED", err)
		} else {
			s.setState("HEALTHY", nil)
		}
	}()
	files := []fileSnapshot{}
	for _, path := range []string{s.smbPath, s.krbPath, s.keytabPath, s.nssPath} {
		f, e := snapshot(path)
		if e != nil {
			err = e
			return err
		}
		files = append(files, f)
	}
	defer func() {
		if err != nil && !joined && !attempted {
			rollbackErr := s.DB.Transaction(func(tx *gorm.DB) error {
				if err := tx.Save(&old).Error; err != nil {
					return err
				}
				if createdRealm != 0 {
					return tx.Delete(&directoryModels.KerberosRealm{}, createdRealm).Error
				}
				return nil
			})
			err = errors.Join(err, rollbackErr, restoreFiles(files))
		}
	}()
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if ad.RealmID == 0 {
			var realm directoryModels.KerberosRealm
			e := tx.Where("realm = ?", ad.Domain).First(&realm).Error
			if errors.Is(e, gorm.ErrRecordNotFound) {
				realm = directoryModels.KerberosRealm{Realm: ad.Domain, KDC: []string{}, AdminServer: []string{}, KpasswdServer: []string{}}
				e = tx.Create(&realm).Error
				if e == nil {
					createdRealm = realm.ID
				}
			}
			if e != nil {
				return e
			}
			ad.RealmID = realm.ID
		}
		ad.MembershipPending = "join"
		return tx.Save(&ad).Error
	})
	if err != nil {
		return err
	}
	s.principalCache.clear()
	defer s.principalCache.clear()
	if err = s.writeKerberos(s.DB); err != nil {
		return err
	}
	if err = s.WriteSamba(ctx, false); err != nil {
		return err
	}
	commandAD := ad
	commandAD.DomainController = discovery.DomainController
	if err = s.checkClock(ctx, commandAD); err != nil {
		return err
	}
	cache, cleanup, err := s.ticket(ctx, ad, input)
	if err != nil {
		return err
	}
	defer cleanup()
	args := []string{"--use-kerberos=required", "--use-krb5-ccache=FILE:" + cache, "ads", "join"}
	if ad.SkipDNSRegistration {
		args = append(args, "--no-dns-updates")
	}
	if ad.ComputerOU != "" {
		args = append(args, "createcomputer="+ad.ComputerOU)
	}
	attempted = true
	joinOutput, err := s.netCommand(ctx, commandAD, []string{"KRB5CCNAME=FILE:" + cache}, args...)
	ad.JoinWarning = ""
	if !ad.SkipDNSRegistration && joinDNSWarning(joinOutput) {
		ad.JoinWarning = joinDNSRegistrationWarning
	}
	if err != nil {
		// A join can succeed remotely but fail its DNS update. Never roll back
		// the member configuration if Samba confirms the trust was established.
		_, trustErr := s.netCommand(context.WithoutCancel(ctx), ad, nil, "ads", "testjoin")
		if trustErr != nil {
			return fmt.Errorf("%w: enrollment may have partially completed; check the connection or retry joining before changing configuration", err)
		}
		if !ad.SkipDNSRegistration && joinDNSWarning(err.Error()) {
			ad.JoinWarning = joinDNSRegistrationWarning
		} else {
			ad.JoinWarning = "The domain join was confirmed, but Samba reported a post-join error. Check the member's domain configuration and DNS records."
		}
		err = nil
	}
	joined = true
	ad.Joined = true
	if err = s.DB.Save(&ad).Error; err != nil {
		return err
	}
	return s.finishJoin(context.WithoutCancel(ctx), ad)
}

func (s *Service) finishJoin(ctx context.Context, ad directoryModels.ActiveDirectory) error {
	defer s.principalCache.clear()
	if err := s.requireSamba(); err != nil {
		return err
	}
	if err := s.applyMember(ctx, ad); err != nil {
		return err
	}
	if err := s.syncMachineKeytab(ctx); err != nil {
		return err
	}
	if err := s.writeKerberos(s.DB); err != nil {
		return err
	}
	ad.MembershipPending = ""
	return s.DB.Save(&ad).Error
}

func (s *Service) applyMember(ctx context.Context, ad directoryModels.ActiveDirectory) error {
	if err := s.writeKerberos(s.DB); err != nil {
		return err
	}
	if err := s.writeNSS(ad.Enabled); err != nil {
		return err
	}
	if s.WriteSamba == nil {
		return ErrUnavailable
	}
	enabled, err := s.sambaEnabled()
	if err != nil {
		return err
	}
	return s.WriteSamba(ctx, enabled)
}

func (s *Service) SetEnabled(ctx context.Context, enabled bool) error {
	unlock, err := s.LockConfiguration()
	if err != nil {
		return err
	}
	defer unlock()
	ad, err := s.GetAD()
	if err != nil {
		return err
	}
	if !ad.Joined {
		return fmt.Errorf("%w: join the domain first", ErrConflict)
	}
	if ad.MembershipPending != "" {
		return fmt.Errorf("%w: finish the membership operation first", ErrConflict)
	}
	old := ad
	ad.Enabled = enabled
	if enabled {
		if err := s.requireSamba(); err != nil {
			return err
		}
	}
	if ad.Enabled && s.ValidateSamba != nil {
		if err := s.ValidateSamba(ctx, ad); err != nil {
			return err
		}
	}
	if err := s.DB.Save(&ad).Error; err != nil {
		return err
	}
	s.principalCache.clear()
	defer s.principalCache.clear()
	if err := s.applyMember(ctx, ad); err != nil {
		return errors.Join(err, s.DB.Save(&old).Error, s.applyMember(context.WithoutCancel(ctx), old))
	}
	s.setState("", nil)
	return nil
}

func (s *Service) Leave(ctx context.Context, input LeaveInput) (err error) {
	unlock, err := s.LockConfiguration()
	if err != nil {
		return err
	}
	defer unlock()
	ad, err := s.GetAD()
	if err != nil {
		return err
	}
	if !ad.Joined && ad.MembershipPending == "" {
		return fmt.Errorf("%w: this node is not joined", ErrConflict)
	}
	s.principalCache.clear()
	defer s.principalCache.clear()
	// An unconfirmed join can still have created a remote computer account.
	// Track completed remote removal explicitly rather than inferring it from
	// Joined=false, which also describes an unresolved enrollment.
	remoteLeave := ad.MembershipPending != "leave-cleanup"
	s.setState("LEAVING", nil)
	defer func() {
		if err != nil {
			s.setState("FAULTED", err)
		} else {
			s.setState("DISABLED", nil)
		}
	}()
	if remoteLeave {
		ad.MembershipPending = "leave"
		if err := s.DB.Save(&ad).Error; err != nil {
			return err
		}
	}
	args := []string{"--machine-pass", "ads", "leave"}
	var env []string
	if !input.LocalOnly && remoteLeave && (input.Username != "" || input.Password != "" || ad.KeytabID != 0) {
		cache, cleanup, err := s.ticket(ctx, ad, JoinInput{Username: input.Username, Password: input.Password})
		if err != nil {
			return err
		}
		defer cleanup()
		args = []string{"--use-kerberos=required", "--use-krb5-ccache=FILE:" + cache, "ads", "leave"}
		env = append(env, "KRB5CCNAME=FILE:"+cache)
	}
	// A completed remote leave with failed local cleanup can be retried
	// without trying to delete the computer account a second time.
	if !input.LocalOnly && remoteLeave {
		if _, err := s.netCommand(ctx, ad, env, args...); err != nil {
			return err
		}
	}
	if remoteLeave {
		ad.Joined, ad.Enabled, ad.MembershipPending = false, false, "leave-cleanup"
		ad.JoinWarning = ""
		// Persist the irreversible remote outcome before any fallible local
		// cleanup; retrying a cleanup must not repeat remote deletion.
		if err := s.DB.Save(&ad).Error; err != nil {
			return err
		}
	}
	err = s.DB.Where("name = ? AND managed = ?", MachineKeytabName, true).Delete(&directoryModels.KerberosKeytab{}).Error
	if err == nil {
		err = os.Remove(s.machineKeytabPath)
		if os.IsNotExist(err) {
			err = nil
		}
	}
	if err == nil {
		err = s.applyMember(context.WithoutCancel(ctx), ad)
	}
	if err == nil {
		ad.MembershipPending = ""
		err = s.DB.Save(&ad).Error
	}
	return err
}

func (s *Service) syncMachineKeytab(ctx context.Context) error {
	ad, err := s.GetAD()
	if err != nil || !ad.Joined {
		return err
	}
	var row directoryModels.KerberosKeytab
	err = s.DB.Where("name = ? AND managed = ?", MachineKeytabName, true).First(&row).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	data, err := os.ReadFile(s.machineKeytabPath)
	if os.IsNotExist(err) && row.ID != 0 {
		// Restore saved machine credentials before contacting the DC. Reboot
		// recovery must also work during a directory outage.
		data, err = s.decryptKeytab(row.Ciphertext)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(s.machineKeytabPath), 0700); err != nil {
			return err
		}
		if err := utils.AtomicWriteFile(s.machineKeytabPath, data, 0600); err != nil {
			return err
		}
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read AD machine keytab: %w", err)
	}
	if ad.Enabled {
		syncKeytab, versionErr := s.machineKeytabSyncSupported(ctx)
		if versionErr != nil {
			return versionErr
		}
		if !syncKeytab {
			// The legacy command needs the DC even when a keytab exists. Run on
			// every monitor pass so a rotated trust password refreshes its keys.
			data, err = s.refreshMachineKeytab(ctx, ad, data, false)
		} else if os.IsNotExist(err) || validateNativeMachineKeytab(data, ad) != nil {
			// Older generated rules omitted the machine principal. Repair those
			// existing files too, without publishing a failed/partial refresh.
			data, err = s.refreshMachineKeytab(ctx, ad, data, true)
		}
	}
	if err != nil {
		return fmt.Errorf("read AD machine keytab: %w", err)
	}
	entries, err := parseKeytab(data)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("AD machine keytab is empty")
	}
	if err := os.Chmod(s.machineKeytabPath, 0600); err != nil {
		return err
	}
	if row.ID != 0 {
		old, err := s.decryptKeytab(row.Ciphertext)
		if err != nil {
			return err
		}
		if slices.Equal(old, data) {
			return nil
		}
	}
	row.Name, row.Managed, row.Principals = MachineKeytabName, true, keytabPrincipals(entries)
	row.Ciphertext, err = s.encryptKeytab(data)
	if err != nil {
		return err
	}
	return s.DB.Save(&row).Error
}

func nssConfig(text string, enabled bool, original string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		key, rest, ok := strings.Cut(line, ":")
		if !ok || (strings.TrimSpace(key) != "passwd" && strings.TrimSpace(key) != "group") {
			continue
		}
		body, comment, hasComment := strings.Cut(rest, "#")
		fields := strings.Fields(body)
		var originalFields []string
		for _, old := range strings.Split(original, "\n") {
			oldKey, value, _ := strings.Cut(old, ":")
			value, _, _ = strings.Cut(value, "#")
			if strings.TrimSpace(oldKey) == strings.TrimSpace(key) {
				originalFields = strings.Fields(value)
				break
			}
		}
		if enabled {
			// FreeBSD requires compat to be the only source. Use files for local
			// accounts while Winbind is active, then restore compat on disable.
			for i, field := range fields {
				if field == "compat" {
					fields[i] = "files"
				}
			}
			if !slices.Contains(fields, "winbind") {
				fields = append(fields, "winbind")
			}
		} else if !slices.Contains(originalFields, "winbind") {
			fields = slices.DeleteFunc(fields, func(value string) bool { return value == "winbind" })
			if slices.Equal(fields, []string{"files"}) && slices.Contains(originalFields, "compat") {
				fields = slices.Clone(originalFields)
			}
		}
		lines[i] = key + ": " + strings.Join(fields, " ")
		if hasComment {
			lines[i] += " #" + comment
		}
	}
	return strings.Join(lines, "\n")
}

func (s *Service) writeNSS(enabled bool) error {
	if err := backupFile(s.nssPath, 0644); err != nil {
		return err
	}
	text, err := os.ReadFile(s.nssPath)
	if err != nil {
		return err
	}
	original, err := os.ReadFile(s.nssPath + ".pre-sylve")
	if err != nil {
		return err
	}
	return utils.AtomicWriteFile(s.nssPath, []byte(nssConfig(string(text), enabled, string(original))), 0644)
}

func (s *Service) Status(ctx context.Context, check bool) (ADStatus, error) {
	ad, err := s.GetAD()
	if err != nil {
		return ADStatus{}, err
	}
	s.stateMu.RLock()
	state, detail, checkedAt := s.state, s.lastError, s.checkedAt
	health := s.health
	s.stateMu.RUnlock()
	health.Warning = strings.TrimSpace(ad.JoinWarning + " " + health.Warning)
	status := ADStatus{controllerHealth: health, State: state, Error: detail, Joined: ad.Joined, Enabled: ad.Enabled, Domain: ad.Domain, CheckedAt: checkedAt}
	if s.RecoveryPending() {
		status.RecoveryPending = true
		status.State = "FAULTED"
		status.Error = "An interrupted directory services restore needs recovery. Open Recovery to roll back before starting Samba."
		return status, nil
	}
	if state == "JOINING" || state == "LEAVING" {
		return status, nil
	}
	if ad.MembershipPending != "" {
		status.State = "FAULTED"
		status.Error = "An interrupted membership operation needs recovery. Retry Join/Leave, or remove local membership when the domain is unavailable."
		if !check || ad.MembershipPending != "join" {
			return status, nil
		}
		if !s.mu.TryLock() {
			return status, nil
		}
		defer s.mu.Unlock()
		if err := s.recoveryReady(); err != nil {
			return s.Status(ctx, false)
		}
		ad, err = s.GetAD()
		if err != nil {
			return status, err
		}
		if ad.MembershipPending != "join" {
			return s.Status(ctx, false)
		}
		status.Joined, status.Enabled, status.Domain = ad.Joined, ad.Enabled, ad.Domain
		if _, err := s.netCommand(ctx, ad, nil, "ads", "testjoin"); err != nil {
			s.setState("FAULTED", err)
			status.Error = err.Error()
			status.CheckedAt = time.Now().UTC()
			return status, nil
		}
		ad.Joined = true
		ad.JoinWarning = recoveredJoinWarning(ad.JoinWarning, ad.SkipDNSRegistration)
		if err := s.DB.Save(&ad).Error; err != nil {
			return status, err
		}
		status.Joined = true
		if err := s.finishJoin(ctx, ad); err != nil {
			s.setState("FAULTED", err)
			status.Error = err.Error()
			status.CheckedAt = time.Now().UTC()
			return status, nil
		}
		s.setState("HEALTHY", nil)
		status.State, status.Error, status.Joined, status.CheckedAt = "HEALTHY", "", true, time.Now().UTC()
		return status, nil
	}
	if !ad.Enabled {
		if state != "FAULTED" {
			status.State = "DISABLED"
		}
		return status, nil
	}
	if !ad.Joined {
		status.State = "NOT_JOINED"
		return status, nil
	}
	if check {
		if !s.mu.TryLock() {
			return status, nil
		}
		defer s.mu.Unlock()
		if err := s.recoveryReady(); err != nil {
			return s.Status(ctx, false)
		}
		ad, err = s.GetAD()
		if err != nil {
			return status, err
		}
		if !ad.Joined || !ad.Enabled || ad.MembershipPending != "" {
			return s.Status(ctx, false)
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		health = controllerHealth{}
		err = s.Preflight(ctx, ad)
		if err == nil {
			attempt, cancel := context.WithTimeout(ctx, controllerAttemptTimeout)
			_, err = s.command(attempt, "/usr/local/bin/wbinfo", []string{"--ping-dc"}, "", nil)
			cancel()
		}
		if err == nil {
			attempt, cancel := context.WithTimeout(ctx, controllerAttemptTimeout)
			_, err = s.command(attempt, "/usr/local/bin/wbinfo", []string{"--check-secret"}, "", nil)
			cancel()
		}
		if err == nil {
			// Verify mandatory Winbind health first, so best-effort alternate
			// discovery exhausting the budget cannot fault a healthy member.
			health, err = s.checkControllers(ctx, ad)
		}
		status.State, status.Error = "HEALTHY", ""
		if err != nil {
			status.State, status.Error = "FAULTED", err.Error()
		}
		s.setState(status.State, err)
		s.stateMu.Lock()
		s.health = health
		s.stateMu.Unlock()
		health.Warning = strings.TrimSpace(ad.JoinWarning + " " + health.Warning)
		status.controllerHealth = health
		status.CheckedAt = time.Now().UTC()
	} else if status.State == "" {
		status.State = "UNKNOWN"
	}
	return status, nil
}
