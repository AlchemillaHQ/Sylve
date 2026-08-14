// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilities

import (
	"errors"
	"testing"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func cloudInitTemplateRequest(
	name, user, meta string,
	networkConfig *string,
) utilitiesServiceInterfaces.CloudInitTemplateRequest {
	return utilitiesServiceInterfaces.CloudInitTemplateRequest{
		Name:          name,
		User:          user,
		Meta:          meta,
		NetworkConfig: networkConfig,
	}
}

func TestCloudInitTemplateLifecycleUsesCompleteDocuments(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.CloudInitTemplate{})
	service := &Service{DB: database}
	emptyNetwork := ""

	created, err := service.AddTemplate(cloudInitTemplateRequest(
		"  Base Template  ",
		"#cloud-config\nusers: []",
		"instance-id: base",
		&emptyNetwork,
	))
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if created.Name != "Base Template" || created.NetworkConfig != "" {
		t.Fatalf("created template=%+v", created)
	}

	_, err = service.AddTemplate(cloudInitTemplateRequest(
		"base template",
		"#cloud-config\nusers: []",
		"instance-id: duplicate",
		&emptyNetwork,
	))
	if !errors.Is(err, ErrCloudInitTemplateConflict) {
		t.Fatalf("duplicate error=%v", err)
	}

	staticNetwork := "version: 2"
	replaced, err := service.EditTemplate(created.ID, cloudInitTemplateRequest(
		"Replacement",
		"#cloud-config\nhostname: replacement",
		"instance-id: replacement",
		&staticNetwork,
	))
	if err != nil {
		t.Fatalf("replace template: %v", err)
	}
	if replaced.Name != "Replacement" ||
		replaced.User != "#cloud-config\nhostname: replacement" ||
		replaced.Meta != "instance-id: replacement" ||
		replaced.NetworkConfig != staticNetwork {
		t.Fatalf("replacement was partial: %+v", replaced)
	}

	replaced, err = service.EditTemplate(created.ID, cloudInitTemplateRequest(
		"Replacement",
		"#cloud-config\nhostname: replacement",
		"instance-id: replacement",
		&emptyNetwork,
	))
	if err != nil || replaced.NetworkConfig != "" {
		t.Fatalf("explicit empty network config was not stored: template=%+v err=%v", replaced, err)
	}

	identity, err := service.DeleteTemplate(created.ID)
	if err != nil {
		t.Fatalf("delete template: %v", err)
	}
	if identity.ID != created.ID || identity.Name != "Replacement" {
		t.Fatalf("delete identity=%+v", identity)
	}
	if _, err := service.DeleteTemplate(created.ID); !errors.Is(err, ErrCloudInitTemplateNotFound) {
		t.Fatalf("second delete error=%v", err)
	}
}

func TestCloudInitTemplateValidationAndEmptyList(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.CloudInitTemplate{})
	service := &Service{DB: database}

	templates, err := service.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if templates == nil || len(templates) != 0 {
		t.Fatalf("empty templates=%#v", templates)
	}

	_, err = service.AddTemplate(cloudInitTemplateRequest("Name", "   ", "meta", nil))
	if !errors.Is(err, ErrCloudInitTemplateInvalid) {
		t.Fatalf("invalid template error=%v", err)
	}

	emptyNetwork := ""
	_, err = service.EditTemplate(999, cloudInitTemplateRequest(
		"Missing",
		"#cloud-config",
		"instance-id: missing",
		&emptyNetwork,
	))
	if !errors.Is(err, ErrCloudInitTemplateNotFound) {
		t.Fatalf("missing replacement error=%v", err)
	}
}
