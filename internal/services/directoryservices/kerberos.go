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
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

type RealmInput struct {
	Realm         string   `json:"realm"`
	KDC           []string `json:"kdc"`
	AdminServer   []string `json:"adminServer"`
	KpasswdServer []string `json:"kpasswdServer"`
}

type KeytabInput struct {
	Name string `json:"name"`
	File string `json:"file"`
}

var realmName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
var hostname = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)

func validServer(value string) bool {
	host := value
	if strings.Contains(value, ":") {
		var port string
		var err error
		host, port, err = net.SplitHostPort(value)
		if err != nil {
			return false
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return hostname.MatchString(host) || net.ParseIP(host) != nil
}

func validateRealm(input RealmInput) (directoryModels.KerberosRealm, error) {
	r := directoryModels.KerberosRealm{Realm: strings.ToUpper(strings.TrimSpace(input.Realm)), KDC: input.KDC, AdminServer: input.AdminServer, KpasswdServer: input.KpasswdServer}
	if !realmName.MatchString(r.Realm) {
		return r, invalid("invalid Kerberos realm name")
	}
	for _, servers := range []*[]string{&r.KDC, &r.AdminServer, &r.KpasswdServer} {
		out := []string{}
		if len(*servers) > 32 {
			return r, invalid("too many Kerberos servers")
		}
		for _, server := range *servers {
			server = strings.TrimSpace(server)
			if !validServer(server) {
				return r, invalid("invalid Kerberos server: %s", server)
			}
			if !slices.Contains(out, server) {
				out = append(out, server)
			}
		}
		*servers = out
	}
	return r, nil
}

func (s *Service) DiscoverRealm(ctx context.Context, realm string) (RealmInput, error) {
	row, err := validateRealm(RealmInput{Realm: realm})
	if err != nil {
		return RealmInput{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result := RealmInput{Realm: row.Realm, KDC: []string{}, AdminServer: []string{}, KpasswdServer: []string{}}
	for _, group := range []struct {
		service string
		port    uint16
		servers *[]string
	}{{"kerberos", 88, &result.KDC}, {"kerberos-adm", 749, &result.AdminServer}, {"kpasswd", 464, &result.KpasswdServer}} {
		_, servers, err := s.lookupSRV(ctx, group.service, "tcp", strings.ToLower(row.Realm))
		if err != nil || len(servers) == 0 {
			if group.service == "kerberos" {
				return result, fmt.Errorf("%w: Kerberos DNS SRV discovery failed", ErrUnavailable)
			}
			continue
		}
		for _, server := range servers {
			host := strings.TrimSuffix(server.Target, ".")
			if !hostname.MatchString(host) || server.Port == 0 {
				continue
			}
			if server.Port != group.port {
				host = net.JoinHostPort(host, strconv.Itoa(int(server.Port)))
			}
			if !slices.Contains(*group.servers, host) {
				*group.servers = append(*group.servers, host)
			}
			if len(*group.servers) >= 32 {
				break
			}
		}
	}
	if len(result.KDC) == 0 {
		return result, fmt.Errorf("%w: no usable Kerberos servers discovered", ErrUnavailable)
	}
	return result, nil
}

func (s *Service) ListRealms() ([]directoryModels.KerberosRealm, error) {
	rows := []directoryModels.KerberosRealm{}
	err := s.DB.Order("realm").Find(&rows).Error
	for i := range rows {
		if rows[i].KDC == nil {
			rows[i].KDC = []string{}
		}
		if rows[i].AdminServer == nil {
			rows[i].AdminServer = []string{}
		}
		if rows[i].KpasswdServer == nil {
			rows[i].KpasswdServer = []string{}
		}
	}
	return rows, err
}

func (s *Service) SaveRealm(ctx context.Context, id uint, input RealmInput) (*directoryModels.KerberosRealm, error) {
	row, err := validateRealm(input)
	if err != nil {
		return nil, err
	}
	unlock, err := s.LockConfiguration()
	if err != nil {
		return nil, err
	}
	defer unlock()
	err = s.mutateKerberos(ctx, func(tx *gorm.DB) error {
		if id != 0 {
			var old directoryModels.KerberosRealm
			if err := tx.First(&old, id).Error; err != nil {
				return recordError(err)
			}
			ad, err := getAD(tx)
			if err != nil {
				return err
			}
			if ad.RealmID == id && row.Realm != old.Realm {
				return fmt.Errorf("%w: AD references this realm", ErrConflict)
			}
		}
		row.ID = id
		return recordError(tx.Save(&row).Error)
	})
	return &row, err
}

func (s *Service) DeleteRealm(ctx context.Context, id uint) error {
	unlock, err := s.LockConfiguration()
	if err != nil {
		return err
	}
	defer unlock()
	return s.mutateKerberos(ctx, func(tx *gorm.DB) error {
		var row directoryModels.KerberosRealm
		if err := tx.First(&row, id).Error; err != nil {
			return recordError(err)
		}
		ad, err := getAD(tx)
		if err != nil {
			return err
		}
		if ad.RealmID == id {
			return fmt.Errorf("%w: AD references this realm", ErrConflict)
		}
		return tx.Delete(&row).Error
	})
}

func (s *Service) ListKeytabs() ([]directoryModels.KerberosKeytab, error) {
	rows := []directoryModels.KerberosKeytab{}
	// Key material is neither loaded nor returned by the listing endpoint.
	err := s.DB.Omit("Ciphertext").Order("name").Find(&rows).Error
	return rows, err
}

func (s *Service) SaveKeytab(ctx context.Context, id uint, input KeytabInput) (*directoryModels.KerberosKeytab, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 120 || strings.ContainsAny(name, "\x00\r\n") || name == MachineKeytabName {
		return nil, invalid("invalid or reserved keytab name")
	}
	unlock, err := s.LockConfiguration()
	if err != nil {
		return nil, err
	}
	defer unlock()
	var row directoryModels.KerberosKeytab
	if id != 0 {
		if err := s.DB.First(&row, id).Error; err != nil {
			return nil, recordError(err)
		}
		if row.Managed {
			return nil, fmt.Errorf("%w: machine keytabs are managed automatically", ErrConflict)
		}
	}
	row.Name = name
	if input.File != "" {
		decoded, err := base64.StdEncoding.DecodeString(input.File)
		if err != nil {
			return nil, invalid("keytab must be base64 encoded")
		}
		entries, err := parseKeytab(decoded)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return nil, invalid("keytab contains no principals")
		}
		row.Principals = keytabPrincipals(entries)
		var managed []directoryModels.KerberosKeytab
		if err := s.DB.Omit("Ciphertext").Where("managed = ?", true).Find(&managed).Error; err != nil {
			return nil, err
		}
		for _, keytab := range managed {
			for _, principal := range row.Principals {
				if slices.Contains(keytab.Principals, principal) {
					return nil, fmt.Errorf("%w: an upload cannot replace managed machine principals", ErrConflict)
				}
			}
		}
		row.Ciphertext, err = s.encryptKeytab(decoded)
		if err != nil {
			return nil, err
		}
	} else if id == 0 {
		return nil, invalid("a keytab file is required")
	}
	err = s.mutateKerberos(ctx, func(tx *gorm.DB) error {
		ad, err := getAD(tx)
		if err != nil {
			return err
		}
		if ad.KeytabID == id && id != 0 && !slices.Contains(row.Principals, ad.KerberosPrincipal) {
			return fmt.Errorf("%w: replacement removes AD's selected principal", ErrConflict)
		}
		return recordError(tx.Save(&row).Error)
	})
	row.Ciphertext = nil
	return &row, err
}

func (s *Service) DeleteKeytab(ctx context.Context, id uint) error {
	unlock, err := s.LockConfiguration()
	if err != nil {
		return err
	}
	defer unlock()
	return s.mutateKerberos(ctx, func(tx *gorm.DB) error {
		var row directoryModels.KerberosKeytab
		if err := tx.First(&row, id).Error; err != nil {
			return recordError(err)
		}
		ad, err := getAD(tx)
		if err != nil {
			return err
		}
		if row.Managed || ad.KeytabID == id {
			return fmt.Errorf("%w: AD requires this keytab", ErrConflict)
		}
		return tx.Delete(&row).Error
	})
}

func getKerberosSettings(db *gorm.DB) (directoryModels.KerberosSettings, error) {
	row := directoryModels.KerberosSettings{ID: 1}
	err := db.First(&row, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	return row, err
}

func (s *Service) GetKerberosSettings() (directoryModels.KerberosSettings, error) {
	return getKerberosSettings(s.DB)
}

// Own the section boundaries. Auxiliary text may supply scalar defaults and
// app/realm blocks, but cannot include files or replace generated sections.
func validateKerberosDefaults(text string, app bool) error {
	if len(text) > 65536 || strings.ContainsAny(text, "\x00[]") {
		return invalid("invalid Kerberos defaults")
	}
	depth := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if line == "}" {
			depth--
			if depth < 0 {
				return invalid("unbalanced Kerberos defaults")
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || !realmName.MatchString(key) || value == "" {
			return invalid("expected key = value in Kerberos defaults")
		}
		if value == "{" {
			depth++
			if !app || depth > 2 {
				return invalid("blocks are only supported for application defaults")
			}
			continue
		}
		if strings.ContainsAny(value, "{}\\") || strings.IndexFunc(value, unicode.IsControl) >= 0 || strings.HasPrefix(key, "include") {
			return invalid("invalid Kerberos default")
		}
		allowed := libDefaultKeys
		if app {
			allowed = appDefaultKeys
		}
		if !slices.Contains(allowed, key) {
			return invalid("unsupported Kerberos default: %s", key)
		}
		if err := validateDefaultValue(key, value); err != nil {
			return err
		}
	}
	if depth != 0 {
		return invalid("unbalanced Kerberos defaults")
	}
	return nil
}

var libDefaultKeys = []string{"default_realm", "dns_lookup_realm", "dns_lookup_kdc", "dns_canonicalize_hostname", "rdns", "ticket_lifetime", "renew_lifetime", "forwardable", "proxiable", "clockskew", "canonicalize", "noaddresses", "no-addresses", "allow_weak_crypto", "default_tkt_enctypes", "default_tgs_enctypes", "permitted_enctypes", "udp_preference_limit", "kdc_timeout", "max_retries", "ccache_type", "verify_ap_req_nofail", "kdc_timesync"}
var appDefaultKeys = []string{"forwardable", "proxiable", "no-addresses", "noaddresses", "ticket_lifetime", "renew_lifetime", "encrypt", "forward", "renewable", "validate", "addressless"}

var kerberosDuration = regexp.MustCompile(`^(?:[0-9]+|(?:[0-9]+[wdhms])+|[0-9]+:[0-5][0-9]:[0-5][0-9])$`)

func validateDefaultValue(key, value string) error {
	// Comments are allowed, but directives and file inclusions are not.
	value, _, _ = strings.Cut(value, "#")
	value, _, _ = strings.Cut(value, ";")
	value = strings.TrimSpace(value)
	valid := false
	switch key {
	case "default_realm":
		valid = realmName.MatchString(value)
	case "ticket_lifetime", "renew_lifetime":
		valid = len(value) <= 32 && kerberosDuration.MatchString(value)
	case "clockskew", "udp_preference_limit", "kdc_timeout", "max_retries", "ccache_type", "kdc_timesync":
		n, err := strconv.ParseUint(value, 10, 32)
		valid = err == nil && n <= 2147483647
		if key == "ccache_type" {
			valid = err == nil && n >= 1 && n <= 4
		}
		if key == "kdc_timesync" {
			valid = err == nil && n <= 1
		}
	case "default_tkt_enctypes", "default_tgs_enctypes", "permitted_enctypes":
		types := strings.Fields(value)
		valid = len(types) > 0
		for _, enctype := range types {
			if !slices.Contains([]string{"aes256-cts-hmac-sha1-96", "aes128-cts-hmac-sha1-96", "aes256-cts-hmac-sha384-192", "aes128-cts-hmac-sha256-128", "arcfour-hmac-md5", "arcfour-hmac", "rc4-hmac", "des3-cbc-sha1", "aes256-cts", "aes128-cts", "DEFAULT"}, enctype) {
				valid = false
			}
		}
	default:
		valid = slices.Contains([]string{"true", "false", "yes", "no"}, strings.ToLower(value))
		if key == "dns_canonicalize_hostname" && value == "fallback" {
			valid = true
		}
	}
	if !valid {
		return invalid("invalid value for Kerberos default %s", key)
	}
	return nil
}

func (s *Service) SaveKerberosSettings(ctx context.Context, row directoryModels.KerberosSettings) error {
	if err := validateKerberosDefaults(row.AppDefaults, true); err != nil {
		return err
	}
	if err := validateKerberosDefaults(row.LibDefaults, false); err != nil {
		return err
	}
	unlock, err := s.LockConfiguration()
	if err != nil {
		return err
	}
	defer unlock()
	row.ID = 1
	return s.mutateKerberos(ctx, func(tx *gorm.DB) error { return tx.Save(&row).Error })
}

func kerberosConfig(realms []directoryModels.KerberosRealm, settings directoryModels.KerberosSettings, ad directoryModels.ActiveDirectory, keytabPath string) string {
	var b strings.Builder
	b.WriteString("# Generated by Sylve Directory Services; do not edit.\n[libdefaults]\n")
	fmt.Fprintf(&b, " default_keytab_name = FILE:%s\n", keytabPath)
	defaults := [][2]string{{"dns_lookup_kdc", "true"}, {"dns_lookup_realm", "false"}, {"rdns", "false"}, {"forwardable", "true"}, {"ticket_lifetime", "24h"}, {"renew_lifetime", "7d"}}
	if ad.Domain != "" {
		defaults = append(defaults, [2]string{"default_realm", ad.Domain})
	}
	supplied := map[string]bool{}
	for _, line := range strings.Split(settings.LibDefaults, "\n") {
		key, _, ok := strings.Cut(line, "=")
		if ok {
			supplied[strings.TrimSpace(key)] = true
		}
	}
	// Kerberos implementations disagree on which duplicate wins. Never emit
	// duplicate generated keys: advanced defaults actually replace defaults.
	for _, entry := range defaults {
		if !supplied[entry[0]] {
			fmt.Fprintf(&b, " %s = %s\n", entry[0], entry[1])
		}
	}
	b.WriteString(settings.LibDefaults + "\n[appdefaults]\n" + settings.AppDefaults + "\n[realms]\n")
	for _, realm := range realms {
		fmt.Fprintf(&b, " %s = {\n", realm.Realm)
		for _, group := range []struct {
			key    string
			values []string
		}{{"kdc", realm.KDC}, {"admin_server", realm.AdminServer}, {"kpasswd_server", realm.KpasswdServer}} {
			for _, value := range group.values {
				fmt.Fprintf(&b, "  %s = %s\n", group.key, value)
			}
		}
		b.WriteString(" }\n")
	}
	b.WriteString("[domain_realm]\n")
	for _, realm := range realms {
		fmt.Fprintf(&b, " %s = %s\n .%s = %s\n", strings.ToLower(realm.Realm), realm.Realm, strings.ToLower(realm.Realm), realm.Realm)
	}
	return b.String()
}

func (s *Service) writeKerberos(db *gorm.DB) error {
	var realms []directoryModels.KerberosRealm
	var keytabs []directoryModels.KerberosKeytab
	if err := db.Order("realm").Find(&realms).Error; err != nil {
		return err
	}
	if err := db.Order("managed, id").Find(&keytabs).Error; err != nil {
		return err
	}
	settings, err := getKerberosSettings(db)
	if err != nil {
		return err
	}
	ad, err := getAD(db)
	if err != nil {
		return err
	}
	if err := backupFile(s.krbPath, 0644); err != nil {
		return err
	}
	// Record an empty original too, so subsequent regenerations cannot mistake
	// Sylve's generated keytab for pre-existing unmanaged credentials.
	if info, err := os.Lstat(s.keytabPath + ".pre-sylve"); os.IsNotExist(err) {
		original, err := snapshot(s.keytabPath)
		if err != nil {
			return err
		}
		data := original.data
		if !original.exists {
			data = []byte{5, 2}
		}
		if err := utils.AtomicWriteFile(s.keytabPath+".pre-sylve", data, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("system keytab backup must be a regular file")
	}
	original, err := os.ReadFile(s.keytabPath + ".pre-sylve")
	if err != nil {
		return err
	}
	files := [][]byte{original}
	for _, keytab := range keytabs {
		data, err := s.decryptKeytab(keytab.Ciphertext)
		if err != nil {
			return err
		}
		files = append(files, data)
	}
	merged, err := mergeKeytabs(files...)
	if err != nil {
		return err
	}
	if err := utils.AtomicWriteFile(s.keytabPath, merged, 0600); err != nil {
		return err
	}
	return utils.AtomicWriteFile(s.krbPath, []byte(kerberosConfig(realms, settings, ad, s.keytabPath)), 0644)
}

func (s *Service) mutateKerberos(ctx context.Context, mutate func(*gorm.DB) error) error {
	files := []fileSnapshot{}
	for _, path := range []string{s.krbPath, s.keytabPath} {
		f, err := snapshot(path)
		if err != nil {
			return err
		}
		files = append(files, f)
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := mutate(tx); err != nil {
			return err
		}
		return s.writeKerberos(tx)
	})
	if err != nil {
		return errors.Join(err, restoreFiles(files))
	}
	return nil
}
