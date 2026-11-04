// SPDX-License-Identifier: BSD-2-Clause

package containerHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailHandlers "github.com/alchemillahq/sylve/internal/handlers/jail"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/services/container"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"

	_ "github.com/alchemillahq/sylve/internal/db/models/container"

	"github.com/gin-gonic/gin"
)

type lifecycleRequester interface {
	RequestActionWithPayload(ctx context.Context, guestType string, guestID uint, action, source, requestedBy, payload string) (*taskModels.GuestLifecycleTask, string, error)
	RequestContainerStack(ctx context.Context, req containerServiceInterfaces.StackRequest, source, requestedBy string) (containerServiceInterfaces.StackRequest, *taskModels.GuestLifecycleTask, string, error)
}

type TaskResponse struct {
	TaskID  uint   `json:"taskId"`
	OCID    uint   `json:"ocId"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
}

func respond(c *gin.Context, status int, message string, data any) {
	c.JSON(status, internal.APIResponse[any]{Status: "success", Message: message, Data: data})
}

func fail(c *gin.Context, status int, message string, err error) {
	c.JSON(status, internal.APIResponse[any]{Status: "error", Message: message, Error: err.Error()})
}

func failService(c *gin.Context, message string, err error) {
	if errors.Is(err, container.ErrNotFound) {
		fail(c, http.StatusNotFound, "container_not_found", err)
		return
	}
	fail(c, http.StatusInternalServerError, message, err)
}

func parseOCID(c *gin.Context) (uint, bool) {
	ocid, err := strconv.ParseUint(c.Param("ocid"), 10, 32)
	if err != nil || ocid == 0 {
		fail(c, http.StatusBadRequest, "invalid_ocid", fmt.Errorf("container ID must be a positive integer"))
		return 0, false
	}
	return uint(ocid), true
}

func parseID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		fail(c, http.StatusBadRequest, "invalid_id", fmt.Errorf("ID must be a positive integer"))
		return 0, false
	}
	return uint(id), true
}

func queueTask(c *gin.Context, lifecycleService lifecycleRequester, ocid uint, action string, payload any) {
	encoded := ""
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_request_data", err)
			return
		}
		encoded = string(data)
	}
	task, outcome, err := lifecycleService.RequestActionWithPayload(c.Request.Context(), taskModels.GuestTypeContainer, ocid, action,
		taskModels.LifecycleTaskSourceUser, strings.TrimSpace(c.GetString("Username")), encoded)
	switch {
	case errors.Is(err, lifecycle.ErrTaskInProgress), errors.Is(err, lifecycle.ErrMigrationActive):
		fail(c, http.StatusConflict, "lifecycle_task_in_progress", err)
		return
	case errors.Is(err, container.ErrNotFound):
		fail(c, http.StatusNotFound, "container_not_found", err)
		return
	case err != nil:
		fail(c, http.StatusBadRequest, "container_"+action+"_rejected", err)
		return
	}
	c.Set("AuditAsyncJobID", task.ID)
	c.Set("AuditAsyncJobType", "container_"+action)
	respond(c, http.StatusAccepted, "container_"+action+"_queued", TaskResponse{TaskID: task.ID, OCID: ocid, Action: action, Outcome: outcome})
}

// @Summary List containers
// @Description List containers with their live state
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]containerServiceInterfaces.ListItem] "Success"
// @Router /container [get]
func ListContainers(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := service.List(c.Request.Context())
		if err != nil {
			failService(c, "failed_to_list_containers", err)
			return
		}
		respond(c, http.StatusOK, "containers_listed", items)
	}
}

// @Summary List containers in brief
// @Description List each container's ID, name and live state for the resource tree
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Success 200 {object} internal.APIResponse[[]containerServiceInterfaces.SimpleList] "Success"
// @Router /container/simple [get]
func ListContainersSimple(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := service.List(c.Request.Context())
		if err != nil {
			failService(c, "failed_to_list_containers", err)
			return
		}
		simple := make([]containerServiceInterfaces.SimpleList, len(items))
		for i, item := range items {
			simple[i] = containerServiceInterfaces.SimpleList{OCID: item.OCID, Name: item.Name, Stack: item.Stack, State: item.State}
		}
		respond(c, http.StatusOK, "containers_listed", simple)
	}
}

// @Summary Get a container
// @Description Get a container's configuration and live Podman state
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param ocid path int true "Container ID"
// @Success 200 {object} internal.APIResponse[containerServiceInterfaces.Detail] "Success"
// @Router /container/{ocid} [get]
func GetContainer(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, ok := parseOCID(c)
		if !ok {
			return
		}
		detail, err := service.Get(c.Request.Context(), ocid)
		if err != nil {
			failService(c, "failed_to_get_container", err)
			return
		}
		respond(c, http.StatusOK, "container_retrieved", detail)
	}
}

// @Summary Get a container's image
// @Description Get details of the image a container was created from
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param ocid path int true "Container ID"
// @Success 200 {object} internal.APIResponse[containerServiceInterfaces.ImageInfo] "Success"
// @Router /container/{ocid}/image [get]
func GetContainerImage(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, ok := parseOCID(c)
		if !ok {
			return
		}
		info, err := service.ImageInfo(c.Request.Context(), ocid)
		if err != nil {
			failService(c, "failed_to_get_container_image", err)
			return
		}
		respond(c, http.StatusOK, "container_image_retrieved", info)
	}
}

// @Summary Create a container
// @Description Queue the creation of a container; the image is pulled if missing
// @Tags Container
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body containerServiceInterfaces.CreateRequest true "Container"
// @Success 202 {object} internal.APIResponse[TaskResponse] "Accepted"
// @Router /container [post]
func CreateContainer(lifecycleService lifecycleRequester) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req containerServiceInterfaces.CreateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, "invalid_request_data", err)
			return
		}
		queueTask(c, lifecycleService, req.OCID, "create", req)
	}
}

// @Summary Update a container
// @Description Queue recreation of a container from an edited spec, keeping its volumes
// @Tags Container
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ocid path int true "Container ID"
// @Param request body containerServiceInterfaces.Spec true "Spec"
// @Success 202 {object} internal.APIResponse[TaskResponse] "Accepted"
// @Router /container/{ocid} [put]
func UpdateContainer(lifecycleService lifecycleRequester) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, ok := parseOCID(c)
		if !ok {
			return
		}
		var spec containerServiceInterfaces.Spec
		if err := c.ShouldBindJSON(&spec); err != nil {
			fail(c, http.StatusBadRequest, "invalid_request_data", err)
			return
		}
		queueTask(c, lifecycleService, ocid, "update", spec)
	}
}

// @Summary Delete a container
// @Description Queue deletion of a container; volumes are kept unless deleteVolumes is true
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param ocid path int true "Container ID"
// @Param deleteVolumes query bool false "Destroy the container's volumes"
// @Success 202 {object} internal.APIResponse[TaskResponse] "Accepted"
// @Router /container/{ocid} [delete]
func DeleteContainer(lifecycleService lifecycleRequester) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, ok := parseOCID(c)
		if !ok {
			return
		}
		deleteVolumes, err := strconv.ParseBool(c.DefaultQuery("deleteVolumes", "false"))
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_delete_volumes", err)
			return
		}
		queueTask(c, lifecycleService, ocid, "delete", containerServiceInterfaces.DeleteRequest{DeleteVolumes: deleteVolumes})
	}
}

// @Summary Queue a container lifecycle action
// @Description Queue a start, stop, restart, or kill action for a container
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param ocid path int true "Container ID"
// @Param action path string true "Lifecycle action" Enums(start,stop,restart,kill)
// @Success 202 {object} internal.APIResponse[TaskResponse] "Accepted"
// @Router /container/{ocid}/actions/{action} [post]
func ContainerAction(lifecycleService lifecycleRequester) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, ok := parseOCID(c)
		if !ok {
			return
		}
		action := strings.ToLower(c.Param("action"))
		if !slices.Contains([]string{"start", "stop", "restart", "kill"}, action) {
			fail(c, http.StatusBadRequest, "invalid_action", fmt.Errorf("action must be one of start, stop, restart, or kill"))
			return
		}
		queueTask(c, lifecycleService, ocid, action, nil)
	}
}

// @Summary Update container options
// @Description Change a container's name, description, or boot options without recreating it
// @Tags Container
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ocid path int true "Container ID"
// @Param request body containerServiceInterfaces.OptionsRequest true "Options"
// @Success 200 {object} internal.APIResponse[any] "Success"
// @Router /container/{ocid}/options [patch]
func UpdateContainerOptions(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, ok := parseOCID(c)
		if !ok {
			return
		}
		var req containerServiceInterfaces.OptionsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, "invalid_request_data", err)
			return
		}
		if err := service.UpdateOptions(c.Request.Context(), ocid, req); err != nil {
			failService(c, "failed_to_update_container_options", err)
			return
		}
		respond(c, http.StatusOK, "container_options_updated", nil)
	}
}

// @Summary Get container logs
// @Description Get the last lines of a container's output
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param ocid path int true "Container ID"
// @Param tail query int false "Number of lines" default(500)
// @Success 200 {object} internal.APIResponse[string] "Success"
// @Router /container/{ocid}/logs [get]
func GetContainerLogs(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, ok := parseOCID(c)
		if !ok {
			return
		}
		tail, err := strconv.ParseUint(c.DefaultQuery("tail", "500"), 10, 32)
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_tail", err)
			return
		}
		logs, err := service.Logs(c.Request.Context(), ocid, uint(tail))
		if err != nil {
			failService(c, "failed_to_get_container_logs", err)
			return
		}
		respond(c, http.StatusOK, "container_logs_retrieved", logs)
	}
}

// @Summary Open a container console WebSocket
// @Description Open an interactive shell in a running container
// @Tags Container
// @Security BearerAuth
// @Param ocid path int true "Container ID"
// @Param shell query string false "Shell to run" default(/bin/sh)
// @Router /container/{ocid}/console [get]
func HandleContainerConsole(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, ok := parseOCID(c)
		if !ok {
			return
		}
		shell := c.DefaultQuery("shell", "/bin/sh")
		if _, err := service.Command(c.Request.Context(), ocid); err != nil {
			failService(c, "container_console_unavailable", err)
			return
		}
		jailHandlers.ServeTerminal(c, fmt.Sprintf("container-%d-%s", ocid, shell), func(sm *jailHandlers.SessionManager, sessionID string) (*jailHandlers.TerminalSession, error) {
			return sm.GetOrCreateTerminalSession(sessionID, func(rows, cols uint16) (jailHandlers.Terminal, error) {
				return service.Console(context.Background(), ocid, shell, rows, cols)
			})
		})
	}
}

// @Summary Get initial container statistics
// @Description Get the finest available CPU and memory usage window for a container
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param ocid path int true "Container ID"
// @Success 200 {object} internal.APIResponse[db.StatsBootstrap[containerModels.Stats]] "Success"
// @Router /container/{ocid}/stats [get]
func GetContainerStatsBootstrap(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, ok := parseOCID(c)
		if !ok {
			return
		}
		stats, err := service.UsageBootstrap(c.Request.Context(), ocid)
		if err != nil {
			failService(c, "failed_to_get_container_stats", err)
			return
		}
		respond(c, http.StatusOK, "container_stats_retrieved", stats)
	}
}

// @Summary Get container statistics
// @Description Get CPU and memory usage for a container over a window
// @Tags Container
// @Produce json
// @Security BearerAuth
// @Param ocid path int true "Container ID"
// @Param step path string true "Window step"
// @Success 200 {object} internal.APIResponse[[]containerModels.Stats] "Success"
// @Router /container/{ocid}/stats/{step} [get]
func GetContainerStats(service containerServiceInterfaces.ContainerServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		ocid, ok := parseOCID(c)
		if !ok {
			return
		}
		step := db.GFSStep(c.Param("step"))
		if _, err := step.Window(); err != nil {
			fail(c, http.StatusBadRequest, "invalid_stats_step", err)
			return
		}
		stats, err := service.Usage(c.Request.Context(), ocid, step)
		if err != nil {
			failService(c, "failed_to_get_container_stats", err)
			return
		}
		respond(c, http.StatusOK, "container_stats_retrieved", stats)
	}
}
