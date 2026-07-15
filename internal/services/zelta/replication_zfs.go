// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	haSnapPrefix = "ha_"

	replicationPropertyPolicyID     = "sylve:replication-policy-id"
	replicationPropertyRunID        = "sylve:replication-run-id"
	replicationPropertyOwnerEpoch   = "sylve:replication-owner-epoch"
	replicationPropertySource       = "sylve:replication-source"
	replicationPropertyTarget       = "sylve:replication-target"
	replicationPropertyRole         = "sylve:replication-role"
	replicationPropertyState        = "sylve:replication-state"
	replicationPropertySnapshot     = "sylve:replication-snapshot"
	replicationPropertySnapshotGUID = "sylve:replication-snapshot-guid"
	replicationPropertyRetainedAt   = "sylve:replication-retained-at"

	replicationRoleStandby    = "standby"
	replicationStateReceiving = "receiving"
	replicationStateStaged    = "staged"
	replicationStateReady     = "ready"
)

// ReplicationZFSTransferOptions identifies one fenced replication generation.
// SnapshotAlreadyCreated allows a caller to create one coherent snapshot group
// across all roots before sending any of them.
type ReplicationZFSTransferOptions struct {
	PolicyID               uint
	RunID                  string
	OwnerEpoch             uint64
	SnapshotName           string
	SnapshotGUID           string
	SnapshotAlreadyCreated bool
	GenerationName         string
}

// ReplicationStagedTransferResult describes a completed, verified staging
// receive. Promotion is intentionally separate so callers can stage every root
// before committing any of them.
type ReplicationStagedTransferResult struct {
	SnapshotName   string
	SnapshotGUID   string
	TargetDataset  string
	StagingDataset string
}

// ReplicationSnapshotManifestEntry is stable input for the higher-level run
// manifest. A caller can sort these entries and hash dataset, snapshot and GUID
// to bind every staged receive to the source generation it was created from.
type ReplicationSnapshotManifestEntry struct {
	SourceDataset string
	SnapshotName  string
	SnapshotGUID  string
}

type replicationSnapshotIdentity struct {
	Name string
	GUID string
}

type replicationPreviousDatasetInfo struct {
	Name     string
	Creation uint64
}

type replicationAttemptHooks struct {
	Run            func(forceRecv bool) (string, error)
	Abort          func() (string, error)
	AuthorizeForce func() error
	Reset          func() error
}

type replicationStagingSeedResult struct {
	Seeded         bool
	CommonSnapshot string
	CommonGUID     string
	Output         string
}

func validReplicationZFSToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-', r == ':':
		default:
			return false
		}
	}
	return true
}

func validateReplicationSnapshotName(snapshotName string) error {
	snapshotName = strings.TrimSpace(snapshotName)
	if !validReplicationZFSToken(snapshotName) {
		return fmt.Errorf("invalid_replication_snapshot_name")
	}
	if !strings.HasPrefix(strings.ToLower(snapshotName), haSnapPrefix) {
		return fmt.Errorf("replication_snapshot_name_must_use_ha_prefix")
	}
	return nil
}

func (opts ReplicationZFSTransferOptions) validate() error {
	if opts.PolicyID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	if opts.OwnerEpoch == 0 {
		return fmt.Errorf("replication_owner_epoch_required")
	}
	if !validReplicationZFSToken(opts.RunID) {
		return fmt.Errorf("invalid_replication_run_id")
	}
	if err := validateReplicationSnapshotName(opts.SnapshotName); err != nil {
		return err
	}
	if snapshotGUID := strings.TrimSpace(opts.SnapshotGUID); snapshotGUID != "" {
		if _, err := strconv.ParseUint(snapshotGUID, 10, 64); err != nil {
			return fmt.Errorf("invalid_replication_snapshot_guid")
		}
	}
	if strings.TrimSpace(opts.GenerationName) != "" && !validReplicationZFSToken(opts.GenerationName) {
		return fmt.Errorf("invalid_replication_generation_name")
	}
	return nil
}

func replicationGenerationName(opts ReplicationZFSTransferOptions) string {
	if generation := strings.TrimSpace(opts.GenerationName); generation != "" {
		return generation
	}
	return strings.TrimSpace(opts.RunID)
}

func replicationStagingDatasetPath(targetPath string, opts ReplicationZFSTransferOptions) (string, error) {
	var err error
	if targetPath, err = validateExactReplicationDatasetPath(targetPath); err != nil {
		return "", fmt.Errorf("invalid_replication_target_dataset: %w", err)
	}
	generation := replicationGenerationName(opts)
	if !validReplicationZFSToken(generation) {
		return "", fmt.Errorf("invalid_replication_generation_name")
	}
	return targetPath + "_gen-" + generation, nil
}

func replicationPreviousDatasetPath(targetPath string, opts ReplicationZFSTransferOptions) (string, error) {
	var err error
	if targetPath, err = validateExactReplicationDatasetPath(targetPath); err != nil {
		return "", fmt.Errorf("invalid_replication_target_dataset: %w", err)
	}
	generation := replicationGenerationName(opts)
	if !validReplicationZFSToken(generation) {
		return "", fmt.Errorf("invalid_replication_generation_name")
	}
	return targetPath + "_previous-" + generation, nil
}

func replicationProvenanceProperties(
	opts ReplicationZFSTransferOptions,
	sourceDataset string,
	targetDataset string,
	state string,
) map[string]string {
	return map[string]string{
		replicationPropertyPolicyID:     strconv.FormatUint(uint64(opts.PolicyID), 10),
		replicationPropertyRunID:        strings.TrimSpace(opts.RunID),
		replicationPropertyOwnerEpoch:   strconv.FormatUint(opts.OwnerEpoch, 10),
		replicationPropertySource:       normalizeDatasetPath(sourceDataset),
		replicationPropertyTarget:       normalizeDatasetPath(targetDataset),
		replicationPropertyRole:         replicationRoleStandby,
		replicationPropertyState:        strings.TrimSpace(state),
		replicationPropertySnapshot:     strings.TrimSpace(opts.SnapshotName),
		replicationPropertySnapshotGUID: strings.TrimSpace(opts.SnapshotGUID),
	}
}

// CreateReplicationSnapshotGroup creates one named recursive snapshot across
// every supplied root before transfer begins. Reusing the returned name for all
// roots gives callers a coherent replication generation instead of taking the
// next root's snapshot after the previous root has finished sending.
func (s *Service) CreateReplicationSnapshotGroup(
	ctx context.Context,
	sourceDatasets []string,
	snapshotName string,
) error {
	if err := validateReplicationSnapshotName(snapshotName); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(sourceDatasets))
	args := []string{"snapshot", "-r"}
	for _, sourceDataset := range sourceDatasets {
		sourceDataset = normalizeDatasetPath(sourceDataset)
		if sourceDataset == "" {
			return fmt.Errorf("source_dataset_required")
		}
		if _, exists := seen[sourceDataset]; exists {
			continue
		}
		seen[sourceDataset] = struct{}{}
		args = append(args, sourceDataset+"@"+snapshotName)
	}
	if len(args) == 2 {
		return fmt.Errorf("source_datasets_required")
	}
	output, err := utils.RunCommandWithContext(ctx, "zfs", args...)
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	return nil
}

func (s *Service) replicationZFSSendPreparedToPath(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	_ string,
	targetPath string,
	snapshotName string,
	receiveProperties map[string]string,
	allowProvenForce bool,
	authorizeProvenForce func() error,
	resetProvenDataset func() error,
	onLine func(string),
) (string, error) {
	if target == nil {
		return "", fmt.Errorf("replication_target_required")
	}
	sourceDataset = normalizeDatasetPath(sourceDataset)
	if sourceDataset == "" {
		return "", fmt.Errorf("source_dataset_required")
	}
	if err := validateReplicationSnapshotName(snapshotName); err != nil {
		return "", err
	}

	targetPath = normalizeDatasetPath(targetPath)
	if targetPath == "" {
		return "", fmt.Errorf("replication_target_dataset_required")
	}

	var outputLog strings.Builder
	appendLine := func(line string) {
		cleaned := strings.TrimSpace(line)
		if cleaned == "" {
			return
		}
		if outputLog.Len() > 0 {
			outputLog.WriteByte('\n')
		}
		outputLog.WriteString(cleaned)
		if onLine != nil {
			onLine(cleaned)
		}
	}

	if err := s.ensureTargetParentDatasets(ctx, target, targetPath); err != nil {
		appendLine(fmt.Sprintf("ensure_target_parent_datasets_failed: %v", err))
		return "", err
	}

	commonSnap, encrypted, err := prepareReplicationTransferMetadata(
		func() (string, error) {
			return s.findCommonReplicationSnapshot(ctx, target, sourceDataset, targetPath)
		},
		func() (bool, error) {
			return s.isDatasetEncrypted(ctx, sourceDataset)
		},
	)
	if err != nil {
		appendLine(err.Error())
		return outputLog.String(), err
	}

	if commonSnap == snapshotName {
		appendLine("replication_snapshot_already_present")
	} else {
		attemptErr := runReplicationAttempts(
			3,
			allowProvenForce,
			replicationAttemptHooks{
				Run: func(forceRecv bool) (string, error) {
					return s.runReplicationPipelineWithProperties(
						ctx,
						target,
						sourceDataset,
						snapshotName,
						commonSnap,
						targetPath,
						encrypted,
						forceRecv,
						receiveProperties,
					)
				},
				Abort: func() (string, error) {
					return s.abortTargetResumableReceiveDataset(ctx, target, targetPath)
				},
				AuthorizeForce: authorizeProvenForce,
				Reset: func() error {
					if resetProvenDataset == nil {
						return fmt.Errorf("replication_proven_staging_reset_required")
					}
					if err := resetProvenDataset(); err != nil {
						return err
					}
					commonSnap = ""
					return nil
				},
			},
			appendLine,
		)
		if attemptErr != nil {
			return outputLog.String(), attemptErr
		}
	}

	if err := requireReplicationReadonly(func() error {
		if err := s.ensureTargetReplicationReadonly(ctx, target, targetPath); err != nil {
			return err
		}
		return s.verifyTargetReplicationReadonly(ctx, target, targetPath)
	}); err != nil {
		appendLine(err.Error())
		return outputLog.String(), err
	}

	return outputLog.String(), nil
}

func prepareReplicationTransferMetadata(
	findCommon func() (string, error),
	detectEncryption func() (bool, error),
) (string, bool, error) {
	if findCommon == nil {
		return "", false, fmt.Errorf("replication_common_snapshot_lookup_required")
	}
	commonSnapshot, err := findCommon()
	if err != nil {
		return "", false, fmt.Errorf("replication_common_snapshot_lookup_failed: %w", err)
	}
	if detectEncryption == nil {
		return "", false, fmt.Errorf("replication_encryption_check_required")
	}
	encrypted, err := detectEncryption()
	if err != nil {
		return "", false, fmt.Errorf("replication_encryption_check_failed: %w", err)
	}
	return commonSnapshot, encrypted, nil
}

func requireReplicationReadonly(harden func() error) error {
	if harden == nil {
		return fmt.Errorf("replication_target_readonly_hardening_required")
	}
	if err := harden(); err != nil {
		return fmt.Errorf("replication_target_readonly_hardening_failed: %w", err)
	}
	return nil
}

func runReplicationAttempts(
	maxAttempts int,
	allowProvenForce bool,
	hooks replicationAttemptHooks,
	appendLine func(string),
) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if hooks.Run == nil {
		return fmt.Errorf("replication_attempt_runner_required")
	}
	if appendLine == nil {
		appendLine = func(string) {}
	}

	forceRecv := false
	completed := false
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if forceRecv {
			if hooks.AuthorizeForce == nil {
				return fmt.Errorf("replication_force_receive_provenance_required")
			}
			if err := hooks.AuthorizeForce(); err != nil {
				return fmt.Errorf("replication_force_receive_provenance_failed: %w", err)
			}
		}
		out, sendErr := hooks.Run(forceRecv)
		if strings.TrimSpace(out) != "" {
			appendLine(out)
		}
		if sendErr == nil {
			completed = true
			break
		}
		lastErr = sendErr

		if isReplicationResumeStateError(sendErr) {
			appendLine("target_partial_receive_detected_aborting")
			if hooks.Abort == nil {
				return fmt.Errorf("replication_resume_abort_required: %w", sendErr)
			}
			abortOut, abortErr := hooks.Abort()
			if strings.TrimSpace(abortOut) != "" {
				appendLine(abortOut)
			}
			if abortErr != nil {
				return fmt.Errorf(
					"replication_failed_after_partial_receive_abort_failed: %w (original: %v)",
					abortErr,
					sendErr,
				)
			}
			appendLine("partial_receive_aborted_retrying")
			continue
		}

		if isReplicationTargetModifiedError(sendErr) {
			if !allowProvenForce {
				return fmt.Errorf("replication_target_diverged_requires_staged_reseed: %w", sendErr)
			}
			if !forceRecv {
				appendLine("proven_staging_dataset_diverged_retrying_with_force_recv")
				forceRecv = true
				continue
			}
		}

		lowerSend := strings.ToLower(sendErr.Error())
		lowerOut := strings.ToLower(out)
		needsReset := strings.Contains(lowerSend, "has snapshots") ||
			strings.Contains(lowerSend, "must destroy")
		if allowProvenForce && needsReset && attempt+1 < maxAttempts {
			if hooks.Reset == nil {
				return fmt.Errorf("replication_proven_staging_reset_required: %w", sendErr)
			}
			appendLine("proven_staging_dataset_resetting")
			if resetErr := hooks.Reset(); resetErr != nil {
				return fmt.Errorf("replication_proven_staging_reset_failed: %w (original: %v)", resetErr, sendErr)
			}
			forceRecv = false
			continue
		}

		interrupted := strings.Contains(lowerSend, "signal") ||
			strings.Contains(lowerSend, "broken pipe") ||
			strings.Contains(lowerSend, "exit status") ||
			strings.Contains(lowerOut, "cannot receive") ||
			strings.Contains(lowerOut, "failed to read from stream")
		if interrupted && attempt+1 < maxAttempts {
			appendLine("replication_transfer_interrupted_retrying")
			continue
		}

		return sendErr
	}

	if !completed {
		if lastErr == nil {
			lastErr = fmt.Errorf("replication_transfer_not_completed")
		}
		return fmt.Errorf("replication_retry_exhausted_after_%d_attempts: %w", maxAttempts, lastErr)
	}
	return nil
}

func (s *Service) runReplicationPipelineWithProperties(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	snapName string,
	commonSnap string,
	targetPath string,
	encrypted bool,
	forceRecv bool,
	receiveProperties map[string]string,
) (string, error) {
	if forceRecv && !hasCompleteReplicationProvenance(receiveProperties) {
		return "", fmt.Errorf("replication_force_receive_provenance_required")
	}
	sendArgs := replicationZFSSendArgs(sourceDataset, snapName, commonSnap, encrypted)

	sshArgs := s.buildSSHArgs(target)
	recvArgs := make([]string, 0, len(sshArgs)+10)
	for _, a := range sshArgs {
		if a != "-n" {
			recvArgs = append(recvArgs, a)
		}
	}
	recvArgs = append(recvArgs, target.SSHHost, "zfs", "recv", "-u", "-x", "mountpoint", "-o", "canmount=noauto")
	propertyNames := make([]string, 0, len(receiveProperties))
	for property := range receiveProperties {
		propertyNames = append(propertyNames, property)
	}
	sort.Strings(propertyNames)
	for _, property := range propertyNames {
		recvArgs = append(recvArgs, "-o", property+"="+receiveProperties[property])
	}
	if forceRecv {
		recvArgs = append(recvArgs, "-F")
	}
	recvArgs = append(recvArgs, targetPath)

	sendCmd := exec.CommandContext(ctx, "zfs", sendArgs...)
	recvCmd := exec.CommandContext(ctx, "ssh", recvArgs...)

	pr, pw := io.Pipe()
	sendCmd.Stdout = pw
	recvCmd.Stdin = pr

	var sendStderr bytes.Buffer
	var recvStderr bytes.Buffer
	sendCmd.Stderr = &sendStderr
	recvCmd.Stderr = &recvStderr

	if err := sendCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return "", fmt.Errorf("zfs_send_start_failed: %w", err)
	}
	if err := recvCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		sendCmd.Wait()
		return "", fmt.Errorf("ssh_recv_start_failed: %w", err)
	}

	var sendErr error
	done := make(chan struct{})
	go func() {
		sendErr = sendCmd.Wait()
		pw.Close()
		close(done)
	}()

	recvErr := recvCmd.Wait()
	pr.Close()
	<-done

	var combined strings.Builder
	sendOut := strings.TrimSpace(sendStderr.String())
	recvOut := strings.TrimSpace(recvStderr.String())
	if sendOut != "" {
		combined.WriteString(sendOut)
	}
	if recvOut != "" {
		if combined.Len() > 0 {
			combined.WriteByte('\n')
		}
		combined.WriteString(recvOut)
	}

	if recvErr != nil {
		return combined.String(), fmt.Errorf("%s: %w", combined.String(), recvErr)
	}
	if sendErr != nil {
		return combined.String(), fmt.Errorf("zfs_send_failed: %w", sendErr)
	}

	return combined.String(), nil
}

func replicationZFSSendArgs(
	sourceDataset string,
	snapshotName string,
	commonSnapshot string,
	encrypted bool,
) []string {
	var args []string
	if encrypted {
		args = append(args, "send", "--raw", "-P", "-R")
	} else {
		args = append(args, "send", "-P", "-R", "-L", "-c", "-e")
	}
	if commonSnapshot = strings.TrimSpace(commonSnapshot); commonSnapshot != "" {
		args = append(args, "-i", "@"+commonSnapshot)
	}
	return append(args, normalizeDatasetPath(sourceDataset)+"@"+strings.TrimSpace(snapshotName))
}

func hasCompleteReplicationProvenance(properties map[string]string) bool {
	for _, property := range []string{
		replicationPropertyPolicyID,
		replicationPropertyRunID,
		replicationPropertyOwnerEpoch,
		replicationPropertySource,
		replicationPropertyTarget,
		replicationPropertyRole,
		replicationPropertyState,
		replicationPropertySnapshot,
		replicationPropertySnapshotGUID,
	} {
		if strings.TrimSpace(properties[property]) == "" {
			return false
		}
	}
	return true
}

func (s *Service) abortTargetResumableReceiveDataset(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	targetDataset string,
) (string, error) {
	if target == nil {
		return "", fmt.Errorf("replication_target_required")
	}
	targetDataset = normalizeDatasetPath(targetDataset)
	if targetDataset == "" {
		return "", fmt.Errorf("replication_target_dataset_required")
	}
	output, err := s.runTargetSSH(ctx, target, "zfs", "receive", "-A", targetDataset)
	if err != nil && !isReplicationResumeAbortNoopError(err) {
		return output, err
	}
	return output, nil
}

func (s *Service) findCommonReplicationSnapshot(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	targetPath string,
) (string, error) {
	localSnaps, err := s.listHaSnapshotIdentitiesLocal(ctx, sourceDataset)
	if err != nil {
		return "", fmt.Errorf("list_local_ha_snapshots_failed: %w", err)
	}
	remoteSnaps, err := s.listHaSnapshotIdentitiesRemote(ctx, target, targetPath)
	if err != nil {
		return "", fmt.Errorf("list_remote_ha_snapshots_failed: %w", err)
	}
	return latestCommonReplicationSnapshot(localSnaps, remoteSnaps)
}

func latestCommonReplicationSnapshot(
	localSnaps []replicationSnapshotIdentity,
	remoteSnaps []replicationSnapshotIdentity,
) (string, error) {
	if len(localSnaps) == 0 || len(remoteSnaps) == 0 {
		return "", nil
	}

	remoteByName := make(map[string]string, len(remoteSnaps))
	for _, snap := range remoteSnaps {
		remoteByName[snap.Name] = snap.GUID
	}

	for i := len(localSnaps) - 1; i >= 0; i-- {
		remoteGUID, ok := remoteByName[localSnaps[i].Name]
		if !ok {
			continue
		}
		if localSnaps[i].GUID == "" || remoteGUID == "" || localSnaps[i].GUID != remoteGUID {
			return "", fmt.Errorf("replication_snapshot_guid_mismatch:%s", localSnaps[i].Name)
		}
		return localSnaps[i].Name, nil
	}

	return "", nil
}

func parseReplicationSnapshotIdentities(output, dataset string) ([]replicationSnapshotIdentity, error) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return []replicationSnapshotIdentity{}, nil
	}

	identities := make([]replicationSnapshotIdentity, 0)
	prefix := dataset + "@" + haSnapPrefix
	scan := bufio.NewScanner(strings.NewReader(output))
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], prefix) {
			continue
		}
		if len(fields) != 2 || strings.TrimSpace(fields[1]) == "" || fields[1] == "-" {
			return nil, fmt.Errorf("invalid_replication_snapshot_identity:%s", fields[0])
		}
		shortName := strings.TrimPrefix(fields[0], dataset+"@")
		if shortName == "" {
			return nil, fmt.Errorf("invalid_replication_snapshot_name:%s", fields[0])
		}
		identities = append(identities, replicationSnapshotIdentity{
			Name: shortName,
			GUID: strings.TrimSpace(fields[1]),
		})
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return identities, nil
}

func snapshotIdentityNames(identities []replicationSnapshotIdentity) []string {
	names := make([]string, 0, len(identities))
	for _, identity := range identities {
		if identity.Name != "" {
			names = append(names, identity.Name)
		}
	}
	return names
}

func replicationSnapshotGUID(
	identities []replicationSnapshotIdentity,
	snapshotName string,
) (string, error) {
	snapshotName = strings.TrimSpace(snapshotName)
	for _, identity := range identities {
		if identity.Name != snapshotName {
			continue
		}
		if identity.GUID == "" {
			return "", fmt.Errorf("replication_snapshot_guid_missing:%s", snapshotName)
		}
		return identity.GUID, nil
	}
	return "", fmt.Errorf("replication_snapshot_not_found:%s", snapshotName)
}

// GetReplicationSnapshotManifest resolves source-side snapshot GUIDs for every
// root in a shared generation. It fails if any root is missing the snapshot.
func (s *Service) GetReplicationSnapshotManifest(
	ctx context.Context,
	sourceDatasets []string,
	snapshotName string,
) ([]ReplicationSnapshotManifestEntry, error) {
	if err := validateReplicationSnapshotName(snapshotName); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(sourceDatasets))
	manifest := make([]ReplicationSnapshotManifestEntry, 0, len(sourceDatasets))
	for _, sourceDataset := range sourceDatasets {
		sourceDataset = normalizeDatasetPath(sourceDataset)
		if sourceDataset == "" {
			return nil, fmt.Errorf("source_dataset_required")
		}
		if _, exists := seen[sourceDataset]; exists {
			continue
		}
		seen[sourceDataset] = struct{}{}
		identities, err := s.listHaSnapshotIdentitiesLocal(ctx, sourceDataset)
		if err != nil {
			return nil, fmt.Errorf("list_replication_snapshot_manifest_%s_failed: %w", sourceDataset, err)
		}
		guid, err := replicationSnapshotGUID(identities, snapshotName)
		if err != nil {
			return nil, fmt.Errorf("resolve_replication_snapshot_manifest_%s_failed: %w", sourceDataset, err)
		}
		manifest = append(manifest, ReplicationSnapshotManifestEntry{
			SourceDataset: sourceDataset,
			SnapshotName:  snapshotName,
			SnapshotGUID:  guid,
		})
	}
	if len(manifest) == 0 {
		return nil, fmt.Errorf("source_datasets_required")
	}
	sort.Slice(manifest, func(i, j int) bool {
		return manifest[i].SourceDataset < manifest[j].SourceDataset
	})
	return manifest, nil
}

func parseReplicationDatasetTree(output, rootDataset string) ([]string, error) {
	rootDataset = normalizeDatasetPath(rootDataset)
	if rootDataset == "" {
		return nil, fmt.Errorf("replication_tree_root_required")
	}
	seen := make(map[string]struct{})
	datasets := make([]string, 0)
	scan := bufio.NewScanner(strings.NewReader(output))
	for scan.Scan() {
		fields := strings.Fields(strings.TrimSpace(scan.Text()))
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 1 {
			return nil, fmt.Errorf("invalid_replication_dataset_tree_entry")
		}
		dataset := normalizeDatasetPath(fields[0])
		if dataset != rootDataset && !strings.HasPrefix(dataset, rootDataset+"/") {
			return nil, fmt.Errorf("replication_dataset_outside_tree:%s", dataset)
		}
		if _, exists := seen[dataset]; exists {
			return nil, fmt.Errorf("duplicate_replication_dataset_tree_entry:%s", dataset)
		}
		seen[dataset] = struct{}{}
		datasets = append(datasets, dataset)
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if _, exists := seen[rootDataset]; !exists {
		return nil, fmt.Errorf("replication_tree_root_missing:%s", rootDataset)
	}
	sort.Strings(datasets)
	return datasets, nil
}

func parseReplicationSnapshotTreeGUIDs(
	output string,
	rootDataset string,
	snapshotName string,
) (map[string]string, error) {
	rootDataset = normalizeDatasetPath(rootDataset)
	snapshotName = strings.TrimSpace(snapshotName)
	if rootDataset == "" || snapshotName == "" {
		return nil, fmt.Errorf("replication_snapshot_tree_identity_required")
	}
	guids := make(map[string]string)
	scan := bufio.NewScanner(strings.NewReader(output))
	for scan.Scan() {
		fields := strings.Fields(strings.TrimSpace(scan.Text()))
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid_replication_snapshot_tree_entry")
		}
		at := strings.LastIndex(fields[0], "@")
		if at <= 0 || fields[0][at+1:] != snapshotName {
			continue
		}
		dataset := normalizeDatasetPath(fields[0][:at])
		if dataset != rootDataset && !strings.HasPrefix(dataset, rootDataset+"/") {
			return nil, fmt.Errorf("replication_snapshot_outside_tree:%s", dataset)
		}
		guid := strings.TrimSpace(fields[1])
		if guid == "" || guid == "-" {
			return nil, fmt.Errorf("replication_snapshot_tree_guid_missing:%s", dataset)
		}
		if _, exists := guids[dataset]; exists {
			return nil, fmt.Errorf("duplicate_replication_snapshot_tree_entry:%s", dataset)
		}
		guids[dataset] = guid
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return guids, nil
}

func buildReplicationSnapshotTreeManifest(
	datasets []string,
	guids map[string]string,
	observedRoot string,
	canonicalSourceRoot string,
	snapshotName string,
) ([]ReplicationSnapshotManifestEntry, error) {
	observedRoot = normalizeDatasetPath(observedRoot)
	canonicalSourceRoot = normalizeDatasetPath(canonicalSourceRoot)
	if observedRoot == "" || canonicalSourceRoot == "" {
		return nil, fmt.Errorf("replication_snapshot_tree_root_required")
	}
	manifest := make([]ReplicationSnapshotManifestEntry, 0, len(datasets))
	for _, dataset := range datasets {
		dataset = normalizeDatasetPath(dataset)
		guid := strings.TrimSpace(guids[dataset])
		if guid == "" {
			return nil, fmt.Errorf("replication_snapshot_tree_generation_missing:%s", dataset)
		}
		relative := strings.TrimPrefix(dataset, observedRoot)
		if relative != "" && !strings.HasPrefix(relative, "/") {
			return nil, fmt.Errorf("replication_snapshot_tree_mapping_invalid:%s", dataset)
		}
		manifest = append(manifest, ReplicationSnapshotManifestEntry{
			SourceDataset: canonicalSourceRoot + relative,
			SnapshotName:  strings.TrimSpace(snapshotName),
			SnapshotGUID:  guid,
		})
	}
	sort.Slice(manifest, func(i, j int) bool {
		return manifest[i].SourceDataset < manifest[j].SourceDataset
	})
	return manifest, nil
}

func (s *Service) replicationSnapshotTreeManifestLocal(
	ctx context.Context,
	observedRoot string,
	canonicalSourceRoot string,
	snapshotName string,
) ([]ReplicationSnapshotManifestEntry, error) {
	datasetOutput, err := utils.RunCommandWithContext(
		ctx,
		"zfs", "list", "-H", "-r", "-t", "filesystem,volume", "-o", "name", observedRoot,
	)
	if err != nil {
		return nil, fmt.Errorf("list_replication_dataset_tree_failed: %w", err)
	}
	snapshotOutput, err := utils.RunCommandWithContext(
		ctx,
		"zfs", "list", "-H", "-p", "-r", "-t", "snapshot", "-o", "name,guid", observedRoot,
	)
	if err != nil {
		return nil, fmt.Errorf("list_replication_snapshot_tree_failed: %w", err)
	}
	datasets, err := parseReplicationDatasetTree(datasetOutput, observedRoot)
	if err != nil {
		return nil, err
	}
	guids, err := parseReplicationSnapshotTreeGUIDs(snapshotOutput, observedRoot, snapshotName)
	if err != nil {
		return nil, err
	}
	return buildReplicationSnapshotTreeManifest(
		datasets,
		guids,
		observedRoot,
		canonicalSourceRoot,
		snapshotName,
	)
}

func (s *Service) replicationSnapshotTreeManifestRemote(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	observedRoot string,
	canonicalSourceRoot string,
	snapshotName string,
) ([]ReplicationSnapshotManifestEntry, error) {
	if target == nil {
		return nil, fmt.Errorf("replication_target_required")
	}
	datasetOutput, err := s.runTargetSSH(
		ctx,
		target,
		"zfs", "list", "-H", "-r", "-t", "filesystem,volume", "-o", "name", observedRoot,
	)
	if err != nil {
		return nil, fmt.Errorf("list_target_replication_dataset_tree_failed: %w", err)
	}
	snapshotOutput, err := s.runTargetSSH(
		ctx,
		target,
		"zfs", "list", "-H", "-p", "-r", "-t", "snapshot", "-o", "name,guid", observedRoot,
	)
	if err != nil {
		return nil, fmt.Errorf("list_target_replication_snapshot_tree_failed: %w", err)
	}
	datasets, err := parseReplicationDatasetTree(datasetOutput, observedRoot)
	if err != nil {
		return nil, err
	}
	guids, err := parseReplicationSnapshotTreeGUIDs(snapshotOutput, observedRoot, snapshotName)
	if err != nil {
		return nil, err
	}
	return buildReplicationSnapshotTreeManifest(
		datasets,
		guids,
		observedRoot,
		canonicalSourceRoot,
		snapshotName,
	)
}

func mergeReplicationSnapshotTreeManifests(
	manifests ...[]ReplicationSnapshotManifestEntry,
) ([]ReplicationSnapshotManifestEntry, error) {
	seen := make(map[string]struct{})
	merged := make([]ReplicationSnapshotManifestEntry, 0)
	for _, manifest := range manifests {
		for _, entry := range manifest {
			key := normalizeDatasetPath(entry.SourceDataset)
			if _, exists := seen[key]; exists {
				return nil, fmt.Errorf("duplicate_replication_snapshot_tree_source:%s", key)
			}
			seen[key] = struct{}{}
			merged = append(merged, entry)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].SourceDataset < merged[j].SourceDataset
	})
	return merged, nil
}

func replicationSnapshotTreeManifestsEqual(
	left []ReplicationSnapshotManifestEntry,
	right []ReplicationSnapshotManifestEntry,
) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if normalizeDatasetPath(left[i].SourceDataset) != normalizeDatasetPath(right[i].SourceDataset) ||
			strings.TrimSpace(left[i].SnapshotName) != strings.TrimSpace(right[i].SnapshotName) ||
			strings.TrimSpace(left[i].SnapshotGUID) != strings.TrimSpace(right[i].SnapshotGUID) {
			return false
		}
	}
	return true
}

func isReplicationSnapshotTreeGenerationMismatch(err error) bool {
	if err == nil {
		return false
	}
	lowerErr := strings.ToLower(err.Error())
	return strings.Contains(lowerErr, "replication_snapshot_tree_generation_missing") ||
		strings.Contains(lowerErr, "replication_tree_root_missing")
}

func (s *Service) GetReplicationSnapshotTreeManifest(
	ctx context.Context,
	sourceDatasets []string,
	snapshotName string,
) ([]ReplicationSnapshotManifestEntry, error) {
	if err := validateReplicationSnapshotName(snapshotName); err != nil {
		return nil, err
	}
	manifests := make([][]ReplicationSnapshotManifestEntry, 0, len(sourceDatasets))
	seen := make(map[string]struct{})
	for _, sourceDataset := range sourceDatasets {
		sourceDataset = normalizeDatasetPath(sourceDataset)
		if sourceDataset == "" {
			return nil, fmt.Errorf("source_dataset_required")
		}
		if _, exists := seen[sourceDataset]; exists {
			continue
		}
		seen[sourceDataset] = struct{}{}
		manifest, err := s.replicationSnapshotTreeManifestLocal(
			ctx,
			sourceDataset,
			sourceDataset,
			snapshotName,
		)
		if err != nil {
			return nil, fmt.Errorf("replication_snapshot_tree_%s_failed: %w", sourceDataset, err)
		}
		manifests = append(manifests, manifest)
	}
	merged, err := mergeReplicationSnapshotTreeManifests(manifests...)
	if err != nil {
		return nil, err
	}
	if len(merged) == 0 {
		return nil, fmt.Errorf("source_datasets_required")
	}
	return merged, nil
}

func (s *Service) bindReplicationSnapshotGUID(
	ctx context.Context,
	sourceDataset string,
	opts ReplicationZFSTransferOptions,
) (ReplicationZFSTransferOptions, error) {
	manifest, err := s.GetReplicationSnapshotManifest(ctx, []string{sourceDataset}, opts.SnapshotName)
	if err != nil {
		return opts, err
	}
	actualGUID := manifest[0].SnapshotGUID
	if expectedGUID := strings.TrimSpace(opts.SnapshotGUID); expectedGUID != "" && expectedGUID != actualGUID {
		return opts, fmt.Errorf(
			"replication_source_snapshot_guid_changed expected=%s actual=%s",
			expectedGUID,
			actualGUID,
		)
	}
	opts.SnapshotGUID = actualGUID
	return opts, nil
}

func (s *Service) ensureReplicationSnapshotGUID(
	ctx context.Context,
	sourceDataset string,
	opts ReplicationZFSTransferOptions,
) (ReplicationZFSTransferOptions, error) {
	if strings.TrimSpace(opts.SnapshotGUID) != "" {
		return opts, nil
	}
	return s.bindReplicationSnapshotGUID(ctx, sourceDataset, opts)
}

func (s *Service) listHaSnapshotIdentitiesLocal(ctx context.Context, dataset string) ([]replicationSnapshotIdentity, error) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return []replicationSnapshotIdentity{}, nil
	}

	output, err := utils.RunCommandWithContext(ctx, "zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,guid", "-s", "creation", dataset)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "dataset does not exist") ||
			strings.Contains(strings.ToLower(err.Error()), "no such") {
			return []replicationSnapshotIdentity{}, nil
		}
		return nil, err
	}

	return parseReplicationSnapshotIdentities(output, dataset)
}

func (s *Service) listHaSnapshotIdentitiesRemote(ctx context.Context, target *clusterModels.BackupTarget, dataset string) ([]replicationSnapshotIdentity, error) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return []replicationSnapshotIdentity{}, nil
	}
	if target == nil {
		return nil, fmt.Errorf("replication_target_required")
	}

	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "list", "-H", "-p", "-t", "snapshot", "-o", "name,guid", "-s", "creation", dataset)
	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "dataset does not exist") ||
			strings.Contains(strings.ToLower(err.Error()), "no such") {
			return []replicationSnapshotIdentity{}, nil
		}
		return nil, err
	}

	return parseReplicationSnapshotIdentities(output, dataset)
}

func (s *Service) listHaSnapshotsLocal(ctx context.Context, dataset string) ([]string, error) {
	identities, err := s.listHaSnapshotIdentitiesLocal(ctx, dataset)
	if err != nil {
		return nil, err
	}
	return snapshotIdentityNames(identities), nil
}

func (s *Service) listHaSnapshotsRemote(ctx context.Context, target *clusterModels.BackupTarget, dataset string) ([]string, error) {
	identities, err := s.listHaSnapshotIdentitiesRemote(ctx, target, dataset)
	if err != nil {
		return nil, err
	}
	return snapshotIdentityNames(identities), nil
}

func filterHaSnapshots(output, dataset string) []string {
	var snaps []string
	scan := bufio.NewScanner(strings.NewReader(output))
	prefix := dataset + "@" + haSnapPrefix
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		short := strings.TrimPrefix(line, dataset+"@")
		if short != "" {
			snaps = append(snaps, short)
		}
	}
	return snaps
}

func (s *Service) ensureTargetParentDatasets(ctx context.Context, target *clusterModels.BackupTarget, targetPath string) error {
	targetPath = normalizeDatasetPath(targetPath)
	if targetPath == "" {
		return nil
	}

	idx := strings.LastIndex(targetPath, "/")
	if idx <= 0 || idx >= len(targetPath)-1 {
		return nil
	}
	parent := targetPath[:idx]

	exists, err := s.targetReplicationDatasetExists(ctx, target, parent)
	if err != nil {
		return fmt.Errorf("check_target_parent_dataset_exists_failed: %w", err)
	}
	if exists {
		return nil
	}

	_, createErr := s.runTargetSSH(ctx, target, "zfs", "create", "-p", "-o", "canmount=noauto", parent)
	if createErr != nil {
		if strings.Contains(strings.ToLower(createErr.Error()), "already exists") {
			return nil
		}
		return fmt.Errorf("create_target_parent_dataset_failed: %w", createErr)
	}

	logger.L.Info().
		Str("parent", parent).
		Str("target", target.SSHHost).
		Msg("replication_target_parent_dataset_created")

	return nil
}

func (s *Service) ensureTargetReplicationReadonly(ctx context.Context, target *clusterModels.BackupTarget, targetPath string) error {
	targetPath = normalizeDatasetPath(targetPath)
	if targetPath == "" {
		return nil
	}

	script := fmt.Sprintf(
		`set -eu
root_ds=%q
zfs set readonly=on "$root_ds"
zfs list -H -o name -r -t filesystem,volume "$root_ds" 2>/dev/null | while read -r ds; do
  [ "$ds" = "$root_ds" ] && continue
  zfs set readonly=on "$ds"
done`,
		targetPath,
	)

	output, err := s.runTargetSSH(ctx, target, "sh", "-c", script)
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}

	return nil
}

func (s *Service) verifyTargetReplicationReadonly(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	targetPath string,
) error {
	targetPath = normalizeDatasetPath(targetPath)
	if targetPath == "" {
		return fmt.Errorf("replication_target_dataset_required")
	}
	output, err := s.runTargetSSH(
		ctx,
		target,
		"zfs",
		"get",
		"-H",
		"-o",
		"value",
		"-r",
		"-t",
		"filesystem,volume",
		"readonly",
		targetPath,
	)
	if err != nil {
		return fmt.Errorf("verify_replication_readonly_failed: %s: %w", strings.TrimSpace(output), err)
	}
	if err := verifyReplicationReadonlyValues(output); err != nil {
		return fmt.Errorf("verify_replication_readonly_failed: %w", err)
	}
	return nil
}

func verifyReplicationReadonlyValues(output string) error {
	values := strings.Fields(output)
	if len(values) == 0 {
		return fmt.Errorf("replication_readonly_values_missing")
	}
	for _, value := range values {
		if value != "on" {
			return fmt.Errorf("replication_dataset_not_readonly:value=%s", value)
		}
	}
	return nil
}

func sortedReplicationPropertyNames(properties map[string]string) []string {
	names := make([]string, 0, len(properties))
	for property := range properties {
		names = append(names, property)
	}
	sort.Strings(names)
	return names
}

func (s *Service) setTargetReplicationProperties(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	dataset string,
	properties map[string]string,
) error {
	if target == nil {
		return fmt.Errorf("replication_target_required")
	}
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return fmt.Errorf("replication_target_dataset_required")
	}
	args := []string{"zfs", "set"}
	for _, property := range sortedReplicationPropertyNames(properties) {
		args = append(args, property+"="+properties[property])
	}
	args = append(args, dataset)
	output, err := s.runTargetSSH(ctx, target, args...)
	if err != nil {
		return fmt.Errorf("set_replication_provenance_failed: %s: %w", strings.TrimSpace(output), err)
	}
	return nil
}

func parseZFSPropertyValues(output string) map[string]string {
	values := make(map[string]string)
	scan := bufio.NewScanner(strings.NewReader(output))
	for scan.Scan() {
		fields := strings.Fields(strings.TrimSpace(scan.Text()))
		if len(fields) < 3 || fields[len(fields)-1] != "local" {
			continue
		}
		values[fields[0]] = strings.Join(fields[1:len(fields)-1], " ")
	}
	return values
}

func (s *Service) readTargetReplicationProperties(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	dataset string,
	propertyNames []string,
) (map[string]string, error) {
	if target == nil {
		return nil, fmt.Errorf("replication_target_required")
	}
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return nil, fmt.Errorf("replication_target_dataset_required")
	}
	propertyNames = append([]string{}, propertyNames...)
	sort.Strings(propertyNames)
	output, err := s.runTargetSSH(
		ctx,
		target,
		"zfs",
		"get",
		"-H",
		"-o",
		"property,value,source",
		strings.Join(propertyNames, ","),
		dataset,
	)
	if err != nil {
		return nil, fmt.Errorf("read_replication_provenance_failed: %s: %w", strings.TrimSpace(output), err)
	}
	return parseZFSPropertyValues(output), nil
}

func verifyReplicationPropertyValues(actual, expected map[string]string) error {
	for property, expectedValue := range expected {
		actualValue, exists := actual[property]
		if !exists || actualValue != expectedValue {
			return fmt.Errorf(
				"replication_provenance_mismatch:%s expected=%q actual=%q",
				property,
				expectedValue,
				actualValue,
			)
		}
	}
	return nil
}

func validateExactReplicationDatasetPath(dataset string) (string, error) {
	raw := strings.TrimSpace(dataset)
	normalized := normalizeDatasetPath(raw)
	if raw == "" || normalized == "" || raw != normalized || strings.HasPrefix(raw, "/") || strings.Contains(raw, "@") {
		return "", fmt.Errorf("invalid_exact_replication_dataset_path")
	}
	for _, component := range strings.Split(raw, "/") {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("invalid_exact_replication_dataset_path")
		}
		for _, r := range component {
			switch {
			case r >= 'a' && r <= 'z',
				r >= 'A' && r <= 'Z',
				r >= '0' && r <= '9',
				r == '.', r == '_', r == '-', r == ':', r == '%':
			default:
				return "", fmt.Errorf("invalid_exact_replication_dataset_path")
			}
		}
	}
	return normalized, nil
}

func parsePreviousReplicationDatasets(
	output string,
	targetDataset string,
) ([]replicationPreviousDatasetInfo, error) {
	targetDataset, err := validateExactReplicationDatasetPath(targetDataset)
	if err != nil {
		return nil, err
	}
	prefix := targetDataset + "_previous-"
	targetDepth := strings.Count(targetDataset, "/")
	results := make([]replicationPreviousDatasetInfo, 0)
	scan := bufio.NewScanner(strings.NewReader(output))
	for scan.Scan() {
		fields := strings.Fields(strings.TrimSpace(scan.Text()))
		if len(fields) == 0 || !strings.HasPrefix(fields[0], prefix) {
			continue
		}
		candidate, pathErr := validateExactReplicationDatasetPath(fields[0])
		if pathErr != nil || strings.Count(candidate, "/") != targetDepth {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid_replication_previous_dataset_listing:%s", candidate)
		}
		creation, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid_replication_previous_dataset_creation:%s", candidate)
		}
		results = append(results, replicationPreviousDatasetInfo{Name: candidate, Creation: creation})
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Creation == results[j].Creation {
			return results[i].Name > results[j].Name
		}
		return results[i].Creation > results[j].Creation
	})
	return results, nil
}

func splitPreviousReplicationRetention(
	datasets []replicationPreviousDatasetInfo,
	keep int,
) (kept []replicationPreviousDatasetInfo, removable []replicationPreviousDatasetInfo) {
	if keep < 0 {
		keep = 0
	}
	ordered := append([]replicationPreviousDatasetInfo{}, datasets...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Creation == ordered[j].Creation {
			return ordered[i].Name > ordered[j].Name
		}
		return ordered[i].Creation > ordered[j].Creation
	})
	if keep > len(ordered) {
		keep = len(ordered)
	}
	kept = append(kept, ordered[:keep]...)
	removable = append(removable, ordered[keep:]...)
	return kept, removable
}

func isLocallyProvenPreviousReplicationDataset(
	properties map[string]string,
	policyID uint,
	targetDataset string,
) bool {
	expected := map[string]string{
		replicationPropertyPolicyID: strconv.FormatUint(uint64(policyID), 10),
		replicationPropertyRole:     replicationRoleStandby,
		replicationPropertyTarget:   targetDataset,
	}
	return verifyReplicationPropertyValues(properties, expected) == nil
}

func buildDestroyProvenPreviousReplicationScript(
	previousDataset string,
	targetDataset string,
	policyID uint,
) string {
	return fmt.Sprintf(
		`set -eu
ds=%q
target=%q
expected_policy=%q
zfs list -H "$ds" >/dev/null 2>&1 || exit 0
[ "$(zfs get -H -o source %q "$ds")" = "local" ] || { echo "replication_previous_policy_not_local" >&2; exit 81; }
[ "$(zfs get -H -o value %q "$ds")" = "$expected_policy" ] || { echo "replication_previous_policy_mismatch" >&2; exit 81; }
[ "$(zfs get -H -o source %q "$ds")" = "local" ] || { echo "replication_previous_role_not_local" >&2; exit 82; }
[ "$(zfs get -H -o value %q "$ds")" = %q ] || { echo "replication_previous_role_invalid" >&2; exit 82; }
[ "$(zfs get -H -o source %q "$ds")" = "local" ] || { echo "replication_previous_target_not_local" >&2; exit 83; }
[ "$(zfs get -H -o value %q "$ds")" = "$target" ] || { echo "replication_previous_target_mismatch" >&2; exit 83; }
zfs get -H -o value -r -t filesystem,volume readonly "$ds" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || { echo "replication_previous_not_readonly" >&2; exit 84; }
zfs destroy -r "$ds"
`,
		previousDataset,
		targetDataset,
		strconv.FormatUint(uint64(policyID), 10),
		replicationPropertyPolicyID,
		replicationPropertyPolicyID,
		replicationPropertyRole,
		replicationPropertyRole,
		replicationRoleStandby,
		replicationPropertyTarget,
		replicationPropertyTarget,
	)
}

// CleanupPreviousReplicationGenerations removes only locally proven standby
// history for one exact target. Unknown or conflicting siblings never count
// against the retained proven generations.
func (s *Service) CleanupPreviousReplicationGenerations(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	targetDataset string,
	policyID uint,
	keep int,
) error {
	if target == nil {
		return fmt.Errorf("replication_target_required")
	}
	if policyID == 0 {
		return fmt.Errorf("replication_policy_id_required")
	}
	if keep < 0 {
		return fmt.Errorf("replication_previous_retention_invalid")
	}
	var err error
	targetDataset, err = validateExactReplicationDatasetPath(targetDataset)
	if err != nil {
		return err
	}
	parent := targetDataset
	if idx := strings.LastIndex(targetDataset, "/"); idx > 0 {
		parent = targetDataset[:idx]
	}
	output, err := s.runTargetSSH(
		ctx,
		target,
		"zfs",
		"list",
		"-H",
		"-p",
		"-r",
		"-t",
		"filesystem,volume",
		"-o",
		"name,creation",
		parent,
	)
	if err != nil {
		return fmt.Errorf("list_replication_previous_generations_failed: %s: %w", strings.TrimSpace(output), err)
	}
	candidates, err := parsePreviousReplicationDatasets(output, targetDataset)
	if err != nil {
		return err
	}
	proven := make([]replicationPreviousDatasetInfo, 0, len(candidates))
	propertyNames := []string{
		replicationPropertyPolicyID,
		replicationPropertyRole,
		replicationPropertyTarget,
		replicationPropertyRetainedAt,
	}
	for _, candidate := range candidates {
		properties, readErr := s.readTargetReplicationProperties(ctx, target, candidate.Name, propertyNames)
		if readErr != nil {
			return fmt.Errorf("read_replication_previous_generation_%s_failed: %w", candidate.Name, readErr)
		}
		if !isLocallyProvenPreviousReplicationDataset(properties, policyID, targetDataset) {
			continue
		}
		if retainedAt := strings.TrimSpace(properties[replicationPropertyRetainedAt]); retainedAt != "" {
			parsedRetainedAt, parseErr := strconv.ParseUint(retainedAt, 10, 64)
			if parseErr != nil {
				continue
			}
			candidate.Creation = parsedRetainedAt
		}
		proven = append(proven, candidate)
	}
	_, removable := splitPreviousReplicationRetention(proven, keep)
	for _, candidate := range removable {
		script := buildDestroyProvenPreviousReplicationScript(candidate.Name, targetDataset, policyID)
		destroyOutput, destroyErr := s.runTargetSSH(ctx, target, "sh", "-c", script)
		if destroyErr != nil {
			return fmt.Errorf(
				"destroy_replication_previous_generation_%s_failed: %s: %w",
				candidate.Name,
				strings.TrimSpace(destroyOutput),
				destroyErr,
			)
		}
	}
	return nil
}

func (s *Service) verifyExistingStagingDataset(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	stagingDataset string,
	opts ReplicationZFSTransferOptions,
	sourceDataset string,
	targetDataset string,
) error {
	expected := replicationProvenanceProperties(opts, sourceDataset, targetDataset, replicationStateReceiving)
	propertyNames := sortedReplicationPropertyNames(expected)
	actual, err := s.readTargetReplicationProperties(ctx, target, stagingDataset, propertyNames)
	if err != nil {
		return err
	}
	state := actual[replicationPropertyState]
	delete(expected, replicationPropertyState)
	if err := verifyReplicationPropertyValues(actual, expected); err != nil {
		return err
	}
	if state != replicationStateReceiving && state != replicationStateStaged {
		return fmt.Errorf("replication_staging_state_invalid:%s", state)
	}
	return nil
}

func (s *Service) verifyCompletedStagingDataset(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	stagingDataset string,
	opts ReplicationZFSTransferOptions,
	sourceDataset string,
	targetDataset string,
) error {
	expected := replicationProvenanceProperties(opts, sourceDataset, targetDataset, replicationStateStaged)
	propertyNames := append(sortedReplicationPropertyNames(expected), "readonly")
	actual, err := s.readTargetReplicationProperties(ctx, target, stagingDataset, propertyNames)
	if err != nil {
		return err
	}
	if err := verifyReplicationPropertyValues(actual, expected); err != nil {
		return err
	}
	if actual["readonly"] != "on" {
		return fmt.Errorf("replication_staging_dataset_not_readonly")
	}
	remoteIdentities, err := s.listHaSnapshotIdentitiesRemote(ctx, target, stagingDataset)
	if err != nil {
		return fmt.Errorf("list_staging_snapshot_identity_failed: %w", err)
	}
	remoteGUID, err := replicationSnapshotGUID(remoteIdentities, opts.SnapshotName)
	if err != nil {
		return err
	}
	if remoteGUID != strings.TrimSpace(opts.SnapshotGUID) {
		return fmt.Errorf(
			"replication_staging_snapshot_guid_mismatch expected=%s actual=%s",
			opts.SnapshotGUID,
			remoteGUID,
		)
	}
	return nil
}

func replicationCurrentStandbySeedProperties(
	policyID uint,
	sourceDataset string,
	targetDataset string,
) map[string]string {
	return map[string]string{
		replicationPropertyPolicyID: strconv.FormatUint(uint64(policyID), 10),
		replicationPropertySource:   normalizeDatasetPath(sourceDataset),
		replicationPropertyTarget:   normalizeDatasetPath(targetDataset),
		replicationPropertyRole:     replicationRoleStandby,
		replicationPropertyState:    replicationStateReady,
	}
}

func buildSeedReplicationStagingScript(
	currentDataset string,
	stagingDataset string,
	commonSnapshot string,
	commonGUID string,
	expectedCurrent map[string]string,
	receiveProperties map[string]string,
) string {
	var currentGuards strings.Builder
	for _, property := range sortedReplicationPropertyNames(expectedCurrent) {
		currentGuards.WriteString(fmt.Sprintf(
			"[ \"$(zfs get -H -o source %q \"$current\")\" = \"local\" ] || not_eligible %q\n"+
				"[ \"$(zfs get -H -o value %q \"$current\")\" = %q ] || not_eligible %q\n",
			property,
			"provenance_not_local:"+property,
			property,
			expectedCurrent[property],
			"provenance_mismatch:"+property,
		))
	}

	var receiveArgs strings.Builder
	var postReceiveGuards strings.Builder
	for _, property := range sortedReplicationPropertyNames(receiveProperties) {
		receiveArgs.WriteString(fmt.Sprintf(" -o %q", property+"="+receiveProperties[property]))
		postReceiveGuards.WriteString(fmt.Sprintf(
			"[ \"$(zfs get -H -o source %q \"$stage\")\" = \"local\" ] || { echo %q >&2; exit 96; }\n"+
				"[ \"$(zfs get -H -o value %q \"$stage\")\" = %q ] || { echo %q >&2; exit 97; }\n",
			property,
			"replication_staging_seed_provenance_not_local:"+property,
			property,
			receiveProperties[property],
			"replication_staging_seed_provenance_mismatch:"+property,
		))
	}

	return fmt.Sprintf(
		`set -eu
current=%q
stage=%q
snap=%q
expected_guid=%q
not_eligible() {
  echo "replication_staging_seed_not_eligible:$1"
  exit 0
}
zfs list -H "$current" >/dev/null 2>&1 || not_eligible "current_missing"
! zfs list -H "$stage" >/dev/null 2>&1 || { echo "replication_staging_seed_path_exists" >&2; exit 90; }
%szfs get -H -o value -r -t filesystem,volume readonly "$current" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || not_eligible "current_not_recursively_readonly"
zfs list -H -t snapshot "$current@$snap" >/dev/null 2>&1 || not_eligible "common_snapshot_missing"
[ "$(zfs get -H -o value guid "$current@$snap")" = "$expected_guid" ] || not_eligible "common_snapshot_guid_mismatch"
encryption_values=$(zfs get -H -o value -r encryption "$current") || { echo "replication_staging_seed_encryption_detection_failed" >&2; exit 91; }
if printf '%%s\n' "$encryption_values" | awk '$1 != "off" && $1 != "-" { encrypted=1 } END { exit encrypted ? 0 : 1 }'; then
  zfs send --raw -P -R "$current@$snap"
else
  zfs send -P -R -L -c -e "$current@$snap"
fi | zfs recv -u -x mountpoint -o canmount=noauto -o readonly=on%s "$stage"
zfs list -H -o name -r -t filesystem,volume "$stage" | while read -r ds; do
  zfs set readonly=on "$ds"
done
zfs get -H -o value -r -t filesystem,volume readonly "$stage" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || { echo "replication_staging_seed_not_readonly" >&2; exit 95; }
%szfs list -H -t snapshot "$stage@$snap" >/dev/null 2>&1 || { echo "replication_staging_seed_snapshot_missing" >&2; exit 98; }
[ "$(zfs get -H -o value guid "$stage@$snap")" = "$expected_guid" ] || { echo "replication_staging_seed_snapshot_guid_mismatch" >&2; exit 99; }
echo "replication_staging_seeded:$snap:$expected_guid"
`,
		currentDataset,
		stagingDataset,
		commonSnapshot,
		commonGUID,
		currentGuards.String(),
		receiveArgs.String(),
		postReceiveGuards.String(),
	)
}

func replicationDatasetMissingResult(output string, err error) bool {
	if err == nil {
		return false
	}
	// Only classify the command's captured ZFS output. Wrapped transport or
	// executable errors are deliberately excluded so they continue to fail
	// closed even if their diagnostic text happens to contain similar words.
	lower := strings.ToLower(strings.TrimSpace(output))
	return strings.Contains(lower, "dataset does not exist") ||
		strings.Contains(lower, "no such dataset") ||
		(strings.Contains(lower, "cannot open") && strings.Contains(lower, "does not exist"))
}

func replicationDatasetListedExactly(output, dataset string) bool {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return false
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == dataset {
			return true
		}
	}
	return false
}

func (s *Service) targetReplicationDatasetExists(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	dataset string,
) (bool, error) {
	if target == nil {
		return false, fmt.Errorf("replication_target_required")
	}
	dataset, err := validateExactReplicationDatasetPath(dataset)
	if err != nil {
		return false, err
	}
	output, err := s.runTargetSSH(
		ctx,
		target,
		"zfs",
		"list",
		"-H",
		"-o",
		"name",
		"-t",
		"filesystem,volume",
		"-d",
		"0",
		dataset,
	)
	if err != nil {
		if replicationDatasetMissingResult(output, err) {
			return false, nil
		}
		return false, fmt.Errorf("replication_target_dataset_lookup_failed: %s: %w", strings.TrimSpace(output), err)
	}
	return replicationDatasetListedExactly(output, dataset), nil
}

func emitReplicationOutput(onLine func(string), output string) {
	if onLine == nil {
		return
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			onLine(line)
		}
	}
}

func joinReplicationOutput(parts ...string) string {
	var joined strings.Builder
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if joined.Len() > 0 {
			joined.WriteByte('\n')
		}
		joined.WriteString(part)
	}
	return joined.String()
}

func (s *Service) trySeedFreshReplicationStaging(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	targetDataset string,
	stagingDataset string,
	opts ReplicationZFSTransferOptions,
) (replicationStagingSeedResult, error) {
	result := replicationStagingSeedResult{}
	var err error
	if sourceDataset, err = validateExactReplicationDatasetPath(sourceDataset); err != nil {
		return result, fmt.Errorf("invalid_replication_source_dataset: %w", err)
	}
	if targetDataset, err = validateExactReplicationDatasetPath(targetDataset); err != nil {
		return result, fmt.Errorf("invalid_replication_target_dataset: %w", err)
	}
	if stagingDataset, err = validateExactReplicationDatasetPath(stagingDataset); err != nil {
		return result, fmt.Errorf("invalid_replication_staging_dataset: %w", err)
	}

	commonSnapshot, commonErr := s.findCommonReplicationSnapshot(ctx, target, sourceDataset, targetDataset)
	if commonErr != nil {
		return result, fmt.Errorf("replication_staging_seed_common_snapshot_lookup_failed: %w", commonErr)
	}
	if commonSnapshot == "" {
		result.Output = "replication_staging_seed_skipped:no_common_snapshot"
		return result, nil
	}
	localIdentities, lookupErr := s.listHaSnapshotIdentitiesLocal(ctx, sourceDataset)
	if lookupErr != nil {
		return result, fmt.Errorf("replication_staging_seed_source_snapshot_lookup_failed: %w", lookupErr)
	}
	commonGUID, guidErr := replicationSnapshotGUID(localIdentities, commonSnapshot)
	if guidErr != nil {
		return result, fmt.Errorf("replication_staging_seed_source_snapshot_guid_failed: %w", guidErr)
	}
	localTree, treeErr := s.replicationSnapshotTreeManifestLocal(
		ctx,
		sourceDataset,
		sourceDataset,
		commonSnapshot,
	)
	if treeErr != nil {
		if isReplicationSnapshotTreeGenerationMismatch(treeErr) {
			result.Output = "replication_staging_seed_skipped:recursive_tree_mismatch"
			return result, nil
		}
		return result, fmt.Errorf("replication_staging_seed_source_tree_failed: %w", treeErr)
	}
	remoteTree, treeErr := s.replicationSnapshotTreeManifestRemote(
		ctx,
		target,
		targetDataset,
		sourceDataset,
		commonSnapshot,
	)
	if treeErr != nil {
		if isReplicationSnapshotTreeGenerationMismatch(treeErr) {
			result.Output = "replication_staging_seed_skipped:recursive_tree_mismatch"
			return result, nil
		}
		return result, fmt.Errorf("replication_staging_seed_target_tree_lookup_failed: %w", treeErr)
	}
	if !replicationSnapshotTreeManifestsEqual(localTree, remoteTree) {
		result.Output = "replication_staging_seed_skipped:recursive_tree_mismatch"
		return result, nil
	}

	receiveProperties := replicationProvenanceProperties(
		opts,
		sourceDataset,
		targetDataset,
		replicationStateReceiving,
	)
	script := buildSeedReplicationStagingScript(
		targetDataset,
		stagingDataset,
		commonSnapshot,
		commonGUID,
		replicationCurrentStandbySeedProperties(opts.PolicyID, sourceDataset, targetDataset),
		receiveProperties,
	)
	output, runErr := s.runTargetSSH(ctx, target, "sh", "-c", script)
	result.Output = strings.TrimSpace(output)
	if runErr != nil {
		return result, fmt.Errorf("seed_replication_staging_failed: %s: %w", result.Output, runErr)
	}
	if strings.Contains(result.Output, "replication_staging_seed_not_eligible:") {
		return result, nil
	}
	if !strings.Contains(result.Output, "replication_staging_seeded:") {
		return result, fmt.Errorf("seed_replication_staging_outcome_ambiguous")
	}

	if err := s.verifyExistingStagingDataset(ctx, target, stagingDataset, opts, sourceDataset, targetDataset); err != nil {
		return result, fmt.Errorf("verify_seeded_replication_staging_provenance_failed: %w", err)
	}
	if err := s.verifyTargetReplicationReadonly(ctx, target, stagingDataset); err != nil {
		return result, fmt.Errorf("verify_seeded_replication_staging_readonly_failed: %w", err)
	}
	remoteIdentities, listErr := s.listHaSnapshotIdentitiesRemote(ctx, target, stagingDataset)
	if listErr != nil {
		return result, fmt.Errorf("verify_seeded_replication_staging_snapshot_failed: %w", listErr)
	}
	remoteGUID, remoteGUIDErr := replicationSnapshotGUID(remoteIdentities, commonSnapshot)
	if remoteGUIDErr != nil || remoteGUID != commonGUID {
		return result, fmt.Errorf(
			"verify_seeded_replication_staging_snapshot_guid_failed expected=%s actual=%s: %v",
			commonGUID,
			remoteGUID,
			remoteGUIDErr,
		)
	}
	seededTree, treeErr := s.replicationSnapshotTreeManifestRemote(
		ctx,
		target,
		stagingDataset,
		sourceDataset,
		commonSnapshot,
	)
	if treeErr != nil || !replicationSnapshotTreeManifestsEqual(localTree, seededTree) {
		return result, fmt.Errorf("verify_seeded_replication_staging_tree_failed: %v", treeErr)
	}
	result.Seeded = true
	result.CommonSnapshot = commonSnapshot
	result.CommonGUID = commonGUID
	return result, nil
}

func buildDestroyProvenStagingScript(
	stagingDataset string,
	expected map[string]string,
) string {
	propertyNames := sortedReplicationPropertyNames(expected)
	var guards strings.Builder
	for _, property := range propertyNames {
		guards.WriteString(fmt.Sprintf(
			"[ \"$(zfs get -H -o source %q \"$ds\")\" = \"local\" ] || { echo %q >&2; exit 40; }\n"+
				"[ \"$(zfs get -H -o value %q \"$ds\")\" = %q ] || { echo %q >&2; exit 41; }\n",
			property,
			"replication_provenance_not_local:"+property,
			property,
			expected[property],
			"replication_provenance_mismatch:"+property,
		))
	}
	return fmt.Sprintf(
		"set -eu\nds=%q\nzfs list -H \"$ds\" >/dev/null 2>&1 || exit 0\n%s"+
			"zfs destroy -r \"$ds\"\n",
		stagingDataset,
		guards.String(),
	)
}

func (s *Service) destroyProvenStagingDataset(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	stagingDataset string,
	opts ReplicationZFSTransferOptions,
	sourceDataset string,
	targetDataset string,
) error {
	expected := replicationProvenanceProperties(opts, sourceDataset, targetDataset, replicationStateReceiving)
	delete(expected, replicationPropertyState)
	script := buildDestroyProvenStagingScript(stagingDataset, expected)
	output, err := s.runTargetSSH(ctx, target, "sh", "-c", script)
	if err != nil {
		return fmt.Errorf("destroy_proven_staging_dataset_failed: %s: %w", strings.TrimSpace(output), err)
	}
	return nil
}

func (s *Service) cleanupExactReplicationStagingAfterFailure(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	stagingDataset string,
	opts ReplicationZFSTransferOptions,
	sourceDataset string,
	targetDataset string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), replicationControlDefaultTimeout)
	defer cancel()

	expected := replicationProvenanceProperties(opts, sourceDataset, targetDataset, replicationStateReceiving)
	delete(expected, replicationPropertyState)
	script := buildAbortAndDestroyProvenStagingScript(stagingDataset, expected)
	output, err := s.runTargetSSH(cleanupCtx, target, "sh", "-c", script)
	if err != nil {
		return fmt.Errorf("cleanup_proven_replication_staging_failed: %s: %w", strings.TrimSpace(output), err)
	}
	return nil
}

func buildAbortAndDestroyProvenStagingScript(stagingDataset string, expected map[string]string) string {
	propertyNames := sortedReplicationPropertyNames(expected)
	var proof strings.Builder
	for _, property := range propertyNames {
		proof.WriteString(fmt.Sprintf(
			"  [ \"$(zfs get -H -o source %q \"$ds\")\" = \"local\" ] || return 1\n"+
				"  [ \"$(zfs get -H -o value %q \"$ds\")\" = %q ] || return 1\n",
			property,
			property,
			expected[property],
		))
	}
	return fmt.Sprintf(
		"set -eu\nds=%q\nzfs list -H \"$ds\" >/dev/null 2>&1 || exit 0\n"+
			"proven() {\n%s}\n"+
			"proven || { echo replication_provenance_mismatch_before_abort >&2; exit 40; }\n"+
			"zfs receive -A \"$ds\" >/dev/null 2>&1 || true\n"+
			"zfs list -H \"$ds\" >/dev/null 2>&1 || exit 0\n"+
			"proven || { echo replication_provenance_mismatch_after_abort >&2; exit 41; }\n"+
			"zfs destroy -r \"$ds\"\n",
		stagingDataset,
		proof.String(),
	)
}

func buildCleanupStaleReplicationStagingScript(
	targetDataset string,
	policyID uint,
	maxOwnerEpoch uint64,
	keepRunID string,
) string {
	parent := targetDataset
	leaf := targetDataset
	if idx := strings.LastIndex(targetDataset, "/"); idx > 0 {
		parent = targetDataset[:idx]
		leaf = targetDataset[idx+1:]
	}
	return fmt.Sprintf(
		`set -eu
parent=%q
prefix=%q
expected_policy=%q
expected_target=%q
max_epoch=%q
keep_run=%q
property_local() {
  [ "$(zfs get -H -o source "$1" "$2" 2>/dev/null)" = "local" ]
}
candidate_proven() {
  ds="$1"
  property_local %q "$ds" || return 1
  [ "$(zfs get -H -o value %q "$ds")" = "$expected_policy" ] || return 1
  property_local %q "$ds" || return 1
  [ "$(zfs get -H -o value %q "$ds")" = "$expected_target" ] || return 1
  property_local %q "$ds" || return 1
  [ "$(zfs get -H -o value %q "$ds")" = %q ] || return 1
  property_local %q "$ds" || return 1
  run="$(zfs get -H -o value %q "$ds")"
  [ -n "$run" ] && [ "$run" != "-" ] && [ "$run" != "$keep_run" ] || return 1
  property_local %q "$ds" || return 1
  epoch="$(zfs get -H -o value %q "$ds")"
  case "$epoch" in *[!0-9]*|'') return 1 ;; esac
  [ "$epoch" -le "$max_epoch" ] || return 1
  property_local %q "$ds" || return 1
  state="$(zfs get -H -o value %q "$ds")"
  case "$state" in %q|%q|%q) ;; *) return 1 ;; esac
}
zfs list -H -d 1 -o name -t filesystem,volume "$parent" 2>/dev/null | while read -r candidate; do
  case "$candidate" in "$parent/$prefix"*) ;; *) continue ;; esac
  candidate_proven "$candidate" || continue
  zfs receive -A "$candidate" >/dev/null 2>&1 || true
  zfs list -H "$candidate" >/dev/null 2>&1 || continue
  candidate_proven "$candidate" || continue
  zfs destroy -r "$candidate"
done`,
		parent,
		leaf+"_gen-",
		strconv.FormatUint(uint64(policyID), 10),
		targetDataset,
		strconv.FormatUint(maxOwnerEpoch, 10),
		strings.TrimSpace(keepRunID),
		replicationPropertyPolicyID,
		replicationPropertyPolicyID,
		replicationPropertyTarget,
		replicationPropertyTarget,
		replicationPropertyRole,
		replicationPropertyRole,
		replicationRoleStandby,
		replicationPropertyRunID,
		replicationPropertyRunID,
		replicationPropertyOwnerEpoch,
		replicationPropertyOwnerEpoch,
		replicationPropertyState,
		replicationPropertyState,
		replicationStateReceiving,
		replicationStateStaged,
		replicationStateReady,
	)
}

// cleanupStaleReplicationStagingGenerations removes abandoned generations
// only after a newer complete generation has committed. Selection by name is
// never sufficient: the remote script re-proves local policy, target, role,
// run, epoch, and state properties immediately before each recursive destroy.
func (s *Service) cleanupStaleReplicationStagingGenerations(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	targetDataset string,
	policyID uint,
	maxOwnerEpoch uint64,
	keepRunID string,
) error {
	if target == nil || policyID == 0 || maxOwnerEpoch == 0 || strings.TrimSpace(keepRunID) == "" {
		return fmt.Errorf("invalid_replication_staging_cleanup_identity")
	}
	var err error
	targetDataset, err = validateExactReplicationDatasetPath(targetDataset)
	if err != nil {
		return err
	}
	script := buildCleanupStaleReplicationStagingScript(
		targetDataset,
		policyID,
		maxOwnerEpoch,
		keepRunID,
	)
	output, err := s.runTargetSSH(ctx, target, "sh", "-c", script)
	if err != nil {
		return fmt.Errorf("cleanup_stale_replication_staging_failed: %s: %w", strings.TrimSpace(output), err)
	}
	return nil
}

// ReplicationZFSSendStaged receives into a provenance-tagged generation dataset
// and leaves the current target untouched. Call PromoteStagedReplicationDataset
// only after every required root has staged successfully.
func (s *Service) ReplicationZFSSendStaged(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	opts ReplicationZFSTransferOptions,
	onLine func(string),
) (ReplicationStagedTransferResult, string, error) {
	result := ReplicationStagedTransferResult{}
	if target == nil {
		return result, "", fmt.Errorf("replication_target_required")
	}
	if err := opts.validate(); err != nil {
		return result, "", err
	}
	sourceDataset = normalizeDatasetPath(sourceDataset)
	if sourceDataset == "" {
		return result, "", fmt.Errorf("source_dataset_required")
	}
	targetDataset := targetDatasetPath(target.BackupRoot, destSuffix)
	if targetDataset == "" {
		return result, "", fmt.Errorf("replication_target_dataset_required")
	}
	stagingDataset, err := replicationStagingDatasetPath(targetDataset, opts)
	if err != nil {
		return result, "", err
	}
	result = ReplicationStagedTransferResult{
		SnapshotName:   strings.TrimSpace(opts.SnapshotName),
		TargetDataset:  targetDataset,
		StagingDataset: stagingDataset,
	}

	if !opts.SnapshotAlreadyCreated {
		if err := s.CreateReplicationSnapshotGroup(ctx, []string{sourceDataset}, opts.SnapshotName); err != nil {
			return result, "", fmt.Errorf("replication_snapshot_failed: %w", err)
		}
	}
	opts, err = s.bindReplicationSnapshotGUID(ctx, sourceDataset, opts)
	if err != nil {
		return result, "", fmt.Errorf("replication_snapshot_manifest_failed: %w", err)
	}
	result.SnapshotGUID = opts.SnapshotGUID
	if err := s.ensureTargetParentDatasets(ctx, target, stagingDataset); err != nil {
		return result, "", err
	}
	exists, err := s.targetReplicationDatasetExists(ctx, target, stagingDataset)
	if err != nil {
		return result, "", fmt.Errorf("replication_staging_dataset_lookup_failed: %w", err)
	}
	seedOutput := ""
	if exists {
		if err := s.verifyExistingStagingDataset(ctx, target, stagingDataset, opts, sourceDataset, targetDataset); err != nil {
			return result, "", fmt.Errorf("replication_staging_dataset_unproven: %w", err)
		}
	} else {
		seedResult, seedErr := s.trySeedFreshReplicationStaging(
			ctx,
			target,
			sourceDataset,
			targetDataset,
			stagingDataset,
			opts,
		)
		seedOutput = seedResult.Output
		emitReplicationOutput(onLine, seedOutput)
		if seedErr != nil {
			return result, seedOutput, seedErr
		}
	}

	receiveProperties := replicationProvenanceProperties(
		opts,
		sourceDataset,
		targetDataset,
		replicationStateReceiving,
	)
	output, err := s.replicationZFSSendPreparedToPath(
		ctx,
		target,
		sourceDataset,
		destSuffix,
		stagingDataset,
		opts.SnapshotName,
		receiveProperties,
		true,
		func() error {
			return s.verifyExistingStagingDataset(
				ctx,
				target,
				stagingDataset,
				opts,
				sourceDataset,
				targetDataset,
			)
		},
		func() error {
			return s.destroyProvenStagingDataset(
				ctx,
				target,
				stagingDataset,
				opts,
				sourceDataset,
				targetDataset,
			)
		},
		onLine,
	)
	output = joinReplicationOutput(seedOutput, output)
	if err != nil {
		return result, output, err
	}

	stagedProperties := replicationProvenanceProperties(
		opts,
		sourceDataset,
		targetDataset,
		replicationStateStaged,
	)
	if err := s.setTargetReplicationProperties(ctx, target, stagingDataset, stagedProperties); err != nil {
		return result, output, err
	}
	if err := s.verifyCompletedStagingDataset(ctx, target, stagingDataset, opts, sourceDataset, targetDataset); err != nil {
		return result, output, fmt.Errorf("replication_staging_verification_failed: %w", err)
	}
	return result, output, nil
}

func buildPromoteStagedReplicationScript(
	stagingDataset string,
	targetDataset string,
	previousDataset string,
	expectedStage map[string]string,
	policyID string,
) string {
	propertyNames := sortedReplicationPropertyNames(expectedStage)
	var stageGuards strings.Builder
	var targetRecoveryGuards strings.Builder
	for _, property := range propertyNames {
		stageGuards.WriteString(fmt.Sprintf(
			"[ \"$(zfs get -H -o source %q \"$stage\")\" = \"local\" ] || { echo %q >&2; exit 49; }\n"+
				"[ \"$(zfs get -H -o value %q \"$stage\")\" = %q ] || { echo %q >&2; exit 51; }\n",
			property,
			"replication_staging_provenance_not_local:"+property,
			property,
			expectedStage[property],
			"replication_staging_provenance_mismatch:"+property,
		))
		if property != replicationPropertyState {
			targetRecoveryGuards.WriteString(fmt.Sprintf(
				"[ \"$(zfs get -H -o source %q \"$target\")\" = \"local\" ] || { echo %q >&2; exit 59; }\n"+
					"[ \"$(zfs get -H -o value %q \"$target\")\" = %q ] || { echo %q >&2; exit 59; }\n",
				property,
				"replication_promoted_recovery_provenance_not_local:"+property,
				property,
				expectedStage[property],
				"replication_promoted_recovery_provenance_mismatch:"+property,
			))
		}
	}

	return fmt.Sprintf(
		`set -eu
stage=%q
target=%q
previous=%q
verify_ha_snapshot_tree() {
  snapshot_root=$1
  snapshot_label=$2
  snapshots=$(zfs list -H -r -t snapshot -o name "$snapshot_root") || { echo "${snapshot_label}_snapshot_verify_list_failed" >&2; exit 63; }
  printf '%%s\n' "$snapshots" | while IFS= read -r snapshot; do
    [ -n "$snapshot" ] || continue
    case "$snapshot" in "$snapshot_root"@*|"$snapshot_root"/*@*) ;; *) echo "${snapshot_label}_snapshot_verify_outside_tree:$snapshot" >&2; exit 64 ;; esac
    case "${snapshot##*@}" in ha_*) ;; *) echo "${snapshot_label}_snapshot_verify_failed:$snapshot" >&2; exit 65 ;; esac
  done
}
if ! zfs list -H "$stage" >/dev/null 2>&1 && zfs list -H "$target" >/dev/null 2>&1; then
%s  [ "$(zfs get -H -o source %q "$target")" = "local" ] || { echo "replication_promoted_recovery_state_not_local" >&2; exit 59; }
  recovered_state=$(zfs get -H -o value %q "$target")
  [ "$recovered_state" = %q ] || [ "$recovered_state" = %q ] || { echo "replication_promoted_recovery_state_invalid" >&2; exit 59; }
  zfs get -H -o value -r -t filesystem,volume readonly "$target" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || { echo "replication_promoted_recovery_not_readonly" >&2; exit 59; }
  verify_ha_snapshot_tree "$target" replication_promoted_recovery
  if [ "$recovered_state" = %q ]; then zfs set %q=%q "$target"; fi
  echo "promoted_dataset=$target"
  exit 0
fi
zfs list -H "$stage" >/dev/null 2>&1 || { echo "replication_staging_dataset_missing" >&2; exit 50; }
stage_proven() {
%s}
stage_proven
zfs get -H -o value -r -t filesystem,volume readonly "$stage" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || { echo "replication_staging_dataset_not_readonly" >&2; exit 52; }
moved_current=0
promoted_new=0
rollback_promotion() {
  trap - EXIT HUP INT TERM
  if [ "$promoted_new" -eq 1 ] && zfs list -H "$target" >/dev/null 2>&1; then
    zfs rename "$target" "$stage" || true
    zfs set %q=%q "$stage" || true
    promoted_new=0
  fi
  if [ "$moved_current" -eq 1 ] && ! zfs list -H "$target" >/dev/null 2>&1; then
    zfs rename "$previous" "$target" || true
    moved_current=0
  fi
}
trap rollback_promotion EXIT HUP INT TERM
if zfs list -H "$target" >/dev/null 2>&1; then
  [ "$(zfs get -H -o source %q "$target")" = "local" ] || { echo "replication_current_policy_not_local" >&2; exit 53; }
  [ "$(zfs get -H -o value %q "$target")" = %q ] || { echo "replication_current_policy_mismatch" >&2; exit 53; }
  [ "$(zfs get -H -o source %q "$target")" = "local" ] || { echo "replication_current_role_not_local" >&2; exit 54; }
  [ "$(zfs get -H -o value %q "$target")" = %q ] || { echo "replication_current_role_invalid" >&2; exit 54; }
  zfs get -H -o value -r -t filesystem,volume readonly "$target" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || { echo "replication_current_dataset_not_readonly" >&2; exit 55; }
  ! zfs list -H "$previous" >/dev/null 2>&1 || { echo "replication_previous_generation_exists" >&2; exit 56; }
fi
stage_snapshots=$(zfs list -H -r -t snapshot -o name "$stage") || { echo "replication_staging_snapshot_list_failed" >&2; exit 60; }
printf '%%s\n' "$stage_snapshots" | while IFS= read -r snapshot; do
  [ -n "$snapshot" ] || continue
  stage_proven
  case "$snapshot" in "$stage"@*|"$stage"/*@*) ;; *) echo "replication_staging_snapshot_outside_tree:$snapshot" >&2; exit 61 ;; esac
  case "${snapshot##*@}" in ha_*) ;; *) zfs destroy "$snapshot" || { echo "replication_staging_snapshot_destroy_failed:$snapshot" >&2; exit 62; } ;; esac
done
stage_proven
verify_ha_snapshot_tree "$stage" replication_staging
if zfs list -H "$target" >/dev/null 2>&1; then
  zfs rename "$target" "$previous"
  moved_current=1
  zfs set %q="$(date +%%s)" "$previous"
fi
if ! zfs rename "$stage" "$target"; then
  exit 57
fi
promoted_new=1
if ! zfs set %q=%q "$target"; then
  exit 58
fi
trap - EXIT HUP INT TERM
if [ "$moved_current" -eq 1 ]; then echo "previous_dataset=$previous"; fi
echo "promoted_dataset=$target"
`,
		stagingDataset,
		targetDataset,
		previousDataset,
		targetRecoveryGuards.String(),
		replicationPropertyState,
		replicationPropertyState,
		replicationStateStaged,
		replicationStateReady,
		replicationStateStaged,
		replicationPropertyState,
		replicationStateReady,
		stageGuards.String(),
		replicationPropertyState,
		replicationStateStaged,
		replicationPropertyPolicyID,
		replicationPropertyPolicyID,
		policyID,
		replicationPropertyRole,
		replicationPropertyRole,
		replicationRoleStandby,
		replicationPropertyRetainedAt,
		replicationPropertyState,
		replicationStateReady,
	)
}

// PromoteStagedReplicationDataset validates provenance and readonly fencing in
// the same target-side script that performs the rename. The previous target is
// retained and restored automatically if the staging rename or final tag fails.
func (s *Service) PromoteStagedReplicationDataset(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	opts ReplicationZFSTransferOptions,
) error {
	if target == nil {
		return fmt.Errorf("replication_target_required")
	}
	if err := opts.validate(); err != nil {
		return err
	}
	sourceDataset = normalizeDatasetPath(sourceDataset)
	if sourceDataset == "" {
		return fmt.Errorf("source_dataset_required")
	}
	opts, err := s.ensureReplicationSnapshotGUID(ctx, sourceDataset, opts)
	if err != nil {
		return fmt.Errorf("replication_snapshot_manifest_failed: %w", err)
	}
	targetDataset := targetDatasetPath(target.BackupRoot, destSuffix)
	stagingDataset, err := replicationStagingDatasetPath(targetDataset, opts)
	if err != nil {
		return err
	}
	previousDataset, err := replicationPreviousDatasetPath(targetDataset, opts)
	if err != nil {
		return err
	}

	expectedStage := replicationProvenanceProperties(
		opts,
		sourceDataset,
		targetDataset,
		replicationStateStaged,
	)
	script := buildPromoteStagedReplicationScript(
		stagingDataset,
		targetDataset,
		previousDataset,
		expectedStage,
		strconv.FormatUint(uint64(opts.PolicyID), 10),
	)
	output, err := s.runTargetSSH(ctx, target, "sh", "-c", script)
	if err != nil {
		return fmt.Errorf("promote_staged_replication_dataset_failed: %s: %w", strings.TrimSpace(output), err)
	}
	return nil
}

func buildRollbackPromotedReplicationScript(
	stagingDataset string,
	targetDataset string,
	previousDataset string,
	expectedCurrent map[string]string,
	policyID string,
) string {
	propertyNames := sortedReplicationPropertyNames(expectedCurrent)
	var currentGuards strings.Builder
	var stageRecoveryGuards strings.Builder
	for _, property := range propertyNames {
		currentGuards.WriteString(fmt.Sprintf(
			"[ \"$(zfs get -H -o source %q \"$target\")\" = \"local\" ] || { echo %q >&2; exit 61; }\n"+
				"[ \"$(zfs get -H -o value %q \"$target\")\" = %q ] || { echo %q >&2; exit 62; }\n",
			property,
			"replication_rollback_current_provenance_not_local:"+property,
			property,
			expectedCurrent[property],
			"replication_rollback_current_provenance_mismatch:"+property,
		))
		if property != replicationPropertyState {
			stageRecoveryGuards.WriteString(fmt.Sprintf(
				"[ \"$(zfs get -H -o source %q \"$stage\")\" = \"local\" ] || { echo %q >&2; exit 68; }\n"+
					"[ \"$(zfs get -H -o value %q \"$stage\")\" = %q ] || { echo %q >&2; exit 68; }\n",
				property,
				"replication_rollback_stage_recovery_provenance_not_local:"+property,
				property,
				expectedCurrent[property],
				"replication_rollback_stage_recovery_provenance_mismatch:"+property,
			))
		}
	}

	return fmt.Sprintf(
		`set -eu
stage=%q
target=%q
previous=%q
if zfs list -H "$stage" >/dev/null 2>&1; then
%s  [ "$(zfs get -H -o source %q "$stage")" = "local" ] || { echo "replication_rollback_stage_recovery_state_not_local" >&2; exit 68; }
  stage_state=$(zfs get -H -o value %q "$stage")
  [ "$stage_state" = %q ] || [ "$stage_state" = %q ] || { echo "replication_rollback_stage_recovery_state_invalid" >&2; exit 68; }
  zfs set %q=%q "$stage"
  zfs get -H -o value -r -t filesystem,volume readonly "$stage" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || { echo "replication_rollback_stage_recovery_not_readonly" >&2; exit 68; }
  if zfs list -H "$target" >/dev/null 2>&1; then
    target_run=$(zfs get -H -o value %q "$target" 2>/dev/null || true)
    [ "$target_run" != %q ] || { echo "replication_rollback_recovery_duplicate_candidate" >&2; exit 69; }
    [ "$(zfs get -H -o source %q "$target")" = "local" ] || { echo "replication_rollback_recovered_policy_not_local" >&2; exit 69; }
    [ "$(zfs get -H -o value %q "$target")" = %q ] || { echo "replication_rollback_recovered_policy_mismatch" >&2; exit 69; }
    [ "$(zfs get -H -o source %q "$target")" = "local" ] || { echo "replication_rollback_recovered_role_not_local" >&2; exit 69; }
    [ "$(zfs get -H -o value %q "$target")" = %q ] || { echo "replication_rollback_recovered_role_invalid" >&2; exit 69; }
    zfs get -H -o value -r -t filesystem,volume readonly "$target" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || { echo "replication_rollback_recovered_not_readonly" >&2; exit 69; }
    echo "restored_previous_dataset=$target"
    echo "rolled_back_generation=$stage"
    exit 0
  fi
  if zfs list -H "$previous" >/dev/null 2>&1; then
    [ "$(zfs get -H -o source %q "$previous")" = "local" ] || { echo "replication_rollback_previous_policy_not_local" >&2; exit 65; }
    [ "$(zfs get -H -o value %q "$previous")" = %q ] || { echo "replication_rollback_previous_policy_mismatch" >&2; exit 65; }
    [ "$(zfs get -H -o source %q "$previous")" = "local" ] || { echo "replication_rollback_previous_role_not_local" >&2; exit 66; }
    [ "$(zfs get -H -o value %q "$previous")" = %q ] || { echo "replication_rollback_previous_role_invalid" >&2; exit 66; }
    zfs get -H -o value -r -t filesystem,volume readonly "$previous" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || { echo "replication_rollback_previous_not_readonly" >&2; exit 67; }
    zfs rename "$previous" "$target"
    echo "restored_previous_dataset=$target"
  else
    echo "rollback_without_previous_dataset=$stage"
  fi
  echo "rolled_back_generation=$stage"
  exit 0
fi
zfs list -H "$target" >/dev/null 2>&1 || { echo "replication_rollback_current_missing" >&2; exit 60; }
! zfs list -H "$stage" >/dev/null 2>&1 || { echo "replication_rollback_staging_exists" >&2; exit 63; }
%szfs get -H -o value -r -t filesystem,volume readonly "$target" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || { echo "replication_rollback_current_not_readonly" >&2; exit 64; }
previous_exists=0
if zfs list -H "$previous" >/dev/null 2>&1; then
  previous_exists=1
  [ "$(zfs get -H -o source %q "$previous")" = "local" ] || { echo "replication_rollback_previous_policy_not_local" >&2; exit 65; }
  [ "$(zfs get -H -o value %q "$previous")" = %q ] || { echo "replication_rollback_previous_policy_mismatch" >&2; exit 65; }
  [ "$(zfs get -H -o source %q "$previous")" = "local" ] || { echo "replication_rollback_previous_role_not_local" >&2; exit 66; }
  [ "$(zfs get -H -o value %q "$previous")" = %q ] || { echo "replication_rollback_previous_role_invalid" >&2; exit 66; }
  zfs get -H -o value -r -t filesystem,volume readonly "$previous" | awk 'NF { seen=1; if ($1 != "on") bad=1 } END { exit (!seen || bad) }' || { echo "replication_rollback_previous_not_readonly" >&2; exit 67; }
fi
moved_new=0
restored_previous=0
undo_rollback() {
  trap - EXIT HUP INT TERM
  if [ "$restored_previous" -eq 1 ] && zfs list -H "$target" >/dev/null 2>&1; then
    zfs rename "$target" "$previous" || true
    restored_previous=0
  fi
  if [ "$moved_new" -eq 1 ] && zfs list -H "$stage" >/dev/null 2>&1; then
    zfs rename "$stage" "$target" || true
    zfs set %q=%q "$target" || true
    moved_new=0
  fi
}
trap undo_rollback EXIT HUP INT TERM
zfs rename "$target" "$stage"
moved_new=1
zfs set %q=%q "$stage"
if [ "$previous_exists" -eq 1 ]; then
  zfs rename "$previous" "$target"
  restored_previous=1
  echo "restored_previous_dataset=$target"
else
  echo "rollback_without_previous_dataset=$stage"
fi
trap - EXIT HUP INT TERM
echo "rolled_back_generation=$stage"
`,
		stagingDataset,
		targetDataset,
		previousDataset,
		stageRecoveryGuards.String(),
		replicationPropertyState,
		replicationPropertyState,
		replicationStateReady,
		replicationStateStaged,
		replicationPropertyState,
		replicationStateStaged,
		replicationPropertyRunID,
		expectedCurrent[replicationPropertyRunID],
		replicationPropertyPolicyID,
		replicationPropertyPolicyID,
		policyID,
		replicationPropertyRole,
		replicationPropertyRole,
		replicationRoleStandby,
		replicationPropertyPolicyID,
		replicationPropertyPolicyID,
		policyID,
		replicationPropertyRole,
		replicationPropertyRole,
		replicationRoleStandby,
		currentGuards.String(),
		replicationPropertyPolicyID,
		replicationPropertyPolicyID,
		policyID,
		replicationPropertyRole,
		replicationPropertyRole,
		replicationRoleStandby,
		replicationPropertyState,
		replicationStateReady,
		replicationPropertyState,
		replicationStateStaged,
	)
}

// RollbackPromotedReplicationDataset reverses one successful root promotion so
// a caller can roll back roots 1..N-1 when root N fails during a multi-root
// commit. It verifies the promoted generation exactly, preserves it under the
// staging name, and restores the retained previous generation when present.
func (s *Service) RollbackPromotedReplicationDataset(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	opts ReplicationZFSTransferOptions,
) error {
	if target == nil {
		return fmt.Errorf("replication_target_required")
	}
	if err := opts.validate(); err != nil {
		return err
	}
	sourceDataset = normalizeDatasetPath(sourceDataset)
	if sourceDataset == "" {
		return fmt.Errorf("source_dataset_required")
	}
	var err error
	opts, err = s.ensureReplicationSnapshotGUID(ctx, sourceDataset, opts)
	if err != nil {
		return fmt.Errorf("replication_snapshot_manifest_failed: %w", err)
	}
	targetDataset := targetDatasetPath(target.BackupRoot, destSuffix)
	stagingDataset, err := replicationStagingDatasetPath(targetDataset, opts)
	if err != nil {
		return err
	}
	previousDataset, err := replicationPreviousDatasetPath(targetDataset, opts)
	if err != nil {
		return err
	}

	expectedCurrent := replicationProvenanceProperties(
		opts,
		sourceDataset,
		targetDataset,
		replicationStateReady,
	)
	script := buildRollbackPromotedReplicationScript(
		stagingDataset,
		targetDataset,
		previousDataset,
		expectedCurrent,
		strconv.FormatUint(uint64(opts.PolicyID), 10),
	)
	output, err := s.runTargetSSH(ctx, target, "sh", "-c", script)
	if err != nil {
		return fmt.Errorf("rollback_promoted_replication_dataset_failed: %s: %w", strings.TrimSpace(output), err)
	}
	return nil
}

func (s *Service) destroyLocalSnapshotBestEffort(ctx context.Context, dataset, snapName string) error {
	dsSnap := normalizeDatasetPath(dataset) + "@" + snapName
	output, err := utils.RunCommandWithContext(ctx, "zfs", "destroy", "-r", dsSnap)
	if err != nil {
		if localSnapshotMissingResult(output, err) {
			return nil
		}
		return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	return nil
}

func localSnapshotMissingResult(output string, err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(output))
	return strings.Contains(lower, "snapshot does not exist") ||
		strings.Contains(lower, "no such snapshot") ||
		strings.Contains(lower, "snapshot not found") ||
		strings.Contains(lower, "dataset does not exist") ||
		strings.Contains(lower, "no such dataset") ||
		strings.Contains(lower, "could not find any snapshots to destroy")
}

func (s *Service) isDatasetEncrypted(ctx context.Context, dataset string) (bool, error) {
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return false, fmt.Errorf("dataset_required")
	}

	output, err := utils.RunCommandWithContext(ctx, "zfs", "get", "-H", "-r", "-o", "value", "encryption", dataset)
	if err != nil {
		return false, fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	return replicationEncryptionValuesRequireRaw(output)
}

func replicationEncryptionValuesRequireRaw(output string) (bool, error) {
	seen := false
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		value := strings.TrimSpace(scanner.Text())
		if value == "" {
			continue
		}
		seen = true
		if value != "off" && value != "-" {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	if !seen {
		return false, fmt.Errorf("replication_encryption_state_missing")
	}
	return false, nil
}

func (s *Service) retainReplicationSnapshots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	destSuffix string,
	keep int,
) error {
	if keep <= 0 {
		keep = defaultReplicationPruneKeepLast
	}

	sourceSnaps, err := s.listHaSnapshotsLocal(ctx, sourceDataset)
	if err != nil {
		return fmt.Errorf("list_source_snapshots_failed: %w", err)
	}

	targetPath := targetDatasetPath(target.BackupRoot, destSuffix)
	targetSnaps, err := s.listHaSnapshotsRemote(ctx, target, targetPath)
	if err != nil {
		return fmt.Errorf("list_target_snapshots_failed: %w", err)
	}

	common := intersectSnapshotNames(sourceSnaps, targetSnaps)
	if len(common) <= keep {
		return nil
	}

	stale := common[:len(common)-keep]
	var errs []string
	for _, snap := range stale {
		if err := s.destroyLocalSnapshotBestEffort(ctx, sourceDataset, snap); err != nil {
			errs = append(errs, fmt.Sprintf("destroy_source_%s_failed: %v", snap, err))
		}
	}

	if target != nil {
		for _, snap := range stale {
			if err := s.destroyRemoteSnapshotBestEffort(ctx, target, targetPath, snap); err != nil {
				errs = append(errs, fmt.Sprintf("destroy_target_%s_failed: %v", snap, err))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("replication_retention_failed: %s", strings.Join(errs, "; "))
	}

	return nil
}

func intersectSnapshotNames(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, s := range b {
		set[s] = struct{}{}
	}

	var common []string
	for _, s := range a {
		if _, ok := set[s]; ok {
			common = append(common, s)
		}
	}

	sort.Strings(common)
	return common
}

func (s *Service) destroyRemoteSnapshotBestEffort(ctx context.Context, target *clusterModels.BackupTarget, dataset, snapName string) error {
	if target == nil {
		return nil
	}

	snap := normalizeDatasetPath(dataset) + "@" + snapName
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "destroy", "-r", snap)
	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		lower := strings.ToLower(err.Error() + " " + output)
		if strings.Contains(lower, "dataset does not exist") ||
			strings.Contains(lower, "no such") ||
			strings.Contains(lower, "snapshot not found") ||
			strings.Contains(lower, "does not exist") {
			return nil
		}
		return fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}

	return nil
}
