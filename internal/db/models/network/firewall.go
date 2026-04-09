// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkModels

import "time"

type FirewallTrafficRule struct {
	ID                uint      `json:"id" gorm:"primaryKey"`
	Name              string    `json:"name" gorm:"not null"`
	Description       string    `json:"description"`
	Visible           bool      `json:"visible" gorm:"not null;default:true"`
	Enabled           bool      `json:"enabled" gorm:"default:true"`
	Log               bool      `json:"log" gorm:"default:false"`
	Quick             bool      `json:"quick" gorm:"default:false"`
	Priority          int       `json:"priority" gorm:"index;default:1"`
	Action            string    `json:"action" gorm:"not null"` // pass|block
	Direction         string    `json:"direction" gorm:"not null;default:in"`
	Protocol          string    `json:"protocol" gorm:"not null;default:any"` // any|tcp|udp|icmp
	IngressInterfaces []string  `json:"ingressInterfaces" gorm:"serializer:json;type:json"`
	EgressInterfaces  []string  `json:"egressInterfaces" gorm:"serializer:json;type:json"`
	Family            string    `json:"family" gorm:"not null;default:any"` // any|inet|inet6
	SourceRaw         string    `json:"sourceRaw"`
	SourceObjID       *uint     `json:"sourceObjId"`
	SourceObj         *Object   `json:"sourceObj"`
	DestRaw           string    `json:"destRaw"`
	DestObjID         *uint     `json:"destObjId"`
	DestObj           *Object   `json:"destObj"`
	SrcPortsRaw       string    `json:"srcPortsRaw"`
	SrcPortObjID      *uint     `json:"srcPortObjId"`
	SrcPortObj        *Object   `json:"srcPortObj"`
	DstPortsRaw       string    `json:"dstPortsRaw"`
	DstPortObjID      *uint     `json:"dstPortObjId"`
	DstPortObj        *Object   `json:"dstPortObj"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type FirewallNATRule struct {
	ID                   uint      `json:"id" gorm:"primaryKey"`
	Name                 string    `json:"name" gorm:"not null"`
	Description          string    `json:"description"`
	Visible              bool      `json:"visible" gorm:"not null;default:true"`
	Enabled              bool      `json:"enabled" gorm:"default:true"`
	Log                  bool      `json:"log" gorm:"default:false"`
	Priority             int       `json:"priority" gorm:"index;default:1"`
	NATType              string    `json:"natType" gorm:"not null;default:snat"` // snat|dnat|binat
	PolicyRoutingEnabled bool      `json:"policyRoutingEnabled" gorm:"not null;default:false"`
	PolicyRouteGateway   string    `json:"policyRouteGateway"`
	IngressInterfaces    []string  `json:"ingressInterfaces" gorm:"serializer:json;type:json"` // received-on style match
	EgressInterfaces     []string  `json:"egressInterfaces" gorm:"serializer:json;type:json"`  // on style match
	Family               string    `json:"family" gorm:"not null;default:any"`                 // any|inet|inet6
	Protocol             string    `json:"protocol" gorm:"not null;default:any"`               // any|tcp|udp|icmp
	SourceRaw            string    `json:"sourceRaw"`
	SourceObjID          *uint     `json:"sourceObjId"`
	SourceObj            *Object   `json:"sourceObj"`
	DestRaw              string    `json:"destRaw"`
	DestObjID            *uint     `json:"destObjId"`
	DestObj              *Object   `json:"destObj"`
	TranslateMode        string    `json:"translateMode" gorm:"not null;default:interface"` // interface|address
	TranslateToRaw       string    `json:"translateToRaw"`
	TranslateToObjID     *uint     `json:"translateToObjId"`
	TranslateToObj       *Object   `json:"translateToObj"`
	DNATTargetRaw        string    `json:"dnatTargetRaw"`
	DNATTargetObjID      *uint     `json:"dnatTargetObjId"`
	DNATTargetObj        *Object   `json:"dnatTargetObj"`
	DstPortsRaw          string    `json:"dstPortsRaw"`
	DstPortObjID         *uint     `json:"dstPortObjId"`
	DstPortObj           *Object   `json:"dstPortObj"`
	RedirectPortsRaw     string    `json:"redirectPortsRaw"`
	RedirectPortObjID    *uint     `json:"redirectPortObjId"`
	RedirectPortObj      *Object   `json:"redirectPortObj"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type FirewallAdvancedSettings struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	PreRules  string    `json:"preRules"`
	PostRules string    `json:"postRules"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
