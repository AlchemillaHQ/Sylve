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

func TestBuildBackupTargetUpdateCandidateUsesStoredManagedKeyAndExactID(t *testing.T) {
	s := &Service{}
	existing := &clusterModels.BackupTarget{
		ID: 81, Name: "old", SSHHost: "root@old", SSHPort: 22,
		SSHKeyPath: "/leader/local/target-81_id", SSHKey: "stored-private-key",
		BackupRoot: "tank/old", Enabled: true,
	}
	input := managedBackupTargetInput()
	input.SSHKey = ""
	candidate, err := s.BuildBackupTargetUpdateCandidate(existing, input)
	if err != nil {
		t.Fatalf("candidate: %v", err)
	}
	if candidate.ID != existing.ID || candidate.SSHKey != "stored-private-key" || candidate.SSHKeyPath != "" {
		t.Fatalf("stored identity not preserved: %+v", candidate)
	}

	input.SSHKey = " replacement-key "
	candidate, err = s.BuildBackupTargetUpdateCandidate(existing, input)
	if err != nil {
		t.Fatalf("replacement candidate: %v", err)
	}
	if candidate.ID != existing.ID || candidate.SSHKey != "replacement-key" || candidate.SSHKeyPath != "" {
		t.Fatalf("replacement candidate: %+v", candidate)
	}
}

func TestBuildBackupTargetUpdateCandidateRejectsLegacyTargetWithoutKey(t *testing.T) {
	existing := &clusterModels.BackupTarget{ID: 82, SSHKeyPath: "/legacy/external/path", Enabled: true}
	input := managedBackupTargetInput()
	input.SSHKey = ""
	if _, err := (&Service{}).BuildBackupTargetUpdateCandidate(existing, input); err == nil ||
		!strings.Contains(err.Error(), "managed_ssh_key_required") {
		t.Fatalf("legacy target error=%v", err)
	}
}
