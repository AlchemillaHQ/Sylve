// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package models

import "time"

type CertificateType string

const (
	CertificateTypeImported      CertificateType = "imported"
	CertificateTypeSelfSigned    CertificateType = "self-signed"
	CertificateTypeLetsEncrypt   CertificateType = "lets-encrypt"
	CertificateTypeSylveManaged  CertificateType = "sylve-managed"
	CertificateTypeSystemDefault CertificateType = "system-default"
)

type ManagedCertificateOperation string

const (
	ManagedCertificateOperationInitial ManagedCertificateOperation = "initial"
	ManagedCertificateOperationRenewal ManagedCertificateOperation = "renewal"
)

type ManagedCertificateOrderStatus string

const (
	ManagedCertificateOrderStatusSubmitting ManagedCertificateOrderStatus = "submitting"
	ManagedCertificateOrderStatusQueued     ManagedCertificateOrderStatus = "queued"
	ManagedCertificateOrderStatusProcessing ManagedCertificateOrderStatus = "processing"
	ManagedCertificateOrderStatusBlocked    ManagedCertificateOrderStatus = "blocked"
	ManagedCertificateOrderStatusFailed     ManagedCertificateOrderStatus = "failed"
	ManagedCertificateOrderStatusIssued     ManagedCertificateOrderStatus = "issued"
)

type Certificate struct {
	ID uint `json:"id" gorm:"primaryKey;autoIncrement"`

	Name              string          `json:"name" gorm:"uniqueIndex;not null"`
	Type              CertificateType `json:"type" gorm:"not null;index"`
	Domain            string          `json:"domain" gorm:"not null"`
	Staging           bool            `json:"staging" gorm:"not null;default:false"`
	DynamicDNSEntryID *uint           `json:"dynamicDnsEntryId" gorm:"index"`

	CertificatePEM string `json:"-" gorm:"type:text"`
	PrivateKeyPEM  string `json:"-" gorm:"type:text"`
	Fingerprint    string `json:"fingerprint" gorm:"index"`

	NotBefore *time.Time `json:"notBefore"`
	NotAfter  *time.Time `json:"notAfter" gorm:"index"`
	RenewedAt *time.Time `json:"renewedAt"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

func (Certificate) TableName() string {
	return "certificates"
}

type ManagedCertificateOrder struct {
	ID uint `json:"id" gorm:"primaryKey;autoIncrement"`

	CertificateID uint                          `json:"certificateId" gorm:"not null;uniqueIndex"`
	OrderID       string                        `json:"orderId" gorm:"size:36;not null;uniqueIndex"`
	Operation     ManagedCertificateOperation   `json:"operation" gorm:"not null"`
	Status        ManagedCertificateOrderStatus `json:"status" gorm:"not null;index"`

	CSRPEM        string `json:"-" gorm:"column:csr_pem;type:text;not null"`
	PrivateKeyPEM string `json:"-" gorm:"type:text;not null"`

	BlockedByOrderID string     `json:"blockedByOrderId" gorm:"size:36"`
	SubmittedAt      *time.Time `json:"submittedAt"`
	RetryAt          *time.Time `json:"retryAt" gorm:"index"`
	Error            string     `json:"error" gorm:"type:text"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

func (ManagedCertificateOrder) TableName() string {
	return "managed_certificate_orders"
}

type CertificateSettings struct {
	ID                   uint  `json:"id" gorm:"primaryKey"`
	ActiveCertificateID  uint  `json:"activeCertificateId" gorm:"not null"`
	PendingCertificateID *uint `json:"pendingCertificateId"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

func (CertificateSettings) TableName() string {
	return "certificate_settings"
}
