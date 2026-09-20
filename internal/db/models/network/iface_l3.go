// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkModels

import "time"

const (
	HostInterfaceL3IPv6ModeInherit  = "inherit"
	HostInterfaceL3IPv6ModeEnabled  = "enabled"
	HostInterfaceL3IPv6ModeDisabled = "disabled"
)

type HostInterfaceL3 struct {
	ID        uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	Interface string `json:"interface" gorm:"uniqueIndex;not null"`

	VLANParent string `json:"vlanParent"`
	VLANTag    uint16 `json:"vlanTag"`

	IPv6Mode string `json:"ipv6Mode" gorm:"default:inherit"`
	MTU      *uint  `json:"mtu"`
	Metric   *uint  `json:"metric"`

	IdentityMAC string `json:"identityMac"`

	AdoptionBaseline HostInterfaceL3Baseline     `json:"adoptionBaseline" gorm:"serializer:json;type:json"`
	AppliedState     HostInterfaceL3AppliedState `json:"appliedState" gorm:"serializer:json;type:json"`

	Revision uint64 `json:"revision" gorm:"default:1"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`

	Addresses []HostInterfaceL3Address `json:"addresses" gorm:"foreignKey:InterfaceL3ID;constraint:OnDelete:CASCADE"`
}

func (HostInterfaceL3) TableName() string { return "host_interface_l3" }

type HostInterfaceL3Baseline struct {
	Addresses  []HostInterfaceL3AppliedAddress `json:"addresses,omitempty"`
	MTU        *uint                           `json:"mtu,omitempty"`
	Metric     *uint                           `json:"metric,omitempty"`
	ND6Flags   *uint32                         `json:"nd6Flags,omitempty"`
	Up         *bool                           `json:"up,omitempty"`
	VLANParent string                          `json:"vlanParent,omitempty"`
	VLANTag    uint16                          `json:"vlanTag,omitempty"`
}

type HostInterfaceL3AppliedState struct {
	Addresses    []HostInterfaceL3AppliedAddress `json:"addresses,omitempty"`
	MTU          *uint                           `json:"mtu,omitempty"`
	Metric       *uint                           `json:"metric,omitempty"`
	IPv6Disabled *bool                           `json:"ipv6Disabled,omitempty"`
	Up           *bool                           `json:"up,omitempty"`
}

type HostInterfaceL3AppliedAddress struct {
	Family  string `json:"family"`
	Address string `json:"address"`
}

type HostInterfaceL3Address struct {
	ID            uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	InterfaceL3ID uint   `json:"interfaceL3Id" gorm:"not null;uniqueIndex:idx_host_interface_l3_address"`
	Family        string `json:"family" gorm:"not null;uniqueIndex:idx_host_interface_l3_address"`
	Address       string `json:"address" gorm:"not null;uniqueIndex:idx_host_interface_l3_address"`
	PrefixLength  uint8  `json:"prefixLength" gorm:"not null"`
	Ordering      int    `json:"ordering"`
}

func (HostInterfaceL3Address) TableName() string { return "host_interface_l3_addresses" }

type HostInterfaceL3Spec struct {
	IPv6Mode  *string                      `json:"ipv6Mode,omitempty"`
	MTU       *uint                        `json:"mtu,omitempty"`
	Metric    *uint                        `json:"metric,omitempty"`
	Addresses []HostInterfaceL3AddressSpec `json:"addresses,omitempty"`
}

type HostInterfaceL3AddressSpec struct {
	Family       string `json:"family"`
	Address      string `json:"address"`
	PrefixLength uint8  `json:"prefixLength"`
}

const (
	PendingApplyKindInterface = "interface"
	PendingApplyKindDelete    = "delete"

	PendingApplyPhasePrepared = "prepared"
	PendingApplyPhaseApplied  = "applied"
)

type PendingApply struct {
	ID        string `json:"id" gorm:"primaryKey"`
	Interface string `json:"interface" gorm:"not null;index"`
	Kind      string `json:"kind" gorm:"not null;index"`
	Phase     string `json:"phase" gorm:"not null;index"`

	CandidatePayload       HostInterfaceL3Spec         `json:"candidatePayload" gorm:"serializer:json;type:json"`
	CandidateAppliedState  HostInterfaceL3AppliedState `json:"candidateAppliedState" gorm:"serializer:json;type:json"`
	RuntimeSnapshot        HostInterfaceL3Baseline     `json:"runtimeSnapshot" gorm:"serializer:json;type:json"`
	IdentityMAC            string                      `json:"identityMac"`
	CandidateRevision      uint64                      `json:"candidateRevision"`
	RuntimeRestoreRequired bool                        `json:"runtimeRestoreRequired"`

	Deadline  time.Time `json:"deadline"`
	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

func (PendingApply) TableName() string { return "pending_apply" }
