// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
)

const zfsStateChangeEventType = "resource.fs.zfs.statechange"

func (s *Service) emitPoolStateNotification(ctx context.Context, ev *models.NetlinkEvent) {
	if !shouldHandleZFSStateChangeEvent(ev) {
		return
	}

	input, shouldEmit, err := s.buildPoolStateChangeNotification(ctx, ev)
	if err != nil {
		logger.L.Debug().
			Err(err).
			Str("event_type", ev.Type).
			Interface("attrs", ev.Attrs).
			Msg("zfs_pool_state_notification_skipped")
		return
	}
	if !shouldEmit {
		return
	}

	_, err = notifier.Emit(ctx, input)
	if err != nil && !errors.Is(err, notifier.ErrEmitterNotConfigured) {
		logger.L.Error().
			Err(err).
			Str("kind", input.Kind).
			Interface("metadata", input.Metadata).
			Msg("failed_to_emit_zfs_pool_state_notification")
	}
}

func (s *Service) buildPoolStateChangeNotification(ctx context.Context, ev *models.NetlinkEvent) (notifier.EventInput, bool, error) {
	if s == nil || s.GZFS == nil {
		return notifier.EventInput{}, false, fmt.Errorf("gzfs_client_not_initialized")
	}

	poolName := strings.TrimSpace(ev.Attrs["pool"])
	if poolName == "" {
		return notifier.EventInput{}, false, fmt.Errorf("zfs_pool_missing_from_event")
	}
	if !s.isPoolActiveForNotifications(poolName) {
		return notifier.EventInput{}, false, nil
	}

	pool, err := s.GZFS.Zpool.Get(ctx, poolName)
	if err != nil {
		return notifier.EventInput{}, false, err
	}
	if pool == nil {
		return notifier.EventInput{}, false, fmt.Errorf("zfs_pool_not_found")
	}

	status, err := pool.Status(ctx)
	if err != nil {
		return notifier.EventInput{}, false, err
	}

	state := resolvePoolStateFromStatus(status, pool, ev.Attrs)
	if !shouldEmitNotificationForPoolState(state) {
		return notifier.EventInput{}, false, nil
	}

	return buildPoolStateNotificationInput(poolName, state, ev.Attrs), true, nil
}

func shouldHandleZFSStateChangeEvent(ev *models.NetlinkEvent) bool {
	if ev == nil {
		return false
	}

	eventType := strings.TrimSpace(strings.ToLower(ev.Type))
	return eventType == zfsStateChangeEventType
}

func shouldEmitNotificationForPoolState(state string) bool {
	switch normalizePoolState(state) {
	case string(gzfs.ZPoolStateOnline),
		string(gzfs.ZPoolStateDegraded),
		string(gzfs.ZPoolStateFaulted),
		string(gzfs.ZPoolStateOffline),
		string(gzfs.ZPoolStateRemoved),
		string(gzfs.ZPoolStateUnavailible),
		"SUSPENDED":
		return true
	default:
		return false
	}
}

func resolvePoolStateFromStatus(status *gzfs.ZPoolStatusPool, pool *gzfs.ZPool, attrs map[string]string) string {
	vdevGUID := strings.TrimSpace(attrs["vdev_guid"])
	vdevPath := strings.TrimSpace(attrs["vdev_path"])

	if status != nil {
		for _, group := range []map[string]*gzfs.ZPoolStatusVDEV{
			status.Vdevs,
			status.Logs,
			status.Spares,
			status.L2Cache,
		} {
			if vdev := findStatusVdev(group, vdevGUID, vdevPath); vdev != nil {
				state := normalizePoolState(vdev.State)
				if state != "" {
					return state
				}
			}
		}

		state := normalizePoolState(status.State)
		if state != "" {
			return state
		}
	}

	if pool != nil {
		state := normalizePoolState(string(pool.State))
		if state != "" {
			return state
		}
	}

	return ""
}

func findStatusVdev(vdevs map[string]*gzfs.ZPoolStatusVDEV, guid, path string) *gzfs.ZPoolStatusVDEV {
	for _, vdev := range vdevs {
		if found := findStatusVdevRecursive(vdev, guid, path); found != nil {
			return found
		}
	}

	return nil
}

func findStatusVdevRecursive(vdev *gzfs.ZPoolStatusVDEV, guid, path string) *gzfs.ZPoolStatusVDEV {
	if vdev == nil {
		return nil
	}

	if guid != "" && strings.EqualFold(strings.TrimSpace(vdev.GUID), guid) {
		return vdev
	}
	if path != "" && strings.EqualFold(strings.TrimSpace(vdev.Path), path) {
		return vdev
	}

	for _, child := range vdev.Vdevs {
		if found := findStatusVdevRecursive(child, guid, path); found != nil {
			return found
		}
	}

	return nil
}

func buildPoolStateNotificationInput(poolName, state string, attrs map[string]string) notifier.EventInput {
	pool := strings.TrimSpace(strings.ToLower(poolName))
	normalizedState := normalizePoolState(state)
	vdevGUID := strings.TrimSpace(attrs["vdev_guid"])
	vdevPath := strings.TrimSpace(attrs["vdev_path"])

	vdevIdentifier := "pool"
	if vdevGUID != "" {
		vdevIdentifier = vdevGUID
	} else if vdevPath != "" {
		vdevIdentifier = vdevPath
	}

	vdevLabel := vdevPath
	if vdevLabel == "" {
		vdevLabel = vdevGUID
	}
	if vdevLabel == "" {
		vdevLabel = "pool"
	}

	title := fmt.Sprintf("ZFS pool %s vdev %s is %s", pool, vdevLabel, normalizedState)
	body := fmt.Sprintf("ZFS state-change detected for pool %s: vdev %s is now %s.", pool, vdevLabel, normalizedState)
	if normalizedState == string(gzfs.ZPoolStateOnline) {
		title = fmt.Sprintf("ZFS pool %s vdev %s recovered to ONLINE", pool, vdevLabel)
		body = fmt.Sprintf("ZFS recovery detected for pool %s: vdev %s returned to ONLINE.", pool, vdevLabel)
	}

	return notifier.EventInput{
		Kind:        notifier.KindForZFSPoolState(pool),
		Title:       title,
		Body:        body,
		Severity:    severityForPoolState(normalizedState),
		Source:      "system.zfs.netlink",
		Fingerprint: fmt.Sprintf("%s|%s|%s", pool, strings.ToLower(vdevIdentifier), strings.ToLower(normalizedState)),
		Metadata: map[string]string{
			"pool":      pool,
			"vdev_guid": vdevGUID,
			"vdev_path": vdevPath,
			"state":     normalizedState,
		},
	}
}

func severityForPoolState(state string) string {
	switch normalizePoolState(state) {
	case string(gzfs.ZPoolStateDegraded):
		return string(models.NotificationSeverityWarning)
	case string(gzfs.ZPoolStateFaulted),
		string(gzfs.ZPoolStateUnavailible),
		"SUSPENDED":
		return string(models.NotificationSeverityCritical)
	case string(gzfs.ZPoolStateOffline),
		string(gzfs.ZPoolStateRemoved):
		return string(models.NotificationSeverityError)
	case string(gzfs.ZPoolStateOnline):
		return string(models.NotificationSeverityInfo)
	default:
		return string(models.NotificationSeverityInfo)
	}
}

func normalizePoolState(state string) string {
	return strings.ToUpper(strings.TrimSpace(state))
}

func (s *Service) isPoolActiveForNotifications(poolName string) bool {
	if s == nil || s.DB == nil {
		return false
	}

	var settings models.BasicSettings
	if err := s.DB.Limit(1).First(&settings).Error; err != nil {
		return false
	}

	poolName = strings.TrimSpace(strings.ToLower(poolName))
	for _, activePool := range settings.Pools {
		if strings.TrimSpace(strings.ToLower(activePool)) == poolName {
			return true
		}
	}

	return false
}
