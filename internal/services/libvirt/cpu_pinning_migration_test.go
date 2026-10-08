// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/beevik/etree"
	"github.com/digitalocean/go-libvirt"
)

type cpuPinningTestDomain struct {
	state     libvirt.DomainState
	xml       string
	stateErr  error
	xmlErr    error
	defineErr error
}

type cpuPinningConnectionStub struct {
	domains    map[string]*cpuPinningTestDomain
	definedXML []string
	xmlFlags   []libvirt.DomainXMLFlags
}

func (c *cpuPinningConnectionStub) DomainLookupByName(name string) (libvirt.Domain, error) {
	if c.domains[name] == nil {
		return libvirt.Domain{}, libvirt.Error{Code: uint32(libvirt.ErrNoDomain), Message: "domain not found"}
	}
	return libvirt.Domain{Name: name}, nil
}

func (c *cpuPinningConnectionStub) DomainGetState(domain libvirt.Domain, _ uint32) (int32, int32, error) {
	stored := c.domains[domain.Name]
	return int32(stored.state), 0, stored.stateErr
}

func (c *cpuPinningConnectionStub) DomainGetXMLDesc(domain libvirt.Domain, flags libvirt.DomainXMLFlags) (string, error) {
	c.xmlFlags = append(c.xmlFlags, flags)
	stored := c.domains[domain.Name]
	if flags&libvirt.DomainXMLSecure == 0 {
		return "", errors.New("secure XML is required to preserve VNC passwords")
	}
	return stored.xml, stored.xmlErr
}

func (c *cpuPinningConnectionStub) DomainDefineXML(domainXML string) (libvirt.Domain, error) {
	c.definedXML = append(c.definedXML, domainXML)
	doc := etree.NewDocument()
	if err := doc.ReadFromString(domainXML); err != nil {
		return libvirt.Domain{}, err
	}
	name := doc.FindElement("//name").Text()
	stored := c.domains[name]
	if stored.defineErr != nil {
		return libvirt.Domain{}, stored.defineErr
	}
	stored.xml = domainXML
	return libvirt.Domain{Name: name}, nil
}

func newCPUPinningMigrationTestService(t *testing.T) *Service {
	t.Helper()
	return &Service{DB: testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.VMCPUPinning{}, &models.Migrations{})}
}

func seedCPUPinningMigrationVM(t *testing.T, service *Service, rid uint, cores []int) vmModels.VM {
	t.Helper()
	vm := vmModels.VM{RID: rid, Name: "cpu-migration", CPUSockets: 1, CPUCores: 4, CPUThreads: 1}
	if len(cores) > 0 {
		vm.CPUPinning = []vmModels.VMCPUPinning{{HostSocket: 0, HostCPU: cores}}
	}
	if err := service.DB.Create(&vm).Error; err != nil {
		t.Fatalf("seed pinned VM: %v", err)
	}
	return vm
}

func cpuPinningMigrationRecordCount(t *testing.T, service *Service, rid uint) int64 {
	t.Helper()
	var count int64
	name := fmt.Sprintf("cpu_pinning_native_xml_format_1_%d", rid)
	if err := service.DB.Model(&models.Migrations{}).Where("name = ?", name).Count(&count).Error; err != nil {
		t.Fatalf("count migration records: %v", err)
	}
	return count
}

func cpuPinningTestXML(rid uint, cpuXML string) string {
	return fmt.Sprintf(`<domain xmlns:bhyve="http://libvirt.org/schemas/domain/bhyve/1.0"><name>%d</name><vcpu>4</vcpu><devices><graphics type="vnc" port="5900" passwd="keep-secret"/></devices>%s</domain>`, rid, cpuXML)
}

func TestMigrateCPUPinningToNativeFormatSkipsRunningAndRetriesRestoredXML(t *testing.T) {
	t.Parallel()
	service := newCPUPinningMigrationTestService(t)
	legacy := `<bhyve:commandline><bhyve:arg value="-p 0:3 -p 1:1"/><bhyve:arg value="-S"/></bhyve:commandline>`
	native := `<cputune><vcpupin vcpu="0" cpuset="3"/><vcpupin vcpu="1" cpuset="1"/></cputune>`
	conn := &cpuPinningConnectionStub{domains: map[string]*cpuPinningTestDomain{
		"101": {state: libvirt.DomainShutoff, xml: cpuPinningTestXML(101, legacy)},
		"102": {state: libvirt.DomainRunning, xml: cpuPinningTestXML(102, legacy)},
		"103": {state: libvirt.DomainShutoff, xml: cpuPinningTestXML(103, native)},
		"104": {state: libvirt.DomainShutoff, xml: cpuPinningTestXML(104, "")},
		"105": {state: libvirt.DomainShutoff, xml: cpuPinningTestXML(105, legacy)},
		"106": {state: libvirt.DomainShutoff, xml: cpuPinningTestXML(106, legacy)},
	}}
	for rid := uint(101); rid <= 106; rid++ {
		cores := []int{3, 1}
		if rid == 105 {
			cores = nil
		}
		seedCPUPinningMigrationVM(t, service, rid, cores)
	}
	if err := service.DB.Create(&models.Migrations{Name: "cpu_pinning_native_xml_format_1_106"}).Error; err != nil {
		t.Fatalf("seed earlier migration record: %v", err)
	}
	var before []vmModels.VMCPUPinning
	if err := service.DB.Order("id").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.migrateCPUPinningToNativeFormat(conn); err != nil {
		t.Fatalf("migrate CPU pins: %v", err)
	}
	want := []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "3"}, {VCPU: 1, CPUSet: "1"}}
	for _, rid := range []uint{101, 103, 104, 106} {
		assertNativeCPUPins(t, conn.domains[strconv.Itoa(int(rid))].xml, want)
		if count := cpuPinningMigrationRecordCount(t, service, rid); count != 1 {
			t.Fatalf("RID %d migration records = %d, want 1", rid, count)
		}
	}
	for _, rid := range []uint{102, 105} {
		if count := cpuPinningMigrationRecordCount(t, service, rid); count != 0 {
			t.Fatalf("skipped RID %d migration records = %d, want 0", rid, count)
		}
		if got := conn.domains[strconv.Itoa(int(rid))].xml; got != cpuPinningTestXML(rid, legacy) {
			t.Fatalf("skipped RID %d was changed: %s", rid, got)
		}
	}
	if len(conn.definedXML) != 3 {
		t.Fatalf("domain redefinitions = %d, want 3", len(conn.definedXML))
	}
	for _, flags := range conn.xmlFlags {
		if flags != libvirt.DomainXMLInactive|libvirt.DomainXMLSecure {
			t.Fatalf("XML flags = %v, want inactive and secure", flags)
		}
	}
	if !strings.Contains(conn.domains["101"].xml, `passwd="keep-secret"`) || !strings.Contains(conn.domains["101"].xml, `value="-S"`) {
		t.Fatalf("migration removed unrelated configuration: %s", conn.domains["101"].xml)
	}
	if err := service.migrateCPUPinningToNativeFormat(conn); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if len(conn.definedXML) != 3 {
		t.Fatalf("repeat migration redefined domains: %d calls", len(conn.definedXML))
	}
	var after []vmModels.VMCPUPinning
	if err := service.DB.Order("id").Find(&after).Error; err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("pin metadata changed: before=%+v after=%+v err=%v", before, after, err)
	}

	conn.domains["102"].state = libvirt.DomainShutoff
	if err := service.ensureCPUPinningNativeXML(conn, libvirt.Domain{Name: "102"}, 102); err != nil {
		t.Fatalf("convert deferred VM before start: %v", err)
	}
	assertNativeCPUPins(t, conn.domains["102"].xml, want)
}

func TestMigrateCPUPinningToNativeFormatRecordsOnlySuccessfulConversions(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"state", "XML", "define", "malformed pin"} {
		t.Run(failure, func(t *testing.T) {
			service := newCPUPinningMigrationTestService(t)
			seedCPUPinningMigrationVM(t, service, 101, []int{3})
			seedCPUPinningMigrationVM(t, service, 102, []int{1})
			original := cpuPinningTestXML(101, `<bhyve:commandline><bhyve:arg value="-p 0:3"/></bhyve:commandline>`)
			domain := &cpuPinningTestDomain{state: libvirt.DomainShutoff, xml: original}
			switch failure {
			case "state":
				domain.stateErr = errors.New("state failed")
			case "XML":
				domain.xmlErr = errors.New("XML failed")
			case "define":
				domain.defineErr = errors.New("define failed")
			case "malformed pin":
				domain.xml = cpuPinningTestXML(101, `<bhyve:commandline><bhyve:arg value="-p invalid"/></bhyve:commandline>`)
			}
			failedXML := domain.xml
			conn := &cpuPinningConnectionStub{domains: map[string]*cpuPinningTestDomain{
				"101": domain, "102": {state: libvirt.DomainShutoff, xml: cpuPinningTestXML(102, "")},
			}}
			if err := service.migrateCPUPinningToNativeFormat(conn); err != nil {
				t.Fatalf("migration should continue past individual failures: %v", err)
			}
			if domain.xml != failedXML || cpuPinningMigrationRecordCount(t, service, 101) != 0 {
				t.Fatal("failed migration changed domain or recorded completion")
			}
			if cpuPinningMigrationRecordCount(t, service, 102) != 1 {
				t.Fatal("failure prevented the next VM from being migrated")
			}
			domain.stateErr, domain.xmlErr, domain.defineErr = nil, nil, nil
			domain.xml = original
			if err := service.migrateCPUPinningToNativeFormat(conn); err != nil {
				t.Fatalf("retry migration: %v", err)
			}
			assertNativeCPUPins(t, domain.xml, []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "3"}})
			if cpuPinningMigrationRecordCount(t, service, 101) != 1 {
				t.Fatal("successful retry was not recorded")
			}
		})
	}
}

func TestEnsureCPUPinningNativeXMLReloadsPersistedPinsBeforeStart(t *testing.T) {
	t.Parallel()
	service := newCPUPinningMigrationTestService(t)
	vm := seedCPUPinningMigrationVM(t, service, 101, []int{3, 1})
	vm.CPUPinning = nil
	conn := &cpuPinningConnectionStub{domains: map[string]*cpuPinningTestDomain{
		"101": {state: libvirt.DomainShutoff, xml: cpuPinningTestXML(101, "")},
	}}
	if err := service.ensureCPUPinningNativeXML(conn, libvirt.Domain{Name: "101"}, vm.RID); err != nil {
		t.Fatalf("ensure pinning before start: %v", err)
	}
	assertNativeCPUPins(t, conn.domains["101"].xml, []libvirtServiceInterfaces.VCPUPin{{VCPU: 0, CPUSet: "3"}, {VCPU: 1, CPUSet: "1"}})
	if err := service.ensureCPUPinningNativeXML(conn, libvirt.Domain{Name: "101"}, vm.RID); err != nil || len(conn.definedXML) != 1 {
		t.Fatalf("repeated start rewrote native pins: definitions=%d err=%v", len(conn.definedXML), err)
	}
	conn.domains["101"].state = libvirt.DomainRunning
	if err := service.ensureCPUPinningNativeXML(conn, libvirt.Domain{Name: "101"}, vm.RID); err == nil {
		t.Fatal("running domain must not be migrated before start")
	}
	if len(conn.definedXML) != 1 {
		t.Fatal("running domain was redefined")
	}
}

func TestEnsureCPUPinningNativeXMLPreservesUnmanagedPinning(t *testing.T) {
	t.Parallel()
	service := newCPUPinningMigrationTestService(t)
	vm := seedCPUPinningMigrationVM(t, service, 101, nil)
	original := cpuPinningTestXML(101, `<bhyve:commandline><bhyve:arg value="-p 0:3"/></bhyve:commandline>`)
	conn := &cpuPinningConnectionStub{domains: map[string]*cpuPinningTestDomain{
		"101": {state: libvirt.DomainShutoff, xml: original},
	}}
	if err := service.ensureCPUPinningNativeXML(conn, libvirt.Domain{Name: "101"}, vm.RID); err != nil {
		t.Fatalf("start VM without managed pins: %v", err)
	}
	if len(conn.definedXML) != 0 || conn.domains["101"].xml != original {
		t.Fatal("before-start reconciliation changed unmanaged pinning")
	}
}

func TestMigrateCPUPinningToNativeFormatRetriesFailedMigrationRecord(t *testing.T) {
	t.Parallel()
	service := newCPUPinningMigrationTestService(t)
	seedCPUPinningMigrationVM(t, service, 101, []int{3})
	if err := service.DB.Migrator().DropTable(&models.Migrations{}); err != nil {
		t.Fatal(err)
	}
	conn := &cpuPinningConnectionStub{domains: map[string]*cpuPinningTestDomain{
		"101": {state: libvirt.DomainShutoff, xml: cpuPinningTestXML(101, "")},
	}}
	if err := service.migrateCPUPinningToNativeFormat(conn); err != nil {
		t.Fatalf("migration with unavailable records: %v", err)
	}
	if len(conn.definedXML) != 1 {
		t.Fatal("native conversion did not complete")
	}
	if err := service.DB.AutoMigrate(&models.Migrations{}); err != nil {
		t.Fatal(err)
	}
	if err := service.migrateCPUPinningToNativeFormat(conn); err != nil {
		t.Fatalf("retry migration record: %v", err)
	}
	if len(conn.definedXML) != 1 || cpuPinningMigrationRecordCount(t, service, 101) != 1 {
		t.Fatal("retry must record already-native XML without redefining it")
	}
}
