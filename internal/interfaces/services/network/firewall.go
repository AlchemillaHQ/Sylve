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
	Name              string   `json:"name" binding:"required"`
	Description       string   `json:"description"`
	Enabled           *bool    `json:"enabled"`
	Log               *bool    `json:"log"`
	Quick             *bool    `json:"quick"`
	Priority          *int     `json:"priority"`
	Action            string   `json:"action" binding:"required,oneof=pass block"`
	Direction         string   `json:"direction" binding:"required,oneof=in out"`
	Protocol          string   `json:"protocol" binding:"required,oneof=any tcp udp icmp"`
	IngressInterfaces []string `json:"ingressInterfaces"`
	EgressInterfaces  []string `json:"egressInterfaces"`
	Family            string   `json:"family" binding:"required,oneof=any inet inet6"`
	SourceRaw         string   `json:"sourceRaw"`
	SourceObjID       *uint    `json:"sourceObjId"`
	DestRaw           string   `json:"destRaw"`
	DestObjID         *uint    `json:"destObjId"`
	SrcPortsRaw       string   `json:"srcPortsRaw"`
	SrcPortObjID      *uint    `json:"srcPortObjId"`
	DstPortsRaw       string   `json:"dstPortsRaw"`
	DstPortObjID      *uint    `json:"dstPortObjId"`
}

type UpsertFirewallNATRuleRequest struct {
	Name                 string   `json:"name" binding:"required"`
	Description          string   `json:"description"`
	Enabled              *bool    `json:"enabled"`
	Log                  *bool    `json:"log"`
	Priority             *int     `json:"priority"`
	NATType              string   `json:"natType" binding:"required,oneof=snat dnat binat"`
	PolicyRoutingEnabled *bool    `json:"policyRoutingEnabled"`
	PolicyRouteGateway   string   `json:"policyRouteGateway"`
	IngressInterfaces    []string `json:"ingressInterfaces"`
	EgressInterfaces     []string `json:"egressInterfaces"`
	Family               string   `json:"family" binding:"required,oneof=any inet inet6"`
	Protocol             string   `json:"protocol" binding:"required,oneof=any tcp udp icmp"`
	SourceRaw            string   `json:"sourceRaw"`
	SourceObjID          *uint    `json:"sourceObjId"`
	DestRaw              string   `json:"destRaw"`
	DestObjID            *uint    `json:"destObjId"`
	TranslateMode        string   `json:"translateMode" binding:"omitempty,oneof=interface address"`
	TranslateToRaw       string   `json:"translateToRaw"`
	TranslateToObjID     *uint    `json:"translateToObjId"`
	DNATTargetRaw        string   `json:"dnatTargetRaw"`
	DNATTargetObjID      *uint    `json:"dnatTargetObjId"`
	DstPortsRaw          string   `json:"dstPortsRaw"`
	DstPortObjID         *uint    `json:"dstPortObjId"`
	RedirectPortsRaw     string   `json:"redirectPortsRaw"`
	RedirectPortObjID    *uint    `json:"redirectPortObjId"`
}

type FirewallAdvancedRequest struct {
	PreRules  string `json:"preRules"`
	PostRules string `json:"postRules"`
}

type FirewallReorderRequest struct {
	ID       uint `json:"id" binding:"required"`
	Priority int  `json:"priority" binding:"required"`
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

type FirewallLiveHitsResponse struct {
	Items        []FirewallLiveHitEvent `json:"items"`
	NextCursor   int64                  `json:"nextCursor"`
	SourceStatus string                 `json:"sourceStatus"` // ok|unavailable
	SourceError  string                 `json:"sourceError"`
	UpdatedAt    time.Time              `json:"updatedAt"`
}
