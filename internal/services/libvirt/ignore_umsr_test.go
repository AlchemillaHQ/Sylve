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

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/beevik/etree"
)

func updateIgnoreUMSR(xml string, ignore bool) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", err
	}

	root := doc.Root()

	featuresEl := root.FindElement("features")
	if featuresEl != nil {
		for _, el := range featuresEl.FindElements("msrs") {
			featuresEl.RemoveChild(el)
		}
	}

	if bhyveCL := doc.FindElement("//commandline"); bhyveCL != nil && bhyveCL.Space == "bhyve" {
		for _, arg := range bhyveCL.ChildElements() {
			if arg.SelectAttrValue("value", "") == "-w" {
				bhyveCL.RemoveChild(arg)
			}
		}
	}

	if ignore {
		if featuresEl == nil {
			featuresEl = root.CreateElement("features")
		}
		msrsEl := featuresEl.CreateElement("msrs")
		msrsEl.CreateAttr("unknown", "ignore")
	}

	return doc.WriteToString()
}

func TestIgnoreUMSR_MigratesOldFormatToNative(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><features><apic></apic></features><bhyve:commandline><bhyve:arg value="-S"/><bhyve:arg value="-w"/><bhyve:arg value="-u"/></bhyve:commandline></domain>`

	updated, err := updateIgnoreUMSR(xml, true)
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

	updated, err := updateIgnoreUMSR(xml, true)
	if err != nil {
		t.Fatalf("updateIgnoreUMSR returned error: %v", err)
	}

	if strings.Count(updated, `<msrs unknown="ignore"`) != 1 {
		t.Fatalf("expected exactly one msrs element, got: %s", updated)
	}
}

func TestIgnoreUMSR_DisablesWhenAlreadyNative(t *testing.T) {
	xml := `<domain type="bhyve"><name>100</name><features><msrs unknown="ignore"/></features></domain>`

	updated, err := updateIgnoreUMSR(xml, false)
	if err != nil {
		t.Fatalf("updateIgnoreUMSR returned error: %v", err)
	}

	if strings.Contains(updated, `<msrs unknown="ignore"`) {
		t.Fatalf("expected msrs element removed when disabled, got: %s", updated)
	}
}

func TestIgnoreUMSR_DisablesWhenOldFormat(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><bhyve:commandline><bhyve:arg value="-w"/></bhyve:commandline></domain>`

	updated, err := updateIgnoreUMSR(xml, false)
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

	updated, err := updateIgnoreUMSR(xml, true)
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

func TestCreateVmXML_ProducesNativeIgnoreUMSRFormat(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		Name:       "vm-ignore-umsr",
		RID:        201,
		CPUSockets: 1,
		CPUCores:   1,
		CPUThreads: 1,
		RAM:        1024 * 1024 * 512,
		TimeOffset: vmModels.TimeOffsetUTC,
		IgnoreUMSR: true,
	}

	xml, err := svc.CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("CreateVmXML returned error: %v", err)
	}

	if strings.Contains(xml, `value="-w"`) {
		t.Fatalf("expected no -w bhyve arg, got native format: %s", xml)
	}

	if !strings.Contains(xml, `<msrs unknown="ignore"`) {
		t.Fatalf("expected native msrs element in features, got: %s", xml)
	}
}

func TestCreateVmXML_OmitsMSRsWhenIgnoreUMSRDisabled(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		Name:       "vm-no-ignore-umsr",
		RID:        202,
		CPUSockets: 1,
		CPUCores:   1,
		CPUThreads: 1,
		RAM:        1024 * 1024 * 512,
		TimeOffset: vmModels.TimeOffsetUTC,
		IgnoreUMSR: false,
	}

	xml, err := svc.CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("CreateVmXML returned error: %v", err)
	}

	if strings.Contains(xml, `value="-w"`) {
		t.Fatalf("expected no -w bhyve arg, got: %s", xml)
	}

	if strings.Contains(xml, `<msrs unknown="ignore"`) {
		t.Fatalf("expected no msrs element when ignoreUMSR is false, got: %s", xml)
	}
}

func TestCreateVmXML_IncludesMSRsAndAPIC(t *testing.T) {
	svc := &Service{}

	vm := vmModels.VM{
		Name:       "vm-msrs-apic",
		RID:        203,
		CPUSockets: 1,
		CPUCores:   1,
		CPUThreads: 1,
		RAM:        1024 * 1024 * 512,
		TimeOffset: vmModels.TimeOffsetUTC,
		IgnoreUMSR: true,
		APIC:       true,
	}

	xml, err := svc.CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("CreateVmXML returned error: %v", err)
	}

	if !strings.Contains(xml, `<msrs unknown="ignore"`) {
		t.Fatalf("expected msrs element in features, got: %s", xml)
	}

	if !strings.Contains(xml, `<apic`) {
		t.Fatalf("expected apic element in features, got: %s", xml)
	}
}
