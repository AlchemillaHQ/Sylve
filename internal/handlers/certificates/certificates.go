// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificateHandlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/certificates"
	"github.com/gin-gonic/gin"
)

type certificateService interface {
	ListCertificates(context.Context) ([]certificates.CertificateView, error)
	CreateCertificate(context.Context, certificates.CertificateInput) (*certificates.CertificateView, error)
	UpdateCertificate(context.Context, uint, certificates.CertificateInput) (*certificates.CertificateView, error)
	DeleteCertificate(context.Context, uint) error
	ActivateCertificate(context.Context, uint) (*certificates.CertificateView, error)
	CancelPendingActivation(context.Context, uint) error
	RenewCertificate(context.Context, uint) (*certificates.CertificateView, error)
	RetryManagedCertificate(context.Context, uint) (*certificates.CertificateView, error)
	CheckDomain(context.Context, string) (*certificates.DomainCheckResult, error)
}

type certificateArchiveService interface {
	ExportCertificateArchive(context.Context, uint) ([]byte, error)
}

func List(service certificateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := service.ListCertificates(c.Request.Context())
		if err != nil {
			certificateError(c, "error_listing_certificates", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[[]certificates.CertificateView]{
			Status: "success", Message: "certificates_listed", Data: items,
		})
	}
}

func Download(service certificateArchiveService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := certificateID(c)
		if !ok {
			return
		}
		archive, err := service.ExportCertificateArchive(c.Request.Context(), id)
		if err != nil {
			certificateError(c, "error_downloading_certificate", err)
			return
		}

		filename := "sylve-certificate-" + strconv.FormatUint(uint64(id), 10) + ".zip"
		c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(http.StatusOK, "application/zip", archive)
	}
}

func Create(service certificateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input certificates.CertificateInput
		if !bindCertificateInput(c, &input) {
			return
		}
		item, err := service.CreateCertificate(c.Request.Context(), input)
		if err != nil {
			certificateError(c, "error_creating_certificate", err)
			return
		}
		c.JSON(http.StatusCreated, internal.APIResponse[*certificates.CertificateView]{
			Status: "success", Message: "certificate_created", Data: item,
		})
	}
}

func Update(service certificateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := certificateID(c)
		if !ok {
			return
		}
		var input certificates.CertificateInput
		if !bindCertificateInput(c, &input) {
			return
		}
		item, err := service.UpdateCertificate(c.Request.Context(), id, input)
		if err != nil {
			certificateError(c, "error_updating_certificate", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*certificates.CertificateView]{
			Status: "success", Message: "certificate_updated", Data: item,
		})
	}
}

func Delete(service certificateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := certificateID(c)
		if !ok {
			return
		}
		if err := service.DeleteCertificate(c.Request.Context(), id); err != nil {
			certificateError(c, "error_deleting_certificate", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "certificate_deleted", Data: nil,
		})
	}
}

func Activate(service certificateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := certificateID(c)
		if !ok {
			return
		}
		item, err := service.ActivateCertificate(c.Request.Context(), id)
		if err != nil {
			certificateError(c, "error_activating_certificate", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*certificates.CertificateView]{
			Status: "success", Message: "certificate_activation_pending", Data: item,
		})
	}
}

func CancelActivation(service certificateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := certificateID(c)
		if !ok {
			return
		}
		if err := service.CancelPendingActivation(c.Request.Context(), id); err != nil {
			certificateError(c, "error_cancelling_certificate_activation", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status: "success", Message: "certificate_activation_cancelled", Data: nil,
		})
	}
}

func Renew(service certificateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := certificateID(c)
		if !ok {
			return
		}
		item, err := service.RenewCertificate(c.Request.Context(), id)
		if err != nil {
			certificateError(c, "error_renewing_certificate", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*certificates.CertificateView]{
			Status: "success", Message: "certificate_renewed", Data: item,
		})
	}
}

func Retry(service certificateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := certificateID(c)
		if !ok {
			return
		}
		item, err := service.RetryManagedCertificate(c.Request.Context(), id)
		if err != nil {
			certificateError(c, "error_retrying_certificate_issuance", err)
			return
		}
		c.JSON(http.StatusAccepted, internal.APIResponse[*certificates.CertificateView]{
			Status: "success", Message: "certificate_issuance_retried", Data: item,
		})
	}
}

func CheckDomain(service certificateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := service.CheckDomain(c.Request.Context(), c.Query("domain"))
		if err != nil {
			certificateError(c, "error_checking_certificate_domain", err)
			return
		}
		c.JSON(http.StatusOK, internal.APIResponse[*certificates.DomainCheckResult]{
			Status: "success", Message: "certificate_domain_checked", Data: result,
		})
	}
}

func bindCertificateInput(c *gin.Context, input *certificates.CertificateInput) bool {
	if err := c.ShouldBindJSON(input); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, internal.APIResponse[any]{
				Status: "error", Message: "certificate_request_too_large", Error: "certificate request body is too large", Data: nil,
			})
			return false
		}
		certificateError(c, "invalid_certificate", errors.Join(certificates.ErrInvalidCertificate, err))
		return false
	}
	return true
}

func certificateID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		certificateError(c, "invalid_certificate_id", certificates.ErrInvalidCertificate)
		return 0, false
	}
	return uint(id), true
}

func certificateError(c *gin.Context, message string, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, certificates.ErrInvalidCertificate),
		errors.Is(err, certificates.ErrNotRenewable),
		errors.Is(err, certificates.ErrRenewalNotDue):
		status = http.StatusBadRequest
	case errors.Is(err, certificates.ErrCertificateNotFound):
		status = http.StatusNotFound
	case errors.Is(err, certificates.ErrCertificateConflict):
		status = http.StatusConflict
	case errors.Is(err, certificates.ErrIssuanceFailed), errors.Is(err, certificates.ErrDomainCheckFailed):
		status = http.StatusBadGateway
	}
	c.JSON(status, internal.APIResponse[any]{
		Status: "error", Message: message, Error: err.Error(), Data: nil,
	})
}
