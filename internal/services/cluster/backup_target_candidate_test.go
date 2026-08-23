// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package cluster

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
)

func managedBackupTargetInput() clusterServiceInterfaces.BackupTargetReq {
	return clusterServiceInterfaces.BackupTargetReq{
		Name: "target", SSHHost: "root@backup", SSHPort: 22,
		SSHKey: "pasted-private-key", BackupRoot: "tank/backups", Enabled: boolPtr(true),
	}
}

func TestBuildBackupTargetCreateCandidateRequiresPastedManagedKey(t *testing.T) {
	s := &Service{}
	input := managedBackupTargetInput()
	candidate, err := s.BuildBackupTargetCreateCandidate(input)
	if err != nil {
		t.Fatalf("candidate: %v", err)
	}
	if candidate.ID != 0 || candidate.SSHKey != "pasted-private-key" || candidate.SSHKeyPath != "" {
		t.Fatalf("create candidate: %+v", candidate)
	}

	input.SSHKey = ""
	if _, err := s.BuildBackupTargetCreateCandidate(input); err == nil ||
		!strings.Contains(err.Error(), "managed_ssh_key_required") {
		t.Fatalf("missing key error=%v", err)
	}
}

func TestBuildBackupTargetCreateCandidateCanonicalizesRemoteValues(t *testing.T) {
	input := managedBackupTargetInput()
	input.SSHHost = "root@Backup.Example"
	input.BackupRoot = " tank/backups "
	candidate, err := (&Service{}).BuildBackupTargetCreateCandidate(input)
	if err != nil {
		t.Fatalf("candidate: %v", err)
	}
	if candidate.SSHHost != "root@backup.example" || candidate.BackupRoot != "tank/backups" {
		t.Fatalf("candidate: %+v", candidate)
	}

	input.SSHHost = "-oProxyCommand=touch"
	if _, err := (&Service{}).BuildBackupTargetCreateCandidate(input); err == nil {
		t.Fatal("unsafe SSH destination accepted")
	}
	input.SSHHost = "root@backup"
	input.BackupRoot = "tank/backups;touch"
	if _, err := (&Service{}).BuildBackupTargetCreateCandidate(input); err == nil {
		t.Fatal("unsafe backup root accepted")
	}
}

func TestBuildBackupTargetUpdatePlanPreservesStoredManagedKeyAndRequiresQuiescedRotation(t *testing.T) {
	s := &Service{}
	existing := &clusterModels.BackupTarget{
		ID: 81, Name: "target", SSHHost: "root@backup", SSHPort: 22,
		SSHKeyPath: "/leader/local/target-81_id", SSHKey: "stored-private-key",
		BackupRoot: "tank/backups", Enabled: true,
	}
	input := managedBackupTargetInput()
	input.SSHKey = ""
	plan, err := s.BuildBackupTargetUpdatePlan(existing, input)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Kind != clusterModels.BackupTargetUpdateKindMetadata || plan.Candidate.ID != existing.ID ||
		plan.Candidate.SSHKey != "stored-private-key" || plan.Candidate.SSHKeyPath != "" {
		t.Fatalf("stored identity not preserved: %+v", plan)
	}

	input.SSHKey = " replacement-key "
	if _, err := s.BuildBackupTargetUpdatePlan(existing, input); err == nil ||
		!strings.Contains(err.Error(), "must_be_disabled") {
		t.Fatalf("enabled replacement error=%v", err)
	}
	existing.Enabled = false
	input.Enabled = boolPtr(false)
	plan, err = s.BuildBackupTargetUpdatePlan(existing, input)
	if err != nil {
		t.Fatalf("replacement plan: %v", err)
	}
	if plan.Kind != clusterModels.BackupTargetUpdateKindRotateKey || plan.Candidate.SSHKey != "replacement-key" ||
		plan.Candidate.Enabled || plan.Candidate.SSHKeyPath != "" {
		t.Fatalf("replacement plan: %+v", plan)
	}
}

func TestBuildBackupTargetUpdatePlanAllowsLegacyMetadataAndDisable(t *testing.T) {
	existing := &clusterModels.BackupTarget{
		ID: 82, Name: "target", SSHHost: "root@backup", SSHPort: 22,
		SSHKeyPath: "/legacy/external/path", BackupRoot: "tank/backups", Enabled: true,
	}
	input := managedBackupTargetInput()
	input.SSHKey = ""
	input.Name = "renamed"
	plan, err := (&Service{}).BuildBackupTargetUpdatePlan(existing, input)
	if err != nil || plan.Kind != clusterModels.BackupTargetUpdateKindMetadata {
		t.Fatalf("legacy metadata plan=%+v err=%v", plan, err)
	}
	input.Name = existing.Name
	input.Enabled = boolPtr(false)
	plan, err = (&Service{}).BuildBackupTargetUpdatePlan(existing, input)
	if err != nil || plan.Kind != clusterModels.BackupTargetUpdateKindDisable {
		t.Fatalf("legacy disable plan=%+v err=%v", plan, err)
	}
}

func TestBuildBackupTargetUpdatePlanRejectsMixedStateAndMetadataChanges(t *testing.T) {
	existing := &clusterModels.BackupTarget{
		ID: 84, Name: "target", SSHHost: "root@backup", SSHPort: 22,
		SSHKey: "key", BackupRoot: "tank/backups", Enabled: true,
	}
	input := managedBackupTargetInput()
	input.SSHKey = ""
	input.Name = "renamed"
	input.Enabled = boolPtr(false)
	if _, err := (&Service{}).BuildBackupTargetUpdatePlan(existing, input); err == nil ||
		!strings.Contains(err.Error(), "update_mode_conflict") {
		t.Fatalf("mixed state/metadata error=%v", err)
	}
}

func TestBuildBackupTargetUpdatePlanRejectsImmutableIdentityChanges(t *testing.T) {
	existing := &clusterModels.BackupTarget{
		ID: 83, Name: "target", SSHHost: "root@backup", SSHPort: 22,
		SSHKey: "key", BackupRoot: "tank/backups", CreateBackupRoot: true, Enabled: true,
	}
	input := managedBackupTargetInput()
	input.SSHKey = ""
	input.CreateBackupRoot = boolPtr(true)
	input.BackupRoot = "tank/other"
	if _, err := (&Service{}).BuildBackupTargetUpdatePlan(existing, input); err == nil ||
		!strings.Contains(err.Error(), "root_immutable") {
		t.Fatalf("root error=%v", err)
	}
	input.BackupRoot = existing.BackupRoot
	input.SSHHost = "root@other"
	if _, err := (&Service{}).BuildBackupTargetUpdatePlan(existing, input); err == nil ||
		!strings.Contains(err.Error(), "endpoint_immutable") {
		t.Fatalf("endpoint error=%v", err)
	}
}
