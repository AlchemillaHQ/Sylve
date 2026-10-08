// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/beevik/etree"
	"github.com/digitalocean/go-libvirt"
	"github.com/google/uuid"
)

func TestIntegrationCPUPinningNativeXML(t *testing.T) {
	if testing.Short() || runtime.GOOS != "freebsd" || os.Geteuid() != 0 {
		t.Skip("requires FreeBSD, root, and a running libvirt bhyve driver")
	}
	uri, err := url.Parse("bhyve:///system")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := libvirt.ConnectToURI(uri)
	if err != nil {
		t.Skipf("libvirt bhyve driver unavailable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Disconnect() })
	version, err := conn.ConnectGetLibVersion()
	if err != nil {
		t.Fatalf("get libvirt version: %v", err)
	}
	if err := validateLibvirtVersion(version); err != nil {
		t.Skip(err)
	}
	logicalCores := utils.GetLogicalCores()
	if logicalCores < 1 {
		t.Fatal("host logical CPU count is unavailable")
	}
	cores := []int{0}
	if logicalCores > 1 {
		cores = append(cores, 1)
	}
	vm := vmModels.VM{
		RID: 100, CPUSockets: 1, CPUCores: len(cores), CPUThreads: 1, RAM: 268435456,
		TimeOffset: vmModels.TimeOffsetLocal, BootROM: vmModels.VMBootROMUEFI,
		CPUPinning: []vmModels.VMCPUPinning{{HostSocket: 0, HostCPU: cores}},
	}
	domainXML, err := (&Service{}).CreateVmXML(vm, t.TempDir())
	if err != nil {
		t.Fatalf("generate pinned domain XML: %v", err)
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromString(domainXML); err != nil {
		t.Fatal(err)
	}
	doc.FindElement("//name").SetText("sylve-test-cpu-pinning-" + uuid.NewString())
	domainXML, err = doc.WriteToString()
	if err != nil {
		t.Fatal(err)
	}
	domain, err := conn.DomainDefineXML(domainXML)
	if err != nil {
		t.Fatalf("define native CPU pinning: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.DomainUndefine(domain); err != nil {
			t.Errorf("remove disposable domain %s: %v", domain.Name, err)
		}
	})
	maplen := (logicalCores + 7) / 8
	maps, count, err := conn.DomainGetVcpuPinInfo(domain, int32(len(cores)), int32(maplen), uint32(libvirt.DomainAffectConfig))
	if err != nil {
		t.Fatalf("query native CPU pinning: %v", err)
	}
	if int(count) != len(cores) || len(maps) != len(cores)*maplen {
		t.Fatalf("unexpected pin map dimensions: count=%d bytes=%d", count, len(maps))
	}
	for vcpu, pinnedCPU := range cores {
		for hostCPU := 0; hostCPU < logicalCores; hostCPU++ {
			pinned := maps[vcpu*maplen+hostCPU/8]&(1<<uint(hostCPU%8)) != 0
			if pinned != (hostCPU == pinnedCPU) {
				t.Fatalf("vCPU %d host CPU %d pinned=%t, want host CPU %d", vcpu, hostCPU, pinned, pinnedCPU)
			}
		}
	}
	native, err := conn.ConnectDomainXMLToNative("bhyve-argv", domainXML, 0)
	if err != nil {
		t.Fatalf("convert native pinning to bhyve arguments: %v", err)
	}
	for vcpu, hostCPU := range cores {
		if arg := fmt.Sprintf("-p %d:%d", vcpu, hostCPU); !strings.Contains(native, arg) {
			t.Fatalf("expected %q in generated bhyve command: %s", arg, native)
		}
	}
}
