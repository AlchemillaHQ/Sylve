// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"context"
	"testing"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestCreateShareReturnsDatasetConflictBeforeDBDuplicate(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{})

	existing := sambaModels.SambaShare{
		Name:    "share-one",
		Dataset: "dataset-guid-1",
		Path:    "",
	}
	if err := dbConn.Create(&existing).Error; err != nil {
		t.Fatalf("failed creating existing share fixture: %v", err)
	}

	svc := &Service{DB: dbConn}

	err := svc.CreateShare(
		context.Background(),
		"share-two",
		"dataset-guid-1",
		nil,
		nil,
		nil,
		nil,
		true,
		false,
		"0664",
		"2775",
		false,
		0,
	)
	if err == nil {
		t.Fatal("expected dataset conflict error, got nil")
	}
	if err.Error() != "share_with_dataset_exists" {
		t.Fatalf("expected share_with_dataset_exists, got %q", err.Error())
	}
}
