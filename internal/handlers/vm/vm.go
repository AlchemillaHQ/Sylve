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

	"github.com/alchemillahq/sylve/internal"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/libvirt"

	"github.com/gin-gonic/gin"
)

type VMEditDescRequest struct {
	ID          uint   `json:"id" binding:"required"`
	Description string `json:"description" binding:"required"`
}

// @Summary Get a Virtual Machine by VMID or ID
// @Description Retrieve a virtual machine by its VMID or ID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param vmid path string true "Virtual Machine ID or VmId"
// @Param type query string false "Type of identifier (vmid or id)"  Enums(vmid, id) default(vmid)
// @Success 200 {object} internal.APIResponse[vmModels.VM] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{id} [get]
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

		var t string = c.DefaultQuery("type", "vmid")
		if t != "vmid" && t != "id" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_type_param",
				Data:    nil,
				Error:   "Type parameter must be either 'vmid' or 'id'",
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
		if t == "vmid" {
			vm, err = libvirtService.GetVMByVmId(identifier)
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
// @Description Retrieve the domain information of a virtual machine by its ID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Virtual Machine ID"
// @Success 200 {object} internal.APIResponse[libvirtServiceInterfaces.LvDomain] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/domain/{id} [get]
func GetLvDomain(libvirtService *libvirt.Service) gin.HandlerFunc {
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

		domain, err := libvirtService.GetLvDomain(vmInt)
		if err != nil {
			c.JSON(500, internal.APIResponse[any]{
				Status:  "error",
				Message: "failed_to_get_domain",
				Data:    nil,
				Error:   "failed_to_get_domain: " + err.Error(),
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

		err := libvirtService.CreateVM(req)

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

		err = libvirtService.RemoveVM(uint(vmInt), deleteMacs, deleteRawDisks, deleteVolumes)

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
// @Description Perform a specified action (start, stop, reboot) on a virtual machine by its ID
// @Tags VM
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Virtual Machine ID"
// @Param action path string true "Action to perform (start, stop, reboot)"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 404 {object} internal.APIResponse[any] "Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Router /vm/{action}/{id} [post]
func VMActionHandler(libvirtService *libvirt.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		vmID := c.Param("id")
		action := c.Param("action")

		if vmID == "" || action == "" {
			c.JSON(400, internal.APIResponse[any]{
				Status:  "error",
				Message: "invalid_request",
				Data:    nil,
				Error:   "Virtual Machine ID and action are required",
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

		err = libvirtService.PerformAction(uint(vmInt), action)
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
// @Description Update the description of a virtual machine by its ID
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

		err := libvirtService.UpdateDescription(req.ID, req.Description)
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
