// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"strings"
	"testing"
)

func TestIgnoreUMSR_MigratesOldFormatToNative(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><features><apic></apic></features><bhyve:commandline><bhyve:arg value="-S"/><bhyve:arg value="-w"/><bhyve:arg value="-u"/></bhyve:commandline></domain>`

	updated, err := updateIgnoreUMSROptionXML(xml, true)
	if err != nil {
		t.Fatalf("updateIgnoreUMSR returned error: %v", err)
	}

	if strings.Contains(updated, `value="-w"`) {
		t.Fatalf("expected -w bhyve arg to be removed, got: %s", updated)
	}

	if !strings.Contains(updated, `<msrs unknown="ignore"`) {
		t.Fatalf("expected native msrs element in features, got: %s", updated)
	}

	if !strings.Contains(updated, `<apic`) {
		t.Fatalf("expected existing apic element preserved, got: %s", updated)
	}

	if !strings.Contains(updated, `value="-S"`) {
		t.Fatalf("expected non-ignoreUMSR arg -S preserved, got: %s", updated)
	}

	if !strings.Contains(updated, `value="-u"`) {
		t.Fatalf("expected non-ignoreUMSR arg -u preserved, got: %s", updated)
	}
}

func TestIgnoreUMSR_UpdatesAlreadyNativeFormat(t *testing.T) {
	xml := `<domain type="bhyve"><name>100</name><features><msrs unknown="ignore"/></features></domain>`

	updated, err := updateIgnoreUMSROptionXML(xml, true)
	if err != nil {
		t.Fatalf("updateIgnoreUMSR returned error: %v", err)
	}

	if strings.Count(updated, `<msrs unknown="ignore"`) != 1 {
		t.Fatalf("expected exactly one msrs element, got: %s", updated)
	}
}

func TestIgnoreUMSR_DisablesWhenAlreadyNative(t *testing.T) {
	xml := `<domain type="bhyve"><name>100</name><features><msrs unknown="ignore"/></features></domain>`

	updated, err := updateIgnoreUMSROptionXML(xml, false)
	if err != nil {
		t.Fatalf("updateIgnoreUMSR returned error: %v", err)
	}

	if strings.Contains(updated, `<msrs unknown="ignore"`) {
		t.Fatalf("expected msrs element removed when disabled, got: %s", updated)
	}
}

func TestIgnoreUMSR_DisablesWhenOldFormat(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><bhyve:commandline><bhyve:arg value="-w"/></bhyve:commandline></domain>`

	updated, err := updateIgnoreUMSROptionXML(xml, false)
	if err != nil {
		t.Fatalf("updateIgnoreUMSR returned error: %v", err)
	}

	if strings.Contains(updated, `value="-w"`) {
		t.Fatalf("expected -w bhyve arg removed when disabled, got: %s", updated)
	}

	if strings.Contains(updated, `<msrs unknown="ignore"`) {
		t.Fatalf("expected no msrs element when disabled, got: %s", updated)
	}
}

func TestIgnoreUMSR_HandlesBothFormatsSimultaneously(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><features><msrs unknown="ignore"/></features><bhyve:commandline><bhyve:arg value="-w"/></bhyve:commandline></domain>`

	updated, err := updateIgnoreUMSROptionXML(xml, true)
	if err != nil {
		t.Fatalf("updateIgnoreUMSR returned error: %v", err)
	}

	if strings.Contains(updated, `value="-w"`) {
		t.Fatalf("expected -w bhyve arg removed, got: %s", updated)
	}

	if strings.Count(updated, `<msrs unknown="ignore"`) != 1 {
		t.Fatalf("expected exactly one msrs element, got: %s", updated)
	}
}
