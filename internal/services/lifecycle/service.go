// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"gorm.io/gorm"
)

const (
	guestLifecycleExecQueueName = "guest-lifecycle-exec"
	guestAutostartQueueName     = "guest-autostart-sequence"
)

const (
	RequestOutcomeQueued            = "queued"
	RequestOutcomeForceStopOverride = "force_stop_requested"
)

var (
	ErrTaskInProgress = errors.New("lifecycle_task_in_progress")
	ErrInvalidGuest   = errors.New("invalid_guest_type")
	ErrInvalidAction  = errors.New("invalid_action")
)

var errGuestAlreadyRunning = errors.New("guest_already_running")

type guestLifecycleExecPayload struct {
	TaskID uint `json:"taskId"`
}

type Service struct {
	DB      *gorm.DB
	Libvirt *libvirt.Service
	Jail    *jail.Service

	createMu sync.Mutex

	vmActionFn   func(rid uint, action string) error
	vmStateFn    func(rid uint) (int, error)
	jailActionFn func(ctid int, action string) error
	jailActiveFn func(ctid uint) (bool, error)

	jailTemplateConvertFn func(ctx context.Context, ctid uint, req jail.ConvertToTemplateRequest) error
	jailTemplateCreateFn  func(ctx context.Context, templateID uint, req jail.CreateFromTemplateRequest) error

	vmTemplateConvertFn func(ctx context.Context, rid uint, req libvirtServiceInterfaces.ConvertToTemplateRequest) error
	vmTemplateCreateFn  func(ctx context.Context, templateID uint, req libvirtServiceInterfaces.CreateFromTemplateRequest) error
}

func NewService(dbConn *gorm.DB, libvirtService *libvirt.Service, jailService *jail.Service) *Service {
	s := &Service{
		DB:      dbConn,
		Libvirt: libvirtService,
		Jail:    jailService,
	}

	if libvirtService != nil {
		s.vmActionFn = libvirtService.PerformAction
		s.vmStateFn = func(rid uint) (int, error) {
			state, err := libvirtService.GetDomainState(int(rid))
			return int(state), err
		}
		s.vmTemplateConvertFn = libvirtService.ConvertVMToTemplate
		s.vmTemplateCreateFn = libvirtService.CreateVMsFromTemplate
	}

	if jailService != nil {
		s.jailActionFn = jailService.JailAction
		s.jailActiveFn = jailService.IsJailActive
		s.jailTemplateConvertFn = jailService.ConvertJailToTemplate
		s.jailTemplateCreateFn = jailService.CreateJailsFromTemplate
	}

	return s
}

func normalizeGuestType(v string) string {
	return strings.TrimSpace(strings.ToLower(v))
}

func normalizeAction(v string) string {
	return strings.TrimSpace(strings.ToLower(v))
}

func normalizeSource(v string) string {
	source := strings.TrimSpace(strings.ToLower(v))
	if source == "" {
		return taskModels.LifecycleTaskSourceUser
	}
	return source
}

func validateAction(guestType, action string) error {
	switch guestType {
	case taskModels.GuestTypeVM:
		switch action {
		case "start", "stop", "shutdown", "reboot":
			return nil
		default:
			return fmt.Errorf("%w: %s", ErrInvalidAction, action)
		}
	case taskModels.GuestTypeJail:
		switch action {
		case "start", "stop", "restart":
			return nil
		default:
			return fmt.Errorf("%w: %s", ErrInvalidAction, action)
		}
	case taskModels.GuestTypeJailTemplate:
		switch action {
		case "convert", "create":
			return nil
		default:
			return fmt.Errorf("%w: %s", ErrInvalidAction, action)
		}
	case taskModels.GuestTypeVMTemplate:
		switch action {
		case "convert", "create":
			return nil
		default:
			return fmt.Errorf("%w: %s", ErrInvalidAction, action)
		}
	default:
		return fmt.Errorf("%w: %s", ErrInvalidGuest, guestType)
	}
}

func (s *Service) RegisterJobs() {
	db.QueueRegisterJSON[guestLifecycleExecPayload](guestLifecycleExecQueueName, func(ctx context.Context, payload guestLifecycleExecPayload) error {
		if payload.TaskID == 0 {
			logger.L.Warn().Msg("guest_lifecycle_exec_invalid_task_id")
			return nil
		}

		if err := s.ExecuteTask(ctx, payload.TaskID); err != nil {
			logger.L.Warn().Err(err).Uint("task_id", payload.TaskID).Msg("guest_lifecycle_exec_failed")
		}

		// We intentionally do not return execution errors to avoid unsafe lifecycle retries.
		return nil
	})

	db.QueueRegisterNoPayload(guestAutostartQueueName, func(ctx context.Context) error {
		if err := s.runStartupAutostart(ctx); err != nil {
			logger.L.Warn().Err(err).Msg("guest_autostart_sequence_failed")
		}
		return nil
	})
}

func (s *Service) EnqueueStartupAutostart(ctx context.Context) error {
	return db.EnqueueNoPayload(ctx, guestAutostartQueueName)
}

func (s *Service) RequestAction(
	ctx context.Context,
	guestType string,
	guestID uint,
	action string,
	source string,
	requestedBy string,
) (*taskModels.GuestLifecycleTask, string, error) {
	return s.createTask(ctx, guestType, guestID, action, source, requestedBy, "", true)
}

func (s *Service) RequestActionWithPayload(
	ctx context.Context,
	guestType string,
	guestID uint,
	action string,
	source string,
	requestedBy string,
	payload string,
) (*taskModels.GuestLifecycleTask, string, error) {
	return s.createTask(ctx, guestType, guestID, action, source, requestedBy, payload, true)
}

func (s *Service) createTask(
	ctx context.Context,
	guestType string,
	guestID uint,
	action string,
	source string,
	requestedBy string,
	payload string,
	enqueue bool,
) (*taskModels.GuestLifecycleTask, string, error) {
	guestType = normalizeGuestType(guestType)
	action = normalizeAction(action)
	source = normalizeSource(source)
	requestedBy = strings.TrimSpace(requestedBy)

	if guestID == 0 {
		return nil, "", fmt.Errorf("invalid_guest_id")
	}

	if err := validateAction(guestType, action); err != nil {
		return nil, "", err
	}

	s.createMu.Lock()
	defer s.createMu.Unlock()

	active, err := s.GetActiveTaskForGuest(guestType, guestID)
	if err != nil {
		return nil, "", err
	}

	// Mimic the Proxmox-style override flow: stop can interrupt an in-flight shutdown.
	if active != nil {
		if guestType == taskModels.GuestTypeVM && action == "stop" && active.Action == "shutdown" && !active.OverrideRequested {
			now := time.Now().UTC()
			if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", active.ID).Updates(map[string]any{
				"override_requested": true,
				"updated_at":         now,
				"message":            RequestOutcomeForceStopOverride,
			}).Error; err != nil {
				return nil, "", err
			}

			refetched := taskModels.GuestLifecycleTask{}
			if err := s.DB.First(&refetched, active.ID).Error; err != nil {
				return nil, "", err
			}
			return &refetched, RequestOutcomeForceStopOverride, nil
		}

		return active, "", ErrTaskInProgress
	}

	task := &taskModels.GuestLifecycleTask{
		GuestType:   guestType,
		GuestID:     guestID,
		Action:      action,
		Source:      source,
		Status:      taskModels.LifecycleTaskStatusQueued,
		RequestedBy: requestedBy,
		Message:     RequestOutcomeQueued,
		Payload:     strings.TrimSpace(payload),
	}

	if err := s.DB.Create(task).Error; err != nil {
		return nil, "", err
	}

	if enqueue {
		if err := db.EnqueueJSON(ctx, guestLifecycleExecQueueName, guestLifecycleExecPayload{TaskID: task.ID}); err != nil {
			failedAt := time.Now().UTC()
			updateErr := s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", task.ID).Updates(map[string]any{
				"status":      taskModels.LifecycleTaskStatusFailed,
				"error":       fmt.Sprintf("enqueue_failed: %v", err),
				"finished_at": failedAt,
				"message":     "enqueue_failed",
			}).Error
			if updateErr != nil {
				logger.L.Warn().Err(updateErr).Uint("task_id", task.ID).Msg("guest_lifecycle_task_enqueue_failure_update_failed")
			}
			return nil, "", err
		}
	}

	return task, RequestOutcomeQueued, nil
}

func (s *Service) ExecuteTask(ctx context.Context, taskID uint) error {
	task := taskModels.GuestLifecycleTask{}
	if err := s.DB.First(&task, taskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	if task.Status == taskModels.LifecycleTaskStatusSuccess || task.Status == taskModels.LifecycleTaskStatusFailed {
		return nil
	}

	now := time.Now().UTC()
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"status":     taskModels.LifecycleTaskStatusRunning,
		"started_at": now,
		"message":    "running",
	}).Error; err != nil {
		return err
	}

	runErr := s.executeGuestAction(ctx, task)
	finishedAt := time.Now().UTC()

	updates := map[string]any{
		"finished_at": finishedAt,
	}

	if runErr != nil {
		if errors.Is(runErr, errGuestAlreadyRunning) {
			updates["status"] = taskModels.LifecycleTaskStatusSuccess
			updates["message"] = "already_running"
			updates["error"] = ""
		} else {
			updates["status"] = taskModels.LifecycleTaskStatusFailed
			updates["message"] = "failed"
			updates["error"] = runErr.Error()
		}
	} else {
		updates["status"] = taskModels.LifecycleTaskStatusSuccess
		updates["message"] = "completed"
		updates["error"] = ""
	}

	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).Where("id = ?", task.ID).Updates(updates).Error; err != nil {
		return err
	}

	return runErr
}

func (s *Service) executeGuestAction(ctx context.Context, task taskModels.GuestLifecycleTask) error {
	switch task.GuestType {
	case taskModels.GuestTypeVM:
		if s.vmActionFn == nil {
			return fmt.Errorf("vm_action_function_not_configured")
		}

		if task.Action == "start" {
			if s.vmStateFn != nil {
				state, err := s.vmStateFn(task.GuestID)
				if err == nil && state == 1 {
					return errGuestAlreadyRunning
				}
			}
		}

		return s.vmActionFn(task.GuestID, task.Action)

	case taskModels.GuestTypeJail:
		if s.jailActionFn == nil {
			return fmt.Errorf("jail_action_function_not_configured")
		}

		if task.Action == "start" {
			if s.jailActiveFn != nil {
				active, err := s.jailActiveFn(task.GuestID)
				if err == nil && active {
					return errGuestAlreadyRunning
				}
			}
		}

		return s.jailActionFn(int(task.GuestID), task.Action)

	case taskModels.GuestTypeJailTemplate:
		switch task.Action {
		case "convert":
			if s.jailTemplateConvertFn == nil {
				return fmt.Errorf("jail_template_convert_function_not_configured")
			}
			req := jail.ConvertToTemplateRequest{}
			if strings.TrimSpace(task.Payload) != "" {
				if err := json.Unmarshal([]byte(task.Payload), &req); err != nil {
					return fmt.Errorf("invalid_template_convert_payload: %w", err)
				}
			}
			return s.jailTemplateConvertFn(ctx, task.GuestID, req)
		case "create":
			if s.jailTemplateCreateFn == nil {
				return fmt.Errorf("jail_template_create_function_not_configured")
			}
			req := jail.CreateFromTemplateRequest{}
			if strings.TrimSpace(task.Payload) != "" {
				if err := json.Unmarshal([]byte(task.Payload), &req); err != nil {
					return fmt.Errorf("invalid_template_create_payload: %w", err)
				}
			}
			return s.jailTemplateCreateFn(ctx, task.GuestID, req)
		default:
			return fmt.Errorf("invalid_action: %s", task.Action)
		}
	case taskModels.GuestTypeVMTemplate:
		switch task.Action {
		case "convert":
			if s.vmTemplateConvertFn == nil {
				return fmt.Errorf("vm_template_convert_function_not_configured")
			}
			req := libvirtServiceInterfaces.ConvertToTemplateRequest{}
			if strings.TrimSpace(task.Payload) != "" {
				if err := json.Unmarshal([]byte(task.Payload), &req); err != nil {
					return fmt.Errorf("invalid_vm_template_convert_payload: %w", err)
				}
			}
			return s.vmTemplateConvertFn(ctx, task.GuestID, req)
		case "create":
			if s.vmTemplateCreateFn == nil {
				return fmt.Errorf("vm_template_create_function_not_configured")
			}
			req := libvirtServiceInterfaces.CreateFromTemplateRequest{}
			if strings.TrimSpace(task.Payload) != "" {
				if err := json.Unmarshal([]byte(task.Payload), &req); err != nil {
					return fmt.Errorf("invalid_vm_template_create_payload: %w", err)
				}
			}
			return s.vmTemplateCreateFn(ctx, task.GuestID, req)
		default:
			return fmt.Errorf("invalid_action: %s", task.Action)
		}

	default:
		return fmt.Errorf("invalid_guest_type: %s", task.GuestType)
	}
}

func (s *Service) GetActiveTaskForGuest(guestType string, guestID uint) (*taskModels.GuestLifecycleTask, error) {
	guestType = normalizeGuestType(guestType)

	var task taskModels.GuestLifecycleTask
	tx := s.DB.
		Where("guest_type = ? AND guest_id = ? AND status IN ?", guestType, guestID, []string{
			taskModels.LifecycleTaskStatusQueued,
			taskModels.LifecycleTaskStatusRunning,
		}).
		Order("created_at DESC").
		Order("id DESC").
		Limit(1).
		Find(&task)
	if tx.Error != nil {
		return nil, tx.Error
	}

	if tx.RowsAffected == 0 {
		return nil, nil
	}

	return &task, nil
}

func (s *Service) ListActiveTasks(guestType string, guestID uint) ([]taskModels.GuestLifecycleTask, error) {
	query := s.DB.
		Model(&taskModels.GuestLifecycleTask{}).
		Where("status IN ?", []string{
			taskModels.LifecycleTaskStatusQueued,
			taskModels.LifecycleTaskStatusRunning,
		})

	if strings.TrimSpace(guestType) != "" {
		query = query.Where("guest_type = ?", normalizeGuestType(guestType))
	}
	if guestID > 0 {
		query = query.Where("guest_id = ?", guestID)
	}

	var tasks []taskModels.GuestLifecycleTask
	if err := query.
		Order("created_at DESC").
		Order("id DESC").
		Find(&tasks).Error; err != nil {
		return nil, err
	}

	return tasks, nil
}

func (s *Service) ListRecentTasks(guestType string, guestID uint, limit int) ([]taskModels.GuestLifecycleTask, error) {
	query := s.DB.Model(&taskModels.GuestLifecycleTask{})

	if strings.TrimSpace(guestType) != "" {
		query = query.Where("guest_type = ?", normalizeGuestType(guestType))
	}
	if guestID > 0 {
		query = query.Where("guest_id = ?", guestID)
	}

	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var tasks []taskModels.GuestLifecycleTask
	if err := query.
		Order("created_at DESC").
		Order("id DESC").
		Limit(limit).
		Find(&tasks).Error; err != nil {
		return nil, err
	}

	return tasks, nil
}

func (s *Service) runStartupAutostart(ctx context.Context) error {
	jails := []jailModels.Jail{}
	if err := s.DB.
		Model(&jailModels.Jail{}).
		Where("start_at_boot = ?", true).
		Order("start_order ASC").
		Order("ct_id ASC").
		Find(&jails).Error; err != nil {
		return err
	}

	for _, jl := range jails {
		task, _, err := s.createTask(ctx, taskModels.GuestTypeJail, jl.CTID, "start", taskModels.LifecycleTaskSourceStartup, "startup", "", false)
		if err != nil {
			if errors.Is(err, ErrTaskInProgress) {
				continue
			}
			logger.L.Warn().Err(err).Uint("ct_id", jl.CTID).Msg("failed_to_create_startup_jail_task")
			continue
		}
		if task == nil {
			continue
		}

		if err := s.ExecuteTask(ctx, task.ID); err != nil && !errors.Is(err, errGuestAlreadyRunning) {
			logger.L.Warn().Err(err).Uint("task_id", task.ID).Msg("startup_jail_task_failed")
		}
	}

	vms := []vmModels.VM{}
	if err := s.DB.
		Model(&vmModels.VM{}).
		Where("start_at_boot = ?", true).
		Order("start_order ASC").
		Order("rid ASC").
		Find(&vms).Error; err != nil {
		return err
	}

	for _, vm := range vms {
		task, _, err := s.createTask(ctx, taskModels.GuestTypeVM, vm.RID, "start", taskModels.LifecycleTaskSourceStartup, "startup", "", false)
		if err != nil {
			if errors.Is(err, ErrTaskInProgress) {
				continue
			}
			logger.L.Warn().Err(err).Uint("rid", vm.RID).Msg("failed_to_create_startup_vm_task")
			continue
		}
		if task == nil {
			continue
		}

		if err := s.ExecuteTask(ctx, task.ID); err != nil && !errors.Is(err, errGuestAlreadyRunning) {
			logger.L.Warn().Err(err).Uint("task_id", task.ID).Msg("startup_vm_task_failed")
		}
	}

	return nil
}
