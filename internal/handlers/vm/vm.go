// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type VMEditDescRequest struct {
	Description string `json:"description"`
}

type VMEditNameRequest struct {
	Name string `json:"name" binding:"required"`
}

type VMCreateResponse struct {
	RID  uint   `json:"rid"`
	Name string `json:"name"`
}

type VMActionResponse struct {
	TaskID  uint   `json:"taskId"`
	RID     uint   `json:"rid"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
}

type vmRemovalService interface {
	PurgeVMRegistration(rid uint, cleanUpMacs bool) ([]string, error)
	ForceRemoveVM(rid uint, cleanUpMacs bool, ctx context.Context) ([]string, error)
	RemoveVMWithWarnings(
		rid uint,
		cleanUpMacs bool,
		deleteRawDisks bool,
		deleteVolumes bool,
		ctx context.Context,
	) (libvirt.VMRemovalResult, error)
}

type vmDetailService interface {
	GetVMByRID(rid uint) (vmModels.VM, error)
}

type vmSimpleDetailService interface {
	GetSimpleVMByRID(rid uint) (libvirtServiceInterfaces.SimpleList, error)
}

type vmCreateService interface {
	CreateVM(req libvirtServiceInterfaces.CreateVMRequest, ctx context.Context) error
}

type vmDescriptionService interface {
	UpdateDescription(rid uint, description string) error
}

type vmNameService interface {
	UpdateName(rid uint, name string) error
}

type vmActionPreflightService interface {
	GetVMByRID(rid uint) (vmModels.VM, error)
	CanPerformVMAction(rid uint, action string) (bool, error)
}

type vmLifecycleRequestService interface {
	RequestAction(
		ctx context.Context,
		guestType string,
		guestID uint,
		action string,
		source string,
		requestedBy string,
	) (*taskModels.GuestLifecycleTask, string, error)
}

type vmDomainService interface {
	GetVMIDByRID(rid uint) (uint, error)
	GetLvDomain(rid uint) (*libvirtServiceInterfaces.LvDomain, error)
}

type vmDomainLifecycleService interface {
	GetActiveTaskForGuest(guestType string, guestID uint) (*taskModels.GuestLifecycleTask, error)
}

var vmCreateConflictCodes = map[string]struct{}{
	"mac_object_already_in_use":                  {},
	"rid_or_name_already_in_use":                 {},
	"vm_create_stale_artifacts_detected":         {},
	"vm_id_already_exists":                       {},
	"vnc_port_already_in_use_by_another_vm":      {},
	"vnc_port_already_in_use_by_another_service": {},
}

var vmCreateBadRequestCodes = map[string]struct{}{
	"calculated_core_out_of_range":                     {},
	"cloud_init_data_missing":                          {},
	"cloud_init_requires_iso":                          {},
	"cloud_init_requires_storage":                      {},
	"core_conflict":                                    {},
	"core_index_out_of_range":                          {},
	"cpu_pinning_exceeds_logical_cores":                {},
	"cpu_pinning_exceeds_total_vcpus":                  {},
	"cpu_sockets_cores_threads_must_be_greater_than_1": {},
	"disk_size_must_be_greater_than_128mb":             {},
	"duplicate_core_across_sockets":                    {},
	"duplicate_core_within_socket":                     {},
	"duplicate_socket_in_request":                      {},
	"empty_core_list_for_socket":                       {},
	"invalid_cloud_init_yaml":                          {},
	"invalid_iso_or_image_format":                      {},
	"invalid_boot_rom":                                 {},
	"invalid_mac_object_type":                          {},
	"invalid_rid":                                      {},
	"invalid_vnc_bind_ip":                              {},
	"invalid_topology_vcpu_is_zero":                    {},
	"invalid_vm_name":                                  {},
	"iso_or_image_not_found":                           {},
	"mac_object_has_no_entries":                        {},
	"mac_object_not_found":                             {},
	"media_not_cloud_init_capable":                     {},
	"memory_must_be_greater_than_128mb":                {},
	"no_emulation_type_selected":                       {},
	"no_switch_emulation_type_selected":                {},
	"passthrough_device_does_not_exist":                {},
	"pool_not_found":                                   {},
	"socket_capacity_exceeded":                         {},
	"socket_index_out_of_range":                        {},
	"start_order_must_be_greater_than_or_equal_to_0":   {},
	"storage_size_greater_than_available":              {},
	"switch_not_found":                                 {},
	"unsupported_download_type":                        {},
	"vnc_password_cannot_contain_commas":               {},
	"vnc_password_required":                            {},
	"vnc_port_must_be_between_1_and_65535":             {},
}

var vmCreateBadRequestCodePrefixes = []string{
	"no_pool_selected_for_",
	"size_should_be_at_least_",
}

var vmCreateAliasCodes = map[string]string{
	"cloud_init_media_not_resolvable":               "iso_or_image_not_found",
	"failed_to_fetch_iso_for_cloud_init_validation": "iso_or_image_not_found",
	"failed_to_find_download":                       "iso_or_image_not_found",
	"failed_to_find_iso":                            "iso_or_image_not_found",
	"failed_to_find_iso_by_uuid":                    "iso_or_image_not_found",
	"image_not_resolvable":                          "iso_or_image_not_found",
	"iso_or_img_not_found":                          "iso_or_image_not_found",
	"iso_or_img_not_found_in_path":                  "iso_or_image_not_found",
	"iso_or_img_not_found_in_torrent":               "iso_or_image_not_found",
}

func isVMNotFoundError(err error) bool {
	return err != nil && (errors.Is(err, gorm.ErrRecordNotFound) ||
		strings.Contains(strings.ToLower(err.Error()), "vm_not_found"))
}

func isLibvirtDomainAbsent(err error) bool {
	return libvirtServiceInterfaces.IsDomainNotFoundError(err)
}

func isSnakeCaseErrorCode(value string) bool {
	if value == "" || !strings.Contains(value, "_") {
		return false
	}

	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}

		return false
	}

	return true
}

func extractVMCreateErrorCode(message string) string {
	parts := strings.Split(strings.ToLower(message), ":")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}

		token := part
		if idx := strings.IndexAny(token, " \t\r\n,.;()[]{}"); idx >= 0 {
			token = token[:idx]
		}
		token = strings.TrimSpace(token)

		if isSnakeCaseErrorCode(token) {
			return token
		}
	}

	return ""
}

func classifyCreateVMError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "failed_to_create_vm"
	}

	errText := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errText, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	case strings.Contains(errText, "guest_identity_inventory_unavailable"):
		return http.StatusServiceUnavailable, "guest_identity_inventory_unavailable"
	case strings.Contains(errText, "guest_identity_inventory_scan_failed"):
		return http.StatusInternalServerError, "guest_identity_inventory_scan_failed"
	case strings.Contains(errText, "guest_identity_inventory_conflict"):
		return http.StatusConflict, "guest_identity_inventory_conflict"
	case strings.Contains(errText, "guest_id_already_in_use"):
		return http.StatusConflict, "guest_id_already_in_use"
	}

	if strings.Contains(errText, "exists=true, allowed=false") {
		return http.StatusBadRequest, "invalid_iso_or_image_format"
	}

	if strings.Contains(errText, "failed to define vm domain") && strings.Contains(errText, "already exists") {
		return http.StatusConflict, "vm_id_already_exists"
	}

	code := extractVMCreateErrorCode(errText)
	if alias, ok := vmCreateAliasCodes[code]; ok {
		code = alias
	}

	switch code {
	case "failed_to_create_vm_with_associations":
		return http.StatusInternalServerError, "vm_create_database_failure"
	case "failed_to_create_lv_vm",
		"failed_to_create_cloud_init_iso",
		"failed_to_create_storage_parent",
		"failed_to_flash_cloud_init_to_disk",
		"failed_to_remove_cloud_init_storage_entry":
		return http.StatusInternalServerError, "vm_create_runtime_failure"
	case "failed_to_list_usable_pools_for_vm_create_precheck",
		"failed_to_list_vm_datasets_for_create_precheck",
		"libvirt_not_initialized",
		"system_service_not_initialized",
		"zfs_client_not_initialized":
		return http.StatusServiceUnavailable, "vm_create_dependency_not_ready"
	}

	if _, ok := vmCreateConflictCodes[code]; ok {
		return http.StatusConflict, code
	}

	if _, ok := vmCreateBadRequestCodes[code]; ok {
		return http.StatusBadRequest, code
	}

	for _, prefix := range vmCreateBadRequestCodePrefixes {
		if strings.HasPrefix(code, prefix) {
			return http.StatusBadRequest, code
		}
	}

	if strings.HasPrefix(code, "invalid_") {
		return http.StatusBadRequest, code
	}

	if code != "" {
		return http.StatusInternalServerError, code
	}

	return http.StatusInternalServerError, "failed_to_create_vm"
}

func classifyUpdateVMDescriptionError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "failed_to_update_vm_description"
	}

	errText := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errText, "vm_not_found"):
		return http.StatusNotFound, "vm_not_found"
	case strings.Contains(errText, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	case strings.Contains(errText, "invalid_description"):
		return http.StatusBadRequest, "invalid_description"
	case strings.Contains(errText, "replication_lease_check_failed"):
		return http.StatusInternalServerError, "replication_lease_check_failed"
	default:
		return http.StatusInternalServerError, "failed_to_update_vm_description"
	}
}

func classifyRemoveVMError(err error, fallback string) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, fallback
	}

	errText := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errText, "vm_not_found"):
		return http.StatusNotFound, "vm_not_found"
	case strings.Contains(errText, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	case strings.Contains(errText, "guest_delete_requires_replication_policy_removed"):
		return http.StatusConflict, "guest_delete_requires_replication_policy_removed"
	case strings.Contains(errText, "replication_storage_topology_change_requires_policy_disabled"):
		return http.StatusConflict, "replication_storage_topology_change_requires_policy_disabled"
	case strings.Contains(errText, "vm_not_orphaned"):
		return http.StatusConflict, "vm_not_orphaned"
	case strings.Contains(errText, "lifecycle_task_in_progress"),
		strings.Contains(errText, "vm_operation_in_progress"):
		return http.StatusConflict, "vm_operation_in_progress"
	case strings.Contains(errText, "vm_orphan_check_unavailable"),
		strings.Contains(errText, "libvirt_service_not_initialized"):
		return http.StatusServiceUnavailable, "vm_delete_dependency_not_ready"
	default:
		return http.StatusInternalServerError, fallback
	}
}

func parseVMRID(c *gin.Context) (uint, bool) {
	ridValue := strings.TrimSpace(c.Param("rid"))
	rid, err := strconv.ParseUint(ridValue, 10, 32)
	if err != nil || rid == 0 {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_vm_rid",
			Data:    nil,
			Error:   "Virtual Machine RID must be a positive integer",
		})
		return 0, false
	}

	return uint(rid), true
}

func parseOptionalBoolQuery(c *gin.Context, name string, defaultValue bool) (bool, bool) {
	rawValue := strings.TrimSpace(c.Query(name))
	if rawValue == "" {
		return defaultValue, true
	}

	value, err := strconv.ParseBool(rawValue)
	if err != nil {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_" + name + "_param",
			Data:    nil,
			Error:   "invalid '" + name + "' value: " + err.Error(),
		})
		return false, false
	}

	return value, true
}

func parseRequiredBoolQuery(c *gin.Context, name string) (bool, bool) {
	rawValue := strings.TrimSpace(c.Query(name))
	if rawValue == "" {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "missing_" + name + "_param",
			Data:    nil,
			Error:   "missing '" + name + "' query parameter",
		})
		return false, false
	}

	value, err := strconv.ParseBool(rawValue)
	if err != nil {
		c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
			Status:  "error",
			Message: "invalid_" + name + "_param",
			Data:    nil,
			Error:   "invalid '" + name + "' value: " + err.Error(),
		})
		return false, false
	}

	return value, true
}

func classifyUpdateVMNameError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "failed_to_update_vm_name"
	}

	errText := strings.ToLower(err.Error())

	switch {
	case strings.Contains(errText, "vm_not_found"):
		return http.StatusNotFound, "vm_not_found"
	case strings.Contains(errText, "replication_lease_not_owned"):
		return http.StatusForbidden, "replication_lease_not_owned"
	}

	code := extractVMCreateErrorCode(errText)
	switch code {
	case "invalid_vm_name":
		return http.StatusBadRequest, code
	case "vm_name_already_in_use":
		return http.StatusConflict, code
	case "replication_lease_check_failed":
		return http.StatusInternalServerError, code
	}

	if strings.HasPrefix(code, "invalid_") {
		return http.StatusBadRequest, code
	}
	if code != "" {
		return http.StatusInternalServerError, code
	}

	return http.StatusInternalServerError, "failed_to_update_vm_name"
}

// @Summary Get a Virtual Machine by RID
// @Description Retrieve a virtual machine by its resource ID (RID)
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Success 200 {object} internal.APIResponse[vmModels.VM] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid} [get]
func GetVMByRID(libvirtService vmDetailService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := parseVMRID(c)
		if !ok {
			return
		}

		vm, err := libvirtService.GetVMByRID(rid)
		if err != nil {
			if isVMNotFoundError(err) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "vm_not_found",
					Data:    nil,
					Error:   "vm_not_found",
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_vm",
				Data:    nil,
				Error:   "failed_to_get_vm: " + err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[vmModels.VM]{
			Status:  "success",
			Message: "vm_retrieved",
			Data:    vm,
			Error:   "",
		})
	}
}

// @Summary List all Virtual Machines
// @Description Retrieve a list of all virtual machines
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]vmModels.VM] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm [get]
func ListVMs(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		vms, err := libvirtService.ListVMs()
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_vms",
				Data:    nil,
				Error:   "failed_to_list_vms: " + err.Error(),
			})
			return
		}

		for i := range vms {
			if vms[i].PCIDevices == nil {
				vms[i].PCIDevices = []int{}
			}
			if vms[i].CPUPinning == nil {
				vms[i].CPUPinning = []vmModels.VMCPUPinning{}
			}
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]vmModels.VM]{
			Status:  "success",
			Message: "vm_listed",
			Data:    vms,
			Error:   "",
		})
	}
}

// @Summary Get a Virtual Machine's Domain
// @Description Retrieve the domain information of a virtual machine by its RID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID"
// @Success 200 {object} internal.APIResponse[libvirtServiceInterfaces.LvDomain] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/domain [get]
func GetLvDomain(libvirtService vmDomainService, lifecycleService vmDomainLifecycleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid",
				Data:    nil,
				Error:   "rid is required",
			})
			return
		}

		ridInt, err := strconv.ParseUint(rid, 10, 32)
		if err != nil || ridInt == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid_format",
				Error:   "rid must be a positive integer",
				Data:    nil,
			})
			return
		}

		vmID, err := libvirtService.GetVMIDByRID(uint(ridInt))
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_vm_domain_registration",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		if vmID == 0 {
			c.JSON(http.StatusNotFound, internal.APIResponse[any]{
				Status:  "error",
				Message: "vm_not_found",
				Error:   "vm_not_found",
				Data:    nil,
			})
			return
		}

		domain, err := libvirtService.GetLvDomain(uint(ridInt))
		if err != nil {
			if isLibvirtDomainAbsent(err) {
				orphanDomain := &libvirtServiceInterfaces.LvDomain{
					ID:     -1,
					Name:   strconv.FormatUint(ridInt, 10),
					Status: "orphan",
				}

				if err := applyVMDomainLifecycleState(orphanDomain, lifecycleService, uint(ridInt)); err != nil {
					writeVMDomainLifecycleError(c, err)
					return
				}

				c.JSON(http.StatusOK, internal.APIResponse[*libvirtServiceInterfaces.LvDomain]{
					Status:  "success",
					Message: "vm_domain_orphaned",
					Data:    orphanDomain,
					Error:   "",
				})
				return
			}

			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "libvirt_connection_unavailable",
				Error:   err.Error(),
				Data:    nil,
			})
			return
		}
		if domain == nil {
			c.JSON(http.StatusServiceUnavailable, internal.APIResponse[any]{
				Status:  "error",
				Message: "libvirt_connection_unavailable",
				Error:   "vm_domain_unavailable",
				Data:    nil,
			})
			return
		}

		if err := applyVMDomainLifecycleState(domain, lifecycleService, uint(ridInt)); err != nil {
			writeVMDomainLifecycleError(c, err)
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[*libvirtServiceInterfaces.LvDomain]{
			Status:  "success",
			Message: "vm_domain_retrieved",
			Data:    domain,
			Error:   "",
		})
	}
}

func applyVMDomainLifecycleState(
	domain *libvirtServiceInterfaces.LvDomain,
	lifecycleService vmDomainLifecycleService,
	rid uint,
) error {
	activeTask, err := lifecycleService.GetActiveTaskForGuest("vm", rid)
	if err != nil {
		return err
	}
	if activeTask != nil {
		domain.PendingAction = activeTask.Action
		domain.OverrideRequested = activeTask.OverrideRequested
	}
	return nil
}

func writeVMDomainLifecycleError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
		Status:  "error",
		Message: "failed_to_get_vm_lifecycle_state",
		Error:   err.Error(),
		Data:    nil,
	})
}

// @Summary Create a new Virtual Machine
// @Description Create a new virtual machine with the specified parameters
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body libvirtServiceInterfaces.CreateVMRequest true "Create Virtual Machine Request"
// @Success 201 {object} internal.APIResponse[VMCreateResponse] "Created"
// @Header 201 {string} Location "/api/vm/{rid}"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm [post]
func CreateVM(libvirtService vmCreateService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req libvirtServiceInterfaces.CreateVMRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		ctx := c.Request.Context()
		err := libvirtService.CreateVM(req, ctx)

		if err != nil {
			statusCode, errorCode := classifyCreateVMError(err)

			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: errorCode,
				Data:    nil,
				Error:   "failed_to_create: " + err.Error(),
			})
			return
		}

		created := VMCreateResponse{
			RID:  *req.RID,
			Name: req.Name,
		}
		c.Header("Location", "/api/vm/"+strconv.FormatUint(uint64(created.RID), 10))
		c.JSON(http.StatusCreated, internal.APIResponse[VMCreateResponse]{
			Status:  "success",
			Message: "vm_created",
			Data:    created,
			Error:   "",
		})
	}
}

// @Summary Remove a Virtual Machine
// @Description Remove a virtual machine by RID. Set force=true for best-effort forced removal; normal removal requires all three cleanup flags.
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param deletemacs query bool false "Delete unused VM MAC objects (required for normal removal; defaults to true for forced removal)"
// @Param deleterawdisks query bool false "Delete raw-disk datasets (required for normal removal)"
// @Param deletevolumes query bool false "Delete volume datasets (required for normal removal)"
// @Param force query bool false "Use best-effort forced removal" default(false)
// @Success 200 {object} internal.APIResponse[libvirt.VMRemovalResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid} [delete]
func RemoveVM(libvirtService vmRemovalService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := parseVMRID(c)
		if !ok {
			return
		}

		forceDelete, ok := parseOptionalBoolQuery(c, "force", false)
		if !ok {
			return
		}

		if forceDelete {
			deleteMacs, valid := parseOptionalBoolQuery(c, "deletemacs", true)
			if !valid {
				return
			}

			warnings, removeErr := libvirtService.ForceRemoveVM(rid, deleteMacs, c.Request.Context())
			if removeErr != nil {
				statusCode, errorCode := classifyRemoveVMError(removeErr, "failed_to_force_remove_vm")
				c.JSON(statusCode, internal.APIResponse[any]{
					Status:  "error",
					Message: errorCode,
					Data:    nil,
					Error:   "failed_to_force_remove_vm: " + removeErr.Error(),
				})
				return
			}

			message := "vm_force_removed"
			if len(warnings) > 0 {
				message = "vm_force_removed_with_warnings"
			}
			c.JSON(http.StatusOK, internal.APIResponse[libvirt.VMRemovalResult]{
				Status:  "success",
				Message: message,
				Data: libvirt.VMRemovalResult{
					Warnings:         warnings,
					RetainedDatasets: []string{},
				},
				Error: "",
			})
			return
		}

		deleteMacs, ok := parseRequiredBoolQuery(c, "deletemacs")
		if !ok {
			return
		}
		deleteRawDisks, ok := parseRequiredBoolQuery(c, "deleterawdisks")
		if !ok {
			return
		}
		deleteVolumes, ok := parseRequiredBoolQuery(c, "deletevolumes")
		if !ok {
			return
		}

		removalResult, removeErr := libvirtService.RemoveVMWithWarnings(
			rid,
			deleteMacs,
			deleteRawDisks,
			deleteVolumes,
			c.Request.Context(),
		)
		if removeErr != nil {
			statusCode, errorCode := classifyRemoveVMError(removeErr, "failed_to_remove_vm")
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: errorCode,
				Data:    nil,
				Error:   "failed_to_remove_vm: " + removeErr.Error(),
			})
			return
		}

		message := "vm_removed"
		if len(removalResult.Warnings) > 0 {
			message = "vm_removed_with_warnings"
		}
		c.JSON(http.StatusOK, internal.APIResponse[libvirt.VMRemovalResult]{
			Status:  "success",
			Message: message,
			Data:    removalResult,
			Error:   "",
		})
	}
}

// @Summary Purge an orphaned Virtual Machine registration
// @Description Remove stale VM registration and runtime metadata by RID while preserving datasets
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param deletemacs query bool false "Delete unused VM MAC objects" default(true)
// @Success 200 {object} internal.APIResponse[libvirt.VMRemovalResult] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/registration [delete]
func PurgeVMRegistration(libvirtService vmRemovalService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := parseVMRID(c)
		if !ok {
			return
		}
		deleteMacs, ok := parseOptionalBoolQuery(c, "deletemacs", true)
		if !ok {
			return
		}

		warnings, removeErr := libvirtService.PurgeVMRegistration(rid, deleteMacs)
		if removeErr != nil {
			statusCode, errorCode := classifyRemoveVMError(removeErr, "failed_to_purge_vm_registration")
			errorMessage := "failed_to_purge_vm_registration: " + removeErr.Error()
			if errorCode == "vm_not_orphaned" {
				errorMessage = "vm_not_orphaned: this VM still has a live definition on its node; use Delete instead of removing a stale entry"
			}
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: errorCode,
				Data:    nil,
				Error:   errorMessage,
			})
			return
		}

		message := "vm_registration_purged"
		if len(warnings) > 0 {
			message = "vm_registration_purged_with_warnings"
		}
		c.JSON(http.StatusOK, internal.APIResponse[libvirt.VMRemovalResult]{
			Status:  "success",
			Message: message,
			Data: libvirt.VMRemovalResult{
				Warnings:         warnings,
				RetainedDatasets: []string{},
			},
			Error: "",
		})
	}
}

// @Summary Queue a Virtual Machine lifecycle action
// @Description Queue a start, stop, shutdown, or reboot action for a virtual machine by its RID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param action path string true "Lifecycle action" Enums(start,stop,shutdown,reboot)
// @Success 202 {object} internal.APIResponse[VMActionResponse] "Accepted"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid}/actions/{action} [post]
func VMActionHandler(
	vmService vmActionPreflightService,
	lifecycleService vmLifecycleRequestService,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := strings.TrimSpace(c.Param("rid"))
		action := strings.ToLower(strings.TrimSpace(c.Param("action")))

		if rid == "" || action == "" {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "Virtual Machine RID and action are required",
			})
			return
		}

		ridInt, err := strconv.ParseUint(rid, 10, 0)
		if err != nil || ridInt == 0 {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid_format",
				Data:    nil,
				Error:   "Virtual Machine RID must be a positive integer",
			})
			return
		}

		switch action {
		case "start", "stop", "shutdown", "reboot":
		default:
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_action",
				Data:    nil,
				Error:   "Action must be one of start, stop, shutdown, or reboot",
			})
			return
		}

		ridUint := uint(ridInt)
		if _, err := vmService.GetVMByRID(ridUint); err != nil {
			if isVMNotFoundError(err) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status: "error", Message: "vm_not_found", Data: nil, Error: err.Error(),
				})
				return
			}
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "failed_to_find_vm", Data: nil, Error: err.Error(),
			})
			return
		}

		allowed, err := vmService.CanPerformVMAction(ridUint, action)
		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status: "error", Message: "replication_lease_check_failed", Data: nil, Error: err.Error(),
			})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, internal.APIResponse[any]{
				Status:  "error",
				Message: "replication_lease_not_owned",
				Data:    nil,
				Error:   "This node does not own the right to perform this VM action",
			})
			return
		}

		username := strings.TrimSpace(c.GetString("Username"))

		task, outcome, err := lifecycleService.RequestAction(
			c.Request.Context(),
			taskModels.GuestTypeVM,
			ridUint,
			action,
			taskModels.LifecycleTaskSourceUser,
			username,
		)

		if err != nil {
			if errors.Is(err, lifecycle.ErrTaskInProgress) || errors.Is(err, lifecycle.ErrMigrationActive) {
				message := "lifecycle_task_in_progress"
				if errors.Is(err, lifecycle.ErrMigrationActive) {
					message = "migration_in_progress"
				}
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: message,
					Data:    nil,
					Error:   err.Error(),
				})
				return
			}

			if errors.Is(err, lifecycle.ErrInvalidAction) || errors.Is(err, lifecycle.ErrInvalidGuest) {
				c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_action",
					Data:    nil,
					Error:   err.Error(),
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_enqueue_lifecycle_task",
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}
		if task == nil || task.ID == 0 {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_enqueue_lifecycle_task",
				Data:    nil,
				Error:   "Lifecycle service returned no task",
			})
			return
		}

		c.Set("AuditAsyncJobID", task.ID)
		c.Set("AuditAsyncJobType", "vm_"+action)

		message := "vm_action_queued"
		if outcome == lifecycle.RequestOutcomeForceStopOverride {
			message = "vm_force_stop_requested"
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[VMActionResponse]{
			Status:  "success",
			Message: message,
			Data: VMActionResponse{
				TaskID:  task.ID,
				RID:     ridUint,
				Action:  action,
				Outcome: outcome,
			},
			Error: "",
		})
	}
}

// @Summary Edit a Virtual Machine's description
// @Description Update the description of a virtual machine by its RID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body VMEditDescRequest true "Edit Virtual Machine Description Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid}/description [patch]
func UpdateVMDescription(libvirtService vmDescriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := parseVMRID(c)
		if !ok {
			return
		}

		var req VMEditDescRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		if err := libvirtService.UpdateDescription(rid, req.Description); err != nil {
			statusCode, errorCode := classifyUpdateVMDescriptionError(err)
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: errorCode,
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_description_updated",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Edit a Virtual Machine's name
// @Description Update the name of a virtual machine by its RID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Param request body VMEditNameRequest true "Edit Virtual Machine Name Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{rid}/name [patch]
func UpdateVMName(libvirtService vmNameService, clusterService *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := parseVMRID(c)
		if !ok {
			return
		}

		var req VMEditNameRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		if err := libvirtService.UpdateName(rid, req.Name); err != nil {
			statusCode, errorCode := classifyUpdateVMNameError(err)
			c.JSON(statusCode, internal.APIResponse[any]{
				Status:  "error",
				Message: errorCode,
				Data:    nil,
				Error:   err.Error(),
			})
			return
		}

		if clusterService != nil {
			syncErr := clusterService.SyncBackupJobFriendlySourceByGuestClusterWide(cluster.BackupJobFriendlySourceUpdate{
				GuestType:   clusterModels.ReplicationGuestTypeVM,
				GuestID:     rid,
				FriendlySrc: strings.TrimSpace(req.Name),
			})
			if syncErr != nil {
				logger.L.Warn().
					Err(syncErr).
					Uint("vm_rid", rid).
					Msg("failed_to_sync_backup_friendly_source_after_vm_rename")
			}
		}

		c.JSON(http.StatusOK, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_name_updated",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary List all VMs (Simple)
// @Description Retrieve a simple list of all VMs
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]libvirtServiceInterfaces.SimpleList] "Success"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/simple [get]
func ListVMsSimple(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		vms, err := libvirtService.SimpleListVM()

		if err != nil {
			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_list_vms_simple",
				Data:    nil,
				Error:   "failed_to_list_vms_simple: " + err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[[]libvirtServiceInterfaces.SimpleList]{
			Status:  "success",
			Message: "vm_listed_simple",
			Data:    vms,
			Error:   "",
		})
	}
}

// @Summary Get a simple Virtual Machine by RID
// @Description Retrieve a simple virtual machine object by its resource ID (RID)
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID" minimum(1)
// @Success 200 {object} internal.APIResponse[libvirtServiceInterfaces.SimpleList] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/simple/{rid} [get]
func GetSimpleVMByRID(libvirtService vmSimpleDetailService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid, ok := parseVMRID(c)
		if !ok {
			return
		}

		simple, err := libvirtService.GetSimpleVMByRID(rid)
		if err != nil {
			if isVMNotFoundError(err) {
				c.JSON(http.StatusNotFound, internal.APIResponse[any]{
					Status:  "error",
					Message: "vm_not_found",
					Data:    nil,
					Error:   "vm_not_found",
				})
				return
			}

			c.JSON(http.StatusInternalServerError, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_vm",
				Data:    nil,
				Error:   "failed_to_get_vm: " + err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, internal.APIResponse[libvirtServiceInterfaces.SimpleList]{
			Status:  "success",
			Message: "vm_retrieved_simple",
			Data:    simple,
			Error:   "",
		})
	}
}
