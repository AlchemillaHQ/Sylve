// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryServiceModels

import "time"

type KerberosRealm struct {
	ID            uint     `json:"id" gorm:"primaryKey"`
	Realm         string   `json:"realm" gorm:"uniqueIndex;not null"`
	KDC           []string `json:"kdc" gorm:"serializer:json;type:text"`
	AdminServer   []string `json:"adminServer" gorm:"serializer:json;type:text"`
	KpasswdServer []string `json:"kpasswdServer" gorm:"serializer:json;type:text"`
}

type KerberosKeytab struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	Name       string    `json:"name" gorm:"uniqueIndex;not null"`
	Ciphertext []byte    `json:"-" gorm:"type:blob;not null"`
	Principals []string  `json:"principals" gorm:"serializer:json;type:text"`
	Managed    bool      `json:"managed" gorm:"not null;default:false"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type KerberosSettings struct {
	ID          uint   `json:"-" gorm:"primaryKey"`
	AppDefaults string `json:"appDefaults" gorm:"type:text;not null;default:''"`
	LibDefaults string `json:"libDefaults" gorm:"type:text;not null;default:''"`
}

// Ranges must remain stable: filesystem ACLs store the mapped Unix IDs.
type IDMap struct {
	Domain    string `json:"domain"`
	Backend   string `json:"backend"`
	RangeLow  uint32 `json:"rangeLow"`
	RangeHigh uint32 `json:"rangeHigh"`
	Schema    string `json:"schema"`
}

type ActiveDirectory struct {
	ID                     uint    `json:"-" gorm:"primaryKey"`
	Enabled                bool    `json:"enabled" gorm:"not null;default:false"`
	Joined                 bool    `json:"joined" gorm:"not null;default:false"`
	MembershipPending      string  `json:"membershipPending" gorm:"not null;default:''"`
	Domain                 string  `json:"domain"`
	Workgroup              string  `json:"workgroup"`
	ComputerName           string  `json:"computerName"`
	Site                   string  `json:"site"`
	ComputerOU             string  `json:"computerOU"`
	DomainController       string  `json:"domainController"`
	RealmID                uint    `json:"realmId"`
	KeytabID               uint    `json:"keytabId"`
	KerberosPrincipal      string  `json:"kerberosPrincipal"`
	JoinUsername           string  `json:"joinUsername" gorm:"not null;default:''"`
	JoinPasswordCiphertext []byte  `json:"-" gorm:"type:blob"`
	JoinPasswordConfigured bool    `json:"joinPasswordConfigured" gorm:"-"`
	AllowTrustedDomains    bool    `json:"allowTrustedDomains"`
	UseDefaultDomain       bool    `json:"useDefaultDomain"`
	DisableCache           bool    `json:"disableCache"`
	SkipDNSRegistration    bool    `json:"skipDNSRegistration" gorm:"not null;default:false"`
	JoinWarning            string  `json:"joinWarning" gorm:"not null;default:''"`
	IDMapBackend           string  `json:"idmapBackend" gorm:"not null;default:rid"`
	RangeLow               uint32  `json:"rangeLow" gorm:"not null;default:1000000"`
	RangeHigh              uint32  `json:"rangeHigh" gorm:"not null;default:1999999999"`
	IDMapSchema            string  `json:"idmapSchema" gorm:"not null;default:rfc2307"`
	TrustedDomains         []IDMap `json:"trustedDomains" gorm:"serializer:json;type:text"`
}

// Directory identities are not Sylve accounts and confer no web-UI access.
type Principal struct {
	SID    string `json:"sid"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	UnixID uint32 `json:"unixId"`
}

type SharePermissions struct {
	Read  []Principal `json:"read"`
	Write []Principal `json:"write"`
}
