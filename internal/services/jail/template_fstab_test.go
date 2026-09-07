// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestRebaseTemplateFstabOnlyChangesContainedDestinations(t *testing.T) {
	const sourceRoot = "/mnt/chunky/sylve/jails/100"
	const targetRoot = "/fast/ssd100/sylve/jails/245"

	input := "" +
		"# devfs " + sourceRoot + "/comment devfs rw 0 0\r\n" +
		"devfs\t" + sourceRoot + "/dev\tdevfs\trw\t0\t0\r\n" +
		"/mnt/ssd100/shared  " + sourceRoot + "/mnt/My\\040Files nullfs rw,uid=100 0 0\n" +
		"/mnt/source " + sourceRoot + " nullfs rw 0 0\n" +
		"/mnt/source " + sourceRoot + "0/mnt/data nullfs rw 0 0\n" +
		"/mnt/source " + sourceRoot + "/../outside nullfs rw 0 0\n" +
		"/mnt/source " + sourceRoot + "\\057..\\057outside nullfs rw 0 0\n" +
		"/mnt/source /somewhere/with/100/mnt/data nullfs rw 0 0\n" +
		"malformed\n"

	expected := "" +
		"# devfs " + sourceRoot + "/comment devfs rw 0 0\r\n" +
		"devfs\t" + targetRoot + "/dev\tdevfs\trw\t0\t0\r\n" +
		"/mnt/ssd100/shared  " + targetRoot + "/mnt/My\\040Files nullfs rw,uid=100 0 0\n" +
		"/mnt/source " + targetRoot + " nullfs rw 0 0\n" +
		"/mnt/source " + sourceRoot + "0/mnt/data nullfs rw 0 0\n" +
		"/mnt/source " + sourceRoot + "/../outside nullfs rw 0 0\n" +
		"/mnt/source " + sourceRoot + "\\057..\\057outside nullfs rw 0 0\n" +
		"/mnt/source /somewhere/with/100/mnt/data nullfs rw 0 0\n" +
		"malformed\n"

	got, err := rebaseTemplateFstab(input, sourceRoot, targetRoot)
	if err != nil {
		t.Fatalf("rebaseTemplateFstab failed: %v", err)
	}
	if got != expected {
		t.Fatalf("rebased fstab mismatch\ngot:\n%s\nwant:\n%s", got, expected)
	}
}

func TestRebaseTemplateFstabPreservesEmptyDocument(t *testing.T) {
	const input = " \t\r\n"
	got, err := rebaseTemplateFstab(input, "", "")
	if err != nil {
		t.Fatalf("empty fstab should not require roots: %v", err)
	}
	if got != input {
		t.Fatalf("empty fstab = %q, want %q", got, input)
	}
}

func TestDisableUnresolvedTemplateFstabPreservesEntriesAsComments(t *testing.T) {
	input := "# existing comment\r\n" +
		"devfs\t/jails/100/dev\tdevfs\trw\t0\t0\r\n" +
		"\n" +
		"  /mnt/source /jails/100/mnt/source nullfs rw 0 0\n"

	expected := "# Sylve: these fstab entries were disabled because the legacy template does not\n" +
		"# record its source jail root. Review destination paths before uncommenting them.\n" +
		"# existing comment\r\n" +
		"# devfs\t/jails/100/dev\tdevfs\trw\t0\t0\r\n" +
		"\n" +
		"#   /mnt/source /jails/100/mnt/source nullfs rw 0 0\n"

	if got := disableUnresolvedTemplateFstab(input); got != expected {
		t.Fatalf("disabled fstab mismatch\ngot:\n%s\nwant:\n%s", got, expected)
	}
}

func TestRebaseTemplateFstabRejectsUnsafeRoots(t *testing.T) {
	tests := []struct {
		name       string
		sourceRoot string
		targetRoot string
	}{
		{name: "missing source", sourceRoot: "", targetRoot: "/jails/200"},
		{name: "relative source", sourceRoot: "jails/100", targetRoot: "/jails/200"},
		{name: "filesystem root source", sourceRoot: "/", targetRoot: "/jails/200"},
		{name: "relative target", sourceRoot: "/jails/100", targetRoot: "jails/200"},
		{name: "filesystem root target", sourceRoot: "/jails/100", targetRoot: "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := rebaseTemplateFstab(
				"devfs /jails/100/dev devfs rw 0 0\n",
				tt.sourceRoot,
				tt.targetRoot,
			)
			if err == nil {
				t.Fatal("expected invalid root to be rejected")
			}
		})
	}
}

func TestResolveTemplateFstabSourceRootUsesCapturedRoot(t *testing.T) {
	template := &jailModels.JailTemplate{
		Fstab:           "devfs /jails/100/dev devfs rw 0 0\n",
		FstabSourceRoot: " /jails/100/ ",
	}

	if err := (&Service{}).resolveTemplateFstabSourceRoot(context.Background(), template); err != nil {
		t.Fatalf("resolve captured source root: %v", err)
	}
	if template.FstabSourceRoot != "/jails/100" {
		t.Fatalf("source root = %q, want /jails/100", template.FstabSourceRoot)
	}
}

func TestResolveTemplateFstabSourceRootRecoversLegacyTemplate(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.Storage{})
	sourceJail := jailModels.Jail{CTID: 100, Name: "source", Type: jailModels.JailTypeFreeBSD}
	if err := dbConn.Create(&sourceJail).Error; err != nil {
		t.Fatalf("create source jail: %v", err)
	}
	if err := dbConn.Create(&jailModels.Storage{
		JailID: sourceJail.ID,
		Pool:   "ssd100",
		GUID:   "source-guid",
		Name:   "Base Filesystem",
		IsBase: true,
	}).Error; err != nil {
		t.Fatalf("create source storage: %v", err)
	}

	runner := &fakeGZFSRunner{
		datasets: map[string]fakeDatasetInfo{
			"ssd100/sylve/jails/100": {
				GUID:       "source-guid",
				Mountpoint: "/mnt/chunky/sylve/jails/100",
			},
		},
	}
	service := newTemplateTestService(t, dbConn, runner, "ssd100")
	template := &jailModels.JailTemplate{
		SourceJailCTID: 100,
		Fstab:          "devfs /mnt/chunky/sylve/jails/100/dev devfs rw 0 0\n",
	}

	if err := service.resolveTemplateFstabSourceRoot(context.Background(), template); err != nil {
		t.Fatalf("recover legacy source root: %v", err)
	}
	if template.FstabSourceRoot != "/mnt/chunky/sylve/jails/100" {
		t.Fatalf("source root = %q, want /mnt/chunky/sylve/jails/100", template.FstabSourceRoot)
	}
}

func TestResolveTemplateFstabSourceRootAllowsUnrecoverableLegacyTemplate(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &jailModels.Storage{})
	service := &Service{DB: dbConn}
	template := &jailModels.JailTemplate{
		SourceJailCTID: 100,
		Fstab:          "devfs /jails/100/dev devfs rw 0 0\n",
	}

	if err := service.resolveTemplateFstabSourceRoot(context.Background(), template); err != nil {
		t.Fatalf("unrecoverable legacy source should use disabled-fstab fallback: %v", err)
	}
	if template.FstabSourceRoot != "" {
		t.Fatalf("source root = %q, want empty fallback marker", template.FstabSourceRoot)
	}
}

func TestResolveTemplateFstabSourceRootAllowsEmptyFstab(t *testing.T) {
	template := &jailModels.JailTemplate{Fstab: " \n\t"}
	if err := (&Service{}).resolveTemplateFstabSourceRoot(context.Background(), template); err != nil {
		t.Fatalf("empty fstab should not require source metadata: %v", err)
	}
}
