// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
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
)

type VMEditDescRequest struct {
	RID         uint   `json:"rid" binding:"required"`
	Description string `json:"description"`
}

type VMEditNameRequest struct {
	RID  uint   `json:"rid" binding:"required"`
	Name string `json:"name" binding:"required"`
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
	"invalid_mac_object_type":                          {},
	"invalid_rid":                                      {},
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
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "vm_not_found")
}

func isVMDomainNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "failed_to_lookup_domain")
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
		return http.StatusInternalServerError, "vm_create_dependency_not_ready"
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

// @Summary Get a Virtual Machine by RID or ID
// @Description Retrieve a virtual machine by its RID or ID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path string true "Virtual Machine RID or ID"
// @Param type query string false "Type of identifier (rid or id)"  Enums(rid, id) default(rid)
// @Success 200 {object} internal.APIResponse[vmModels.VM] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/:id [get]
func GetVMByIdentifier(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		vmID := c.Param("id")
		if vmID == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_vm_id",
				Data:    nil,
				Error:   "Virtual Machine ID is required",
			})
			return
		}

		var t string = c.DefaultQuery("type", "rid")
		if t != "rid" && t != "id" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_type_param",
				Data:    nil,
				Error:   "Type parameter must be either 'rid' or 'id'",
			})
			return
		}

		identifier, err := strconv.Atoi(vmID)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_vm_id_format",
				Data:    nil,
				Error:   "Virtual Machine ID must be a valid integer",
			})
			return
		}

		var vm vmModels.VM
		if t == "rid" {
			vm, err = libvirtService.GetVMByRID(uint(identifier))
		} else {
			vm, err = libvirtService.GetVM(identifier)
		}

		if err != nil || vm.ID == 0 {
			if isVMNotFoundError(err) || vm.ID == 0 {
				c.JSON(404, internal.APIResponse[any]{
					Status:  "error",
					Message: "vm_not_found",
					Data:    nil,
					Error:   "vm_not_found",
				})
				return
			}

			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_vm",
				Data:    nil,
				Error:   "failed_to_get_vm: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[vmModels.VM]{
			Status:  "success",
			Message: "vm_retrieved_by_vmid",
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
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm [get]
func ListVMs(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		vms, err := libvirtService.ListVMs()

		for i := range vms {
			if vms[i].PCIDevices == nil {
				vms[i].PCIDevices = []int{}
			}
			if vms[i].CPUPinning == nil {
				vms[i].CPUPinning = []vmModels.VMCPUPinning{
					{
						HostSocket: 0,
						HostCPU:    []int{},
					},
				}
			}
		}

		if err != nil {
			c.JSON(500, internal.APIResponse[any]{Error: "failed_to_list_vms: " + err.Error()})
			return
		}

		c.JSON(200, internal.APIResponse[[]vmModels.VM]{
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
// @Param rid path string true "Virtual Machine RID"
// @Success 200 {object} internal.APIResponse[libvirtServiceInterfaces.LvDomain] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/domain/:rid [get]
func GetLvDomain(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		if rid == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid",
				Data:    nil,
				Error:   "Virtual Machine ID is required",
			})
			return
		}

		ridInt, err := strconv.ParseUint(rid, 10, 0)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid_format",
				Error:   "Virtual Machine RID must be a valid integer",
				Data:    nil,
			})
			return
		}

		domain, err := libvirtService.GetLvDomain(uint(ridInt))
		if err != nil {
			if isVMDomainNotFoundError(err) {
				c.JSON(404, internal.APIResponse[any]{
					Status:  "error",
					Message: "vm_domain_not_found",
					Error:   "vm_domain_not_found",
					Data:    nil,
				})
				return
			}

			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_domain",
				Error:   "failed_to_get_domain: " + err.Error(),
				Data:    nil,
			})
			return
		}

		c.JSON(200, internal.APIResponse[*libvirtServiceInterfaces.LvDomain]{
			Status:  "success",
			Message: "vm_domain_retrieved",
			Data:    domain,
			Error:   "",
		})
	}
}

// @Summary Create a new Virtual Machine
// @Description Create a new virtual machine with the specified parameters
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body libvirtServiceInterfaces.CreateVMRequest true "Create Virtual Machine Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm [post]
func CreateVM(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req libvirtServiceInterfaces.CreateVMRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
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

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_created",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Remove a Virtual Machine
// @Description Remove a virtual machine by its ID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Virtual Machine ID"
// @Param deletemacs query bool true "Delete or Keep"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{id} [delete]
func RemoveVM(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		vmID := c.Param("id")
		if vmID == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_vm_id",
				Data:    nil,
				Error:   "Virtual Machine ID is required",
			})
			return
		}

		vmInt, err := strconv.Atoi(vmID)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_vm_id_format",
				Data:    nil,
				Error:   "Virtual Machine ID must be a valid integer",
			})
			return
		}

		forceDelete := false
		forceDeleteStr := strings.TrimSpace(c.DefaultQuery("force", "false"))
		if forceDeleteStr != "" {
			forceDelete, err = strconv.ParseBool(forceDeleteStr)
			if err != nil {
				c.JSON(400, internal.APIResponse[any]{
					Status:  "error",
					Message: "invalid_force_param",
					Error:   "invalid 'force' value: " + err.Error(),
					Data:    nil,
				})
				return
			}
		}

		if forceDelete {
			deleteMacs := true
			deleteMacsStr := strings.TrimSpace(c.Query("deletemacs"))
			if deleteMacsStr != "" {
				parsedDeleteMacs, parseErr := strconv.ParseBool(deleteMacsStr)
				if parseErr != nil {
					c.JSON(400, internal.APIResponse[any]{
						Status:  "error",
						Message: "invalid_deletemacs_param",
						Error:   "invalid 'deletemacs' value: " + parseErr.Error(),
						Data:    nil,
					})
					return
				}
				deleteMacs = parsedDeleteMacs
			}

			ctx := c.Request.Context()
			warnings, removeErr := libvirtService.ForceRemoveVM(uint(vmInt), deleteMacs, ctx)
			if removeErr != nil {
				if isVMNotFoundError(removeErr) {
					c.JSON(404, internal.APIResponse[any]{
						Status:  "error",
						Message: "vm_not_found",
						Data:    nil,
						Error:   "vm_not_found",
					})
					return
				}

				c.JSON(500, internal.APIResponse[any]{
					Status:  "error",
					Message: "failed_to_force_remove_vm",
					Data:    nil,
					Error:   "failed_to_force_remove_vm: " + removeErr.Error(),
				})
				return
			}

			message := "vm_force_removed"
			if len(warnings) > 0 {
				message = "vm_force_removed_with_warnings"
			}

			c.JSON(200, internal.APIResponse[map[string]any]{
				Status:  "success",
				Message: message,
				Data: map[string]any{
					"warnings": warnings,
				},
				Error: "",
			})
			return
		}

		deleteMacsStr := c.Query("deletemacs")
		if deleteMacsStr == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_deletemacs_param",
				Error:   "missing 'deletemacs' query parameter",
				Data:    nil,
			})
			return
		}

		deleteMacs, err := strconv.ParseBool(deleteMacsStr)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_deletemacs_param",
				Error:   "invalid 'deletemacs' value: " + err.Error(),
				Data:    nil,
			})
			return
		}

		deleteRawDisksStr := c.Query("deleterawdisks")
		if deleteRawDisksStr == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_deleterawdisks_param",
				Error:   "missing 'deleterawdisks' query parameter",
				Data:    nil,
			})
			return
		}

		deleteRawDisks, err := strconv.ParseBool(deleteRawDisksStr)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_deleterawdisks_param",
				Error:   "invalid 'deleterawdisks' value: " + err.Error(),
				Data:    nil,
			})
			return
		}

		deleteVolumesStr := c.Query("deletevolumes")
		if deleteVolumesStr == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "missing_deletevolumes_param",
				Error:   "missing 'deletevolumes' query parameter",
				Data:    nil,
			})
			return
		}

		deleteVolumes, err := strconv.ParseBool(deleteVolumesStr)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_deletevolumes_param",
				Error:   "invalid 'deletevolumes' value: " + err.Error(),
				Data:    nil,
			})
			return
		}

		ctx := c.Request.Context()
		err = libvirtService.RemoveVM(uint(vmInt), deleteMacs, deleteRawDisks, deleteVolumes, ctx)

		if err != nil {
			if isVMNotFoundError(err) {
				c.JSON(404, internal.APIResponse[any]{
					Status:  "error",
					Message: "vm_not_found",
					Data:    nil,
					Error:   "vm_not_found",
				})
				return
			}

			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_remove_vm",
				Data:    nil,
				Error:   "failed_to_remove: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "vm_removed",
			Data:    nil,
			Error:   "",
		})
	}
}

// @Summary Perform an action on a Virtual Machine
// @Description Perform a specified action (start, stop, reboot) on a virtual machine by its RID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param rid path string true "Virtual Machine RID"
// @Param action path string true "Action to perform (start, stop, reboot)"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{action}/:rid [post]
func VMActionHandler(lifecycleService *lifecycle.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.Param("rid")
		action := c.Param("action")

		if rid == "" || action == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "Virtual Machine ID and action are required",
			})
			return
		}

		ridInt, err := strconv.ParseUint(rid, 10, 0)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_rid_format",
				Data:    nil,
				Error:   "Virtual Machine ID must be a valid integer",
			})
			return
		}

		username := strings.TrimSpace(c.GetString("Username"))

		_, outcome, err := lifecycleService.RequestAction(
			c.Request.Context(),
			taskModels.GuestTypeVM,
			uint(ridInt),
			action,
			taskModels.LifecycleTaskSourceUser,
			username,
		)

		if err != nil {
			if errors.Is(err, lifecycle.ErrTaskInProgress) {
				c.JSON(http.StatusConflict, internal.APIResponse[any]{
					Status:  "error",
					Message: "lifecycle_task_in_progress",
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

		message := "vm_action_queued"
		if outcome == lifecycle.RequestOutcomeForceStopOverride {
			message = "vm_force_stop_requested"
		}

		c.JSON(http.StatusAccepted, internal.APIResponse[any]{
			Status:  "success",
			Message: message,
			Data: map[string]any{
				"outcome": outcome,
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
// @Param request body VMEditDescRequest true "Edit Virtual Machine Description Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Router /vm/description [put]
func UpdateVMDescription(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req VMEditDescRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request_data",
				Data:    nil,
				Error:   "Invalid request data: " + err.Error(),
			})
			return
		}

		err := libvirtService.UpdateDescription(req.RID, req.Description)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_update_description",
				Data:    nil,
				Error:   "failed_to_update_description: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
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
// @Param request body VMEditNameRequest true "Edit Virtual Machine Name Request"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Router /vm/name [put]
func UpdateVMName(libvirtService *libvirt.Service, clusterService *cluster.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		if err := libvirtService.UpdateName(req.RID, req.Name); err != nil {
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
				GuestID:     req.RID,
				FriendlySrc: strings.TrimSpace(req.Name),
			})
			if syncErr != nil {
				logger.L.Warn().
					Err(syncErr).
					Uint("vm_rid", req.RID).
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
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/simple [get]
func ListVMsSimple(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		vms, err := libvirtService.SimpleListVM()

		if err != nil {
			c.JSON(500, internal.APIResponse[any]{Error: "failed_to_list_jails_simple: " + err.Error()})
			return
		}

		c.JSON(200, internal.APIResponse[[]libvirtServiceInterfaces.SimpleList]{
			Status:  "success",
			Message: "vm_listed_simple",
			Data:    vms,
			Error:   "",
		})
	}
}

// @Summary Get a simple Virtual Machine by RID or ID
// @Description Retrieve a simple virtual machine object by its RID or ID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Virtual Machine RID or ID"
// @Param type query string false "Type of identifier (rid or id)" Enums(rid, id) default(rid)
// @Success 200 {object} internal.APIResponse[libvirtServiceInterfaces.SimpleList] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/simple/:id [get]
func GetSimpleVMByIdentifier(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		vmID := c.Param("id")
		if vmID == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_vm_id",
				Data:    nil,
				Error:   "Virtual Machine ID is required",
			})
			return
		}

		t := c.DefaultQuery("type", "rid")
		if t != "rid" && t != "id" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_type_param",
				Data:    nil,
				Error:   "Type parameter must be either 'rid' or 'id'",
			})
			return
		}

		identifier, err := strconv.Atoi(vmID)
		if err != nil {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_vm_id_format",
				Data:    nil,
				Error:   "Virtual Machine ID must be a valid integer",
			})
			return
		}

		simple, err := libvirtService.GetSimpleVM(identifier, t == "rid")
		if err != nil {
			if isVMNotFoundError(err) {
				c.JSON(404, internal.APIResponse[any]{
					Status:  "error",
					Message: "vm_not_found",
					Data:    nil,
					Error:   "vm_not_found",
				})
				return
			}

			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_vm",
				Data:    nil,
				Error:   "failed_to_get_vm: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[libvirtServiceInterfaces.SimpleList]{
			Status:  "success",
			Message: "vm_retrieved_simple_by_vmid",
			Data:    simple,
			Error:   "",
		})
	}
}
