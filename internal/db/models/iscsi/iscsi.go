// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsiModels

import "time"

type ISCSIInitiator struct {
	ID            uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	Nickname      string    `json:"nickname" gorm:"uniqueIndex"`
	TargetAddress string    `json:"targetAddress"`
	TargetName    string    `json:"targetName"`
	InitiatorName string    `json:"initiatorName"`
	AuthMethod    string    `json:"authMethod" gorm:"default:'None'"`
	CHAPName      string    `json:"chapName"`
	CHAPSecret    string    `json:"chapSecret"`
	TgtCHAPName   string    `json:"tgtChapName"`
	TgtCHAPSecret string    `json:"tgtChapSecret"`
	CreatedAt     time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt     time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type ISCSITargetPortal struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	TargetID  uint      `json:"targetId" gorm:"not null;index;constraint:OnDelete:CASCADE"`
	Address   string    `json:"address"`
	Port      int       `json:"port" gorm:"default:3260"`
	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type ISCSITargetLUN struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	TargetID  uint      `json:"targetId" gorm:"not null;index;constraint:OnDelete:CASCADE"`
	LUNNumber int       `json:"lunNumber"`
	ZVol      string    `json:"zvol"`
	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type ISCSITarget struct {
	ID               uint                `json:"id" gorm:"primaryKey;autoIncrement"`
	TargetName       string              `json:"targetName" gorm:"uniqueIndex"`
	Alias            string              `json:"alias"`
	AuthMethod       string              `json:"authMethod" gorm:"default:'None'"`
	CHAPName         string              `json:"chapName"`
	CHAPSecret       string              `json:"chapSecret"`
	MutualCHAPName   string              `json:"mutualChapName"`
	MutualCHAPSecret string              `json:"mutualChapSecret"`
	Portals          []ISCSITargetPortal `json:"portals" gorm:"foreignKey:TargetID"`
	LUNs             []ISCSITargetLUN    `json:"luns" gorm:"foreignKey:TargetID"`
	CreatedAt        time.Time           `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt        time.Time           `json:"updatedAt" gorm:"autoUpdateTime"`
}
