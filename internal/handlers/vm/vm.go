// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/libvirt"

	"github.com/gin-gonic/gin"
)

type VMEditDescRequest struct {
	RID         uint   `json:"rid" binding:"required"`
	Description string `json:"description"`
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
			c.JSON(500, internal.APIResponse[any]{Error: "failed_to_create: " + err.Error()})
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
func VMActionHandler(libvirtService *libvirt.Service) gin.HandlerFunc {
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

		err = libvirtService.PerformAction(uint(ridInt), action)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_perform_action",
				Data:    nil,
				Error:   "failed_to_perform_action: " + err.Error(),
			})
			return
		}

		c.JSON(200, internal.APIResponse[any]{
			Status:  "success",
			Message: "action_performed",
			Data:    nil,
			Error:   "",
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
