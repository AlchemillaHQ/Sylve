// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryServiceHandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/alchemillahq/sylve/internal"
	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	"github.com/alchemillahq/sylve/internal/services/directoryservices"
	"github.com/gin-gonic/gin"
)

func respond(c *gin.Context, data any, err error) {
	status, state, message := http.StatusOK, "success", "directory_services_updated"
	detail := ""
	if err != nil {
		state, message, detail, data = "error", "directory_services_error", err.Error(), nil
		switch {
		case errors.Is(err, directoryservices.ErrInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, directoryservices.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, directoryservices.ErrConflict), errors.Is(err, directoryservices.ErrBusy):
			status = http.StatusConflict
		case errors.Is(err, directoryservices.ErrUnavailable):
			status = http.StatusBadGateway
		default:
			status = http.StatusInternalServerError
		}
	}
	c.JSON(status, internal.APIResponse[any]{Status: state, Message: message, Error: detail, Data: data})
}

func bind(c *gin.Context, input any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	var payload json.RawMessage
	err := decoder.Decode(&payload)
	if err == nil {
		payload = bytes.TrimSpace(payload)
		if len(payload) == 0 || payload[0] != '{' {
			err = errors.New("expected JSON object")
		} else {
			object := json.NewDecoder(bytes.NewReader(payload))
			object.DisallowUnknownFields()
			err = object.Decode(input)
		}
	}
	if err == nil {
		if err = decoder.Decode(&struct{}{}); err == io.EOF {
			return true
		}
	}
	status := http.StatusBadRequest
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	c.AbortWithStatusJSON(status, internal.APIResponse[any]{Status: "error", Message: "invalid_directory_services_request", Error: "Expected a single JSON object with supported fields"})
	return false
}

func entryID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		respond(c, nil, directoryservices.ErrInvalid)
		return 0, false
	}
	return uint(id), true
}

// @Summary Get Active Directory configuration
// @Tags Directory Services
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[directoryModels.ActiveDirectory]
// @Router /directory-services/active-directory [get]
func GetAD(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := s.GetAD()
		respond(c, data, err)
	}
}

// @Summary Update Active Directory configuration
// @Description Enrollment passwords are stored encrypted and never returned. A blank or omitted password keeps the saved value; clearJoinPassword explicitly removes it.
// @Tags Directory Services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body directoryservices.ADInput true "Configuration (does not join the domain)"
// @Success 200 {object} internal.APIResponse[directoryModels.ActiveDirectory]
// @Failure 400 {object} internal.APIResponse[any]
// @Failure 409 {object} internal.APIResponse[any]
// @Failure 413 {object} internal.APIResponse[any]
// @Router /directory-services/active-directory [put]
func UpdateAD(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input directoryservices.ADInput
		if !bind(c, &input) {
			return
		}
		data, err := s.SaveAD(c.Request.Context(), input)
		respond(c, data, err)
	}
}

// DiscoverAD reads DNS and controller metadata without saving configuration.
func DiscoverAD(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input directoryservices.ADDiscoveryInput
		if !bind(c, &input) {
			return
		}
		data, err := s.DiscoverAD(c.Request.Context(), input)
		respond(c, data, err)
	}
}

// @Summary Get cached Active Directory status
// @Tags Directory Services
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[directoryservices.ADStatus]
// @Router /directory-services/active-directory/status [get]
func GetStatus(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := s.Status(c.Request.Context(), false)
		respond(c, data, err)
	}
}

// @Summary Check Active Directory trust and Winbind health
// @Tags Directory Services
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[directoryservices.ADStatus]
// @Router /directory-services/active-directory/check [post]
func CheckStatus(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := s.Status(c.Request.Context(), true)
		respond(c, data, err)
	}
}

// @Summary Join the configured AD domain
// @Description Uses supplied temporary credentials, the configured keytab, or the encrypted saved enrollment credentials. Temporary overrides are not saved.
// @Tags Directory Services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body directoryservices.JoinInput true "Optional temporary overrides (empty for configured keytab or saved credentials)"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 400 {object} internal.APIResponse[any]
// @Failure 409 {object} internal.APIResponse[any]
// @Failure 502 {object} internal.APIResponse[any]
// @Router /directory-services/active-directory/join [post]
func Join(s *directoryservices.Service) gin.HandlerFunc {
	return membership(s, false)
}

// @Summary Leave the AD domain
// @Tags Directory Services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body directoryservices.LeaveInput true "Leave credentials, or explicit local-only removal during an outage"
// @Success 200 {object} internal.APIResponse[any]
// @Failure 409 {object} internal.APIResponse[any]
// @Failure 502 {object} internal.APIResponse[any]
// @Router /directory-services/active-directory/leave [post]
func Leave(s *directoryservices.Service) gin.HandlerFunc { return membership(s, true) }

func membership(s *directoryservices.Service, leave bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
		defer cancel()
		var err error
		if leave {
			var input directoryservices.LeaveInput
			if !bind(c, &input) {
				return
			}
			err = s.Leave(ctx, input)
		} else {
			var input directoryservices.JoinInput
			if !bind(c, &input) {
				return
			}
			err = s.Join(ctx, input)
		}
		respond(c, nil, err)
	}
}

type EnabledInput struct {
	Enabled *bool `json:"enabled"`
}

// @Summary Enable or disable AD without leaving the domain
// @Tags Directory Services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body EnabledInput true "Enabled state"
// @Success 200 {object} internal.APIResponse[any]
// @Router /directory-services/active-directory/enabled [put]
func SetEnabled(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input EnabledInput
		if !bind(c, &input) {
			return
		}
		if input.Enabled == nil {
			respond(c, nil, directoryservices.ErrInvalid)
			return
		}
		respond(c, nil, s.SetEnabled(c.Request.Context(), *input.Enabled))
	}
}

// @Summary Search AD users or groups
// @Tags Directory Services
// @Produce json
// @Security BearerAuth
// @Param q query string true "Name, qualified name, or SID"
// @Param kind query string true "user or group"
// @Success 200 {object} internal.APIResponse[[]directoryModels.Principal]
// @Router /directory-services/principals [get]
func Principals(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := s.SearchPrincipals(c.Request.Context(), c.Query("q"), c.Query("kind"))
		respond(c, data, err)
	}
}

// @Summary List Kerberos realms
// @Tags Directory Services
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]directoryModels.KerberosRealm]
// @Router /directory-services/kerberos/realms [get]
func ListRealms(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := s.ListRealms()
		respond(c, data, err)
	}
}

type RealmDiscoveryInput struct {
	Realm string `json:"realm"`
}

// @Summary Discover Kerberos servers using DNS SRV records
// @Tags Directory Services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RealmDiscoveryInput true "Realm to discover (does not save)"
// @Success 200 {object} internal.APIResponse[directoryservices.RealmInput]
// @Router /directory-services/kerberos/realms/discover [post]
func DiscoverRealm(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input RealmDiscoveryInput
		if !bind(c, &input) {
			return
		}
		data, err := s.DiscoverRealm(c.Request.Context(), input.Realm)
		respond(c, data, err)
	}
}

// @Summary Create a Kerberos realm
// @Tags Directory Services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body directoryservices.RealmInput true "Realm"
// @Success 200 {object} internal.APIResponse[directoryModels.KerberosRealm]
// @Router /directory-services/kerberos/realms [post]
func CreateRealm(s *directoryservices.Service) gin.HandlerFunc { return saveRealm(s, false) }

// @Summary Update a Kerberos realm
// @Tags Directory Services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Realm ID"
// @Param request body directoryservices.RealmInput true "Realm"
// @Success 200 {object} internal.APIResponse[directoryModels.KerberosRealm]
// @Router /directory-services/kerberos/realms/{id} [put]
func UpdateRealm(s *directoryservices.Service) gin.HandlerFunc { return saveRealm(s, true) }

func saveRealm(s *directoryservices.Service, update bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var id uint
		if update {
			var ok bool
			id, ok = entryID(c)
			if !ok {
				return
			}
		}
		var input directoryservices.RealmInput
		if !bind(c, &input) {
			return
		}
		data, err := s.SaveRealm(c.Request.Context(), id, input)
		respond(c, data, err)
	}
}

// @Summary Delete an unused Kerberos realm
// @Tags Directory Services
// @Produce json
// @Security BearerAuth
// @Param id path int true "Realm ID"
// @Success 200 {object} internal.APIResponse[any]
// @Router /directory-services/kerberos/realms/{id} [delete]
func DeleteRealm(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := entryID(c)
		if ok {
			respond(c, nil, s.DeleteRealm(c.Request.Context(), id))
		}
	}
}

// @Summary List keytab metadata without key material
// @Tags Directory Services
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]directoryModels.KerberosKeytab]
// @Router /directory-services/kerberos/keytabs [get]
func ListKeytabs(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := s.ListKeytabs()
		respond(c, data, err)
	}
}

// @Summary Upload a Kerberos keytab
// @Tags Directory Services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body directoryservices.KeytabInput true "Name and base64 keytab file"
// @Success 200 {object} internal.APIResponse[directoryModels.KerberosKeytab]
// @Router /directory-services/kerberos/keytabs [post]
func CreateKeytab(s *directoryservices.Service) gin.HandlerFunc { return saveKeytab(s, false) }

// @Summary Rename or replace an uploaded keytab
// @Tags Directory Services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Keytab ID"
// @Param request body directoryservices.KeytabInput true "Name and optional base64 replacement file"
// @Success 200 {object} internal.APIResponse[directoryModels.KerberosKeytab]
// @Router /directory-services/kerberos/keytabs/{id} [put]
func UpdateKeytab(s *directoryservices.Service) gin.HandlerFunc { return saveKeytab(s, true) }

func saveKeytab(s *directoryservices.Service, update bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var id uint
		if update {
			var ok bool
			id, ok = entryID(c)
			if !ok {
				return
			}
		}
		var input directoryservices.KeytabInput
		if !bind(c, &input) {
			return
		}
		data, err := s.SaveKeytab(c.Request.Context(), id, input)
		respond(c, data, err)
	}
}

// @Summary Delete an unused uploaded keytab
// @Tags Directory Services
// @Produce json
// @Security BearerAuth
// @Param id path int true "Keytab ID"
// @Success 200 {object} internal.APIResponse[any]
// @Router /directory-services/kerberos/keytabs/{id} [delete]
func DeleteKeytab(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := entryID(c)
		if ok {
			respond(c, nil, s.DeleteKeytab(c.Request.Context(), id))
		}
	}
}

// @Summary Get Kerberos application and library defaults
// @Tags Directory Services
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[directoryModels.KerberosSettings]
// @Router /directory-services/kerberos/settings [get]
func GetSettings(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := s.GetKerberosSettings()
		respond(c, data, err)
	}
}

// @Summary Update validated Kerberos defaults
// @Tags Directory Services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body directoryModels.KerberosSettings true "Defaults without section headers"
// @Success 200 {object} internal.APIResponse[any]
// @Router /directory-services/kerberos/settings [put]
func UpdateSettings(s *directoryservices.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input directoryModels.KerberosSettings
		if bind(c, &input) {
			respond(c, nil, s.SaveKerberosSettings(c.Request.Context(), input))
		}
	}
}
