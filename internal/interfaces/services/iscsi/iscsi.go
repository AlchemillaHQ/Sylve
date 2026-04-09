// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsiServiceInterfaces

import iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"

type ISCSIServiceInterface interface {
	WriteConfig(reload bool) error
	GetInitiators() ([]iscsiModels.ISCSIInitiator, error)
	GetStatus() (map[string]string, error)

	GetTargets() ([]iscsiModels.ISCSITarget, error)
	CreateTarget(targetName, alias, authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret string) error
	UpdateTarget(id uint, targetName, alias, authMethod, chapName, chapSecret, mutualChapName, mutualChapSecret string) error
	DeleteTarget(id uint) error
	AddPortal(targetID uint, address string, port int) error
	RemovePortal(id uint) error
	AddLUN(targetID uint, lunNumber int, zvol string) error
	RemoveLUN(id uint) error
	GenerateTargetConfig() (string, error)
	WriteTargetConfig(reload bool) error
}
