// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkServiceInterfaces

import "time"

type UpsertFirewallTrafficRuleRequest struct {
	Name              string   `json:"name" binding:"required,max=128"`
	Description       string   `json:"description" binding:"max=2048"`
	Enabled           *bool    `json:"enabled"`
	Log               *bool    `json:"log"`
	Quick             *bool    `json:"quick"`
	Priority          *int     `json:"priority" binding:"omitempty,gt=0,lte=1000000"`
	Action            string   `json:"action" binding:"required,oneof=pass block"`
	Direction         string   `json:"direction" binding:"required,oneof=in out"`
	Protocol          string   `json:"protocol" binding:"required,oneof=any tcp udp tcp_udp icmp"`
	IngressInterfaces []string `json:"ingressInterfaces" binding:"max=64,unique,dive,required,max=64"`
	EgressInterfaces  []string `json:"egressInterfaces" binding:"max=64,unique,dive,required,max=64"`
	Family            string   `json:"family" binding:"required,oneof=any inet inet6"`
	SourceRaw         string   `json:"sourceRaw" binding:"max=2048"`
	SourceObjID       *uint    `json:"sourceObjId" binding:"omitempty,gt=0"`
	DestRaw           string   `json:"destRaw" binding:"max=2048"`
	DestObjID         *uint    `json:"destObjId" binding:"omitempty,gt=0"`
	SrcPortsRaw       string   `json:"srcPortsRaw" binding:"max=2048"`
	SrcPortObjID      *uint    `json:"srcPortObjId" binding:"omitempty,gt=0"`
	DstPortsRaw       string   `json:"dstPortsRaw" binding:"max=2048"`
	DstPortObjID      *uint    `json:"dstPortObjId" binding:"omitempty,gt=0"`
}

type BulkDeleteFirewallTrafficRulesRequest struct {
	IDs []uint `json:"ids" binding:"required,min=1,max=1024,unique,dive,gt=0"`
}

type UpsertFirewallNATRuleRequest struct {
	Name                 string   `json:"name" binding:"required,max=128"`
	Description          string   `json:"description" binding:"max=2048"`
	Enabled              *bool    `json:"enabled"`
	Log                  *bool    `json:"log"`
	Priority             *int     `json:"priority" binding:"omitempty,gt=0,lte=1000000"`
	NATType              string   `json:"natType" binding:"required,oneof=snat dnat binat"`
	PolicyRoutingEnabled *bool    `json:"policyRoutingEnabled"`
	PolicyRouteGateway   string   `json:"policyRouteGateway" binding:"max=64"`
	IngressInterfaces    []string `json:"ingressInterfaces" binding:"max=64,unique,dive,required,max=64"`
	EgressInterfaces     []string `json:"egressInterfaces" binding:"max=64,unique,dive,required,max=64"`
	Family               string   `json:"family" binding:"required,oneof=any inet inet6"`
	Protocol             string   `json:"protocol" binding:"required,oneof=any tcp udp icmp"`
	SourceRaw            string   `json:"sourceRaw" binding:"max=2048"`
	SourceObjID          *uint    `json:"sourceObjId" binding:"omitempty,gt=0"`
	DestRaw              string   `json:"destRaw" binding:"max=2048"`
	DestObjID            *uint    `json:"destObjId" binding:"omitempty,gt=0"`
	TranslateMode        string   `json:"translateMode" binding:"omitempty,oneof=interface address"`
	TranslateToRaw       string   `json:"translateToRaw" binding:"max=2048"`
	TranslateToObjID     *uint    `json:"translateToObjId" binding:"omitempty,gt=0"`
	DNATTargetRaw        string   `json:"dnatTargetRaw" binding:"max=2048"`
	DNATTargetObjID      *uint    `json:"dnatTargetObjId" binding:"omitempty,gt=0"`
	DstPortsRaw          string   `json:"dstPortsRaw" binding:"max=2048"`
	DstPortObjID         *uint    `json:"dstPortObjId" binding:"omitempty,gt=0"`
	RedirectPortsRaw     string   `json:"redirectPortsRaw" binding:"max=2048"`
	RedirectPortObjID    *uint    `json:"redirectPortObjId" binding:"omitempty,gt=0"`
}

type BulkDeleteFirewallNATRulesRequest struct {
	IDs []uint `json:"ids" binding:"required,min=1,max=1024,unique,dive,gt=0"`
}

type FirewallAdvancedRequest struct {
	PreRules          string `json:"preRules" binding:"max=262144"`
	PreNatDecl        string `json:"preNatDecl" binding:"max=262144"`
	PostNatDecl       string `json:"postNatDecl" binding:"max=262144"`
	PreTrafficAnchor  string `json:"preTrafficAnchor" binding:"max=262144"`
	PostTrafficAnchor string `json:"postTrafficAnchor" binding:"max=262144"`
	PostRules         string `json:"postRules" binding:"max=262144"`
}

type FirewallAdvancedValidationDetails struct {
	Detail string `json:"detail"`
}

type FirewallReorderRequest struct {
	ID       uint `json:"id" binding:"required,gt=0"`
	Priority int  `json:"priority" binding:"required,gt=0,lte=1024"`
}

type FirewallTrafficRuleCounter struct {
	ID        uint      `json:"id"`
	Packets   uint64    `json:"packets"`
	Bytes     uint64    `json:"bytes"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type FirewallNATRuleCounter struct {
	ID        uint      `json:"id"`
	Packets   uint64    `json:"packets"`
	Bytes     uint64    `json:"bytes"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type FirewallLiveHitEvent struct {
	Cursor    int64     `json:"cursor"`
	Timestamp time.Time `json:"timestamp"`
	RuleType  string    `json:"ruleType"` // traffic|nat
	RuleID    uint      `json:"ruleId"`
	RuleName  string    `json:"ruleName"`
	Action    string    `json:"action"`
	Direction string    `json:"direction"`
	Interface string    `json:"interface"`
	Bytes     uint64    `json:"bytes"`
	RawLine   string    `json:"rawLine"`
}

type FirewallLiveHitsFilter struct {
	RuleType  string `json:"ruleType"`  // traffic|nat
	RuleID    *uint  `json:"ruleId"`    // optional
	Action    string `json:"action"`    // optional
	Direction string `json:"direction"` // in|out
	Interface string `json:"interface"` // optional
	Query     string `json:"query"`     // optional text search over rawLine/ruleName
}

type RenderedConfigResponse struct {
	PfConf       string `json:"pfConf"`
	ObjectTables string `json:"objectTables"`
	NatRules     string `json:"natRules"`
	TrafficRules string `json:"trafficRules"`
}

type FirewallLiveHitsResponse struct {
	Items        []FirewallLiveHitEvent `json:"items"`
	NextCursor   int64                  `json:"nextCursor"`
	SourceStatus string                 `json:"sourceStatus"` // ok|unavailable
	SourceError  string                 `json:"sourceError"`
	UpdatedAt    time.Time              `json:"updatedAt"`
}
