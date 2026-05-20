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

	"github.com/beevik/etree"
)

func updateVirtio9P(xml string, filesystems []virtio9pSpec) (string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return "", err
	}

	root := doc.Root()

	bhyveCL := doc.FindElement("//commandline")
	if bhyveCL != nil && bhyveCL.Space == "bhyve" {
		for _, arg := range bhyveCL.ChildElements() {
			v := arg.SelectAttrValue("value", "")
			if v != "" && strings.Contains(v, "virtio-9p") {
				bhyveCL.RemoveChild(arg)
			}
		}
	}

	devicesEl := root.FindElement("devices")
	if devicesEl == nil {
		devicesEl = root.CreateElement("devices")
	}

	for _, el := range devicesEl.FindElements("filesystem") {
		devicesEl.RemoveChild(el)
	}

	for _, fs := range filesystems {
		fsEl := devicesEl.CreateElement("filesystem")
		fsEl.CreateAttr("type", "mount")

		srcEl := fsEl.CreateElement("source")
		srcEl.CreateAttr("dir", fs.sourcePath)

		tgtEl := fsEl.CreateElement("target")
		tgtEl.CreateAttr("dir", fs.targetName)

		if fs.readOnly {
			fsEl.CreateElement("readonly")
		}
	}

	return doc.WriteToString()
}

type virtio9pSpec struct {
	sourcePath string
	targetName string
	readOnly   bool
}

func TestVirtio9P_MigratesOldFormatToNative(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><bhyve:commandline><bhyve:arg value="-S"/><bhyve:arg value="-s 10:0,virtio-9p,shared=/zroot/data"/><bhyve:arg value="-u"/></bhyve:commandline></domain>`

	filesystems := []virtio9pSpec{
		{sourcePath: "/zroot/data", targetName: "shared", readOnly: false},
	}

	updated, err := updateVirtio9P(xml, filesystems)
	if err != nil {
		t.Fatalf("updateVirtio9P returned error: %v", err)
	}

	if strings.Contains(updated, "virtio-9p") {
		t.Fatalf("expected old virtio-9p bhyve arg to be removed, got: %s", updated)
	}

	if !strings.Contains(updated, `<filesystem type="mount"`) {
		t.Fatalf("expected native filesystem element, got: %s", updated)
	}

	if !strings.Contains(updated, `<source dir="/zroot/data"`) {
		t.Fatalf("expected source dir in filesystem element, got: %s", updated)
	}

	if !strings.Contains(updated, `<target dir="shared"`) {
		t.Fatalf("expected target dir in filesystem element, got: %s", updated)
	}

	if !strings.Contains(updated, `value="-S"`) {
		t.Fatalf("expected non-virtio9p arg -S preserved, got: %s", updated)
	}

	if !strings.Contains(updated, `value="-u"`) {
		t.Fatalf("expected non-virtio9p arg -u preserved, got: %s", updated)
	}
}

func TestVirtio9P_WithReadOnly(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><bhyve:commandline><bhyve:arg value="-s 10:0,virtio-9p,shared=/zroot/data,ro"/></bhyve:commandline></domain>`

	filesystems := []virtio9pSpec{
		{sourcePath: "/zroot/data", targetName: "shared", readOnly: true},
	}

	updated, err := updateVirtio9P(xml, filesystems)
	if err != nil {
		t.Fatalf("updateVirtio9P returned error: %v", err)
	}

	if !strings.Contains(updated, `<readonly/>`) && !strings.Contains(updated, `<readonly></readonly>`) {
		t.Fatalf("expected readonly element in filesystem, got: %s", updated)
	}
}

func TestVirtio9P_MultipleFilesystems(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>100</name><bhyve:commandline><bhyve:arg value="-s 10:0,virtio-9p,dir1=/path/one"/><bhyve:arg value="-s 11:0,virtio-9p,dir2=/path/two,ro"/></bhyve:commandline></domain>`

	filesystems := []virtio9pSpec{
		{sourcePath: "/path/one", targetName: "dir1", readOnly: false},
		{sourcePath: "/path/two", targetName: "dir2", readOnly: true},
	}

	updated, err := updateVirtio9P(xml, filesystems)
	if err != nil {
		t.Fatalf("updateVirtio9P returned error: %v", err)
	}

	if strings.Count(updated, `<filesystem type="mount"`) != 2 {
		t.Fatalf("expected 2 filesystem elements, got: %s", updated)
	}

	if strings.Count(updated, `<readonly/>`)+strings.Count(updated, `<readonly></readonly>`) != 1 {
		t.Fatalf("expected 1 readonly element for the ro share, got: %s", updated)
	}
}

func TestVirtio9P_UpdatesAlreadyNativeFormat(t *testing.T) {
	xml := `<domain type="bhyve"><devices><filesystem type="mount"><source dir="/old/path"/><target dir="oldtarget"/></filesystem></devices></domain>`

	filesystems := []virtio9pSpec{
		{sourcePath: "/new/path", targetName: "newtarget", readOnly: false},
	}

	updated, err := updateVirtio9P(xml, filesystems)
	if err != nil {
		t.Fatalf("updateVirtio9P returned error: %v", err)
	}

	if strings.Count(updated, `<filesystem type="mount"`) != 1 {
		t.Fatalf("expected exactly one filesystem element after update, got: %s", updated)
	}

	if !strings.Contains(updated, `<source dir="/new/path"`) {
		t.Fatalf("expected updated source path, got: %s", updated)
	}

	if !strings.Contains(updated, `<target dir="newtarget"`) {
		t.Fatalf("expected updated target name, got: %s", updated)
	}
}

func TestVirtio9P_RemovesAllWhenEmpty(t *testing.T) {
	xml := `<domain type="bhyve" xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><devices><filesystem type="mount"><source dir="/path/one"/><target dir="dir1"/></filesystem></devices><bhyve:commandline><bhyve:arg value="-s 10:0,virtio-9p,dir2=/path/two"/></bhyve:commandline></domain>`

	filesystems := []virtio9pSpec{}

	updated, err := updateVirtio9P(xml, filesystems)
	if err != nil {
		t.Fatalf("updateVirtio9P returned error: %v", err)
	}

	if strings.Contains(updated, "virtio-9p") {
		t.Fatalf("expected old bhyve arg removed, got: %s", updated)
	}

	if strings.Contains(updated, `<filesystem`) {
		t.Fatalf("expected no filesystem elements when empty, got: %s", updated)
	}
}


