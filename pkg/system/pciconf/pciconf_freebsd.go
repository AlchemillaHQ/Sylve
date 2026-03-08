// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package pciconf

/*
#include <stdlib.h>
#include <sys/types.h>
#include <sys/fcntl.h>
#include <sys/pciio.h>
#include <dev/pci/pcireg.h>
#include <unistd.h>

static int wrap_pci_getconf(int fd, struct pci_conf_io *pc) {
	return ioctl(fd, PCIOCGETCONF, pc);
}

static int wrap_open(const char *path, int flags, mode_t mode) {
	int fd = open(path, flags, mode);
	return fd;
}
*/
import "C"

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

const maxDevices = 255

var (
	pciVendors = map[uint16]string{}
	pciDevices = map[uint32]string{}

	parseOnce sync.Once
	parseErr  error
)

var pciNomatchTab = []struct {
	class    uint8
	subclass int
	desc     string
}{

	{C.PCIC_OLD, -1, "old"},
	{C.PCIC_OLD, C.PCIS_OLD_NONVGA, "non-VGA display device"},
	{C.PCIC_OLD, C.PCIS_OLD_VGA, "VGA-compatible display device"},
	{C.PCIC_STORAGE, -1, "mass storage"},
	{C.PCIC_STORAGE, C.PCIS_STORAGE_SCSI, "SCSI"},
	{C.PCIC_STORAGE, C.PCIS_STORAGE_IDE, "ATA"},
	{C.PCIC_STORAGE, C.PCIS_STORAGE_FLOPPY, "floppy disk"},
	{C.PCIC_STORAGE, C.PCIS_STORAGE_IPI, "IPI"},
	{C.PCIC_STORAGE, C.PCIS_STORAGE_RAID, "RAID"},
	{C.PCIC_STORAGE, C.PCIS_STORAGE_ATA_ADMA, "ATA (ADMA)"},
	{C.PCIC_STORAGE, C.PCIS_STORAGE_SATA, "SATA"},
	{C.PCIC_STORAGE, C.PCIS_STORAGE_SAS, "SAS"},
	{C.PCIC_STORAGE, C.PCIS_STORAGE_NVM, "NVM"},
	{C.PCIC_NETWORK, -1, "network"},
	{C.PCIC_NETWORK, C.PCIS_NETWORK_ETHERNET, "ethernet"},
	{C.PCIC_NETWORK, C.PCIS_NETWORK_TOKENRING, "token ring"},
	{C.PCIC_NETWORK, C.PCIS_NETWORK_FDDI, "fddi"},
	{C.PCIC_NETWORK, C.PCIS_NETWORK_ATM, "ATM"},
	{C.PCIC_NETWORK, C.PCIS_NETWORK_ISDN, "ISDN"},
	{C.PCIC_DISPLAY, -1, "display"},
	{C.PCIC_DISPLAY, C.PCIS_DISPLAY_VGA, "VGA"},
	{C.PCIC_DISPLAY, C.PCIS_DISPLAY_XGA, "XGA"},
	{C.PCIC_DISPLAY, C.PCIS_DISPLAY_3D, "3D"},
	{C.PCIC_MULTIMEDIA, -1, "multimedia"},
	{C.PCIC_MULTIMEDIA, C.PCIS_MULTIMEDIA_VIDEO, "video"},
	{C.PCIC_MULTIMEDIA, C.PCIS_MULTIMEDIA_AUDIO, "audio"},
	{C.PCIC_MULTIMEDIA, C.PCIS_MULTIMEDIA_TELE, "telephony"},
	{C.PCIC_MULTIMEDIA, C.PCIS_MULTIMEDIA_HDA, "HDA"},
	{C.PCIC_MEMORY, -1, "memory"},
	{C.PCIC_MEMORY, C.PCIS_MEMORY_RAM, "RAM"},
	{C.PCIC_MEMORY, C.PCIS_MEMORY_FLASH, "flash"},
	{C.PCIC_BRIDGE, -1, "bridge"},
	{C.PCIC_BRIDGE, C.PCIS_BRIDGE_HOST, "HOST-PCI"},
	{C.PCIC_BRIDGE, C.PCIS_BRIDGE_ISA, "PCI-ISA"},
	{C.PCIC_BRIDGE, C.PCIS_BRIDGE_EISA, "PCI-EISA"},
	{C.PCIC_BRIDGE, C.PCIS_BRIDGE_MCA, "PCI-MCA"},
	{C.PCIC_BRIDGE, C.PCIS_BRIDGE_PCI, "PCI-PCI"},
	{C.PCIC_BRIDGE, C.PCIS_BRIDGE_PCMCIA, "PCI-PCMCIA"},
	{C.PCIC_BRIDGE, C.PCIS_BRIDGE_NUBUS, "PCI-NuBus"},
	{C.PCIC_BRIDGE, C.PCIS_BRIDGE_CARDBUS, "PCI-CardBus"},
	{C.PCIC_BRIDGE, C.PCIS_BRIDGE_RACEWAY, "PCI-RACEway"},
	{C.PCIC_SIMPLECOMM, -1, "simple comms"},
	{C.PCIC_SIMPLECOMM, C.PCIS_SIMPLECOMM_UART, "UART"}, /* could detect 16550 */
	{C.PCIC_SIMPLECOMM, C.PCIS_SIMPLECOMM_PAR, "parallel port"},
	{C.PCIC_SIMPLECOMM, C.PCIS_SIMPLECOMM_MULSER, "multiport serial"},
	{C.PCIC_SIMPLECOMM, C.PCIS_SIMPLECOMM_MODEM, "generic modem"},
	{C.PCIC_BASEPERIPH, -1, "base peripheral"},
	{C.PCIC_BASEPERIPH, C.PCIS_BASEPERIPH_PIC, "interrupt controller"},
	{C.PCIC_BASEPERIPH, C.PCIS_BASEPERIPH_DMA, "DMA controller"},
	{C.PCIC_BASEPERIPH, C.PCIS_BASEPERIPH_TIMER, "timer"},
	{C.PCIC_BASEPERIPH, C.PCIS_BASEPERIPH_RTC, "realtime clock"},
	{C.PCIC_BASEPERIPH, C.PCIS_BASEPERIPH_PCIHOT, "PCI hot-plug controller"},
	{C.PCIC_BASEPERIPH, C.PCIS_BASEPERIPH_SDHC, "SD host controller"},
	{C.PCIC_INPUTDEV, -1, "input device"},
	{C.PCIC_INPUTDEV, C.PCIS_INPUTDEV_KEYBOARD, "keyboard"},
	{C.PCIC_INPUTDEV, C.PCIS_INPUTDEV_DIGITIZER, "digitizer"},
	{C.PCIC_INPUTDEV, C.PCIS_INPUTDEV_MOUSE, "mouse"},
	{C.PCIC_INPUTDEV, C.PCIS_INPUTDEV_SCANNER, "scanner"},
	{C.PCIC_INPUTDEV, C.PCIS_INPUTDEV_GAMEPORT, "gameport"},
	{C.PCIC_DOCKING, -1, "docking station"},
	{C.PCIC_PROCESSOR, -1, "processor"},
	{C.PCIC_SERIALBUS, -1, "serial bus"},
	{C.PCIC_SERIALBUS, C.PCIS_SERIALBUS_FW, "FireWire"},
	{C.PCIC_SERIALBUS, C.PCIS_SERIALBUS_ACCESS, "AccessBus"},
	{C.PCIC_SERIALBUS, C.PCIS_SERIALBUS_SSA, "SSA"},
	{C.PCIC_SERIALBUS, C.PCIS_SERIALBUS_USB, "USB"},
	{C.PCIC_SERIALBUS, C.PCIS_SERIALBUS_FC, "Fibre Channel"},
	{C.PCIC_SERIALBUS, C.PCIS_SERIALBUS_SMBUS, "SMBus"},
	{C.PCIC_WIRELESS, -1, "wireless controller"},
	{C.PCIC_WIRELESS, C.PCIS_WIRELESS_IRDA, "iRDA"},
	{C.PCIC_WIRELESS, C.PCIS_WIRELESS_IR, "IR"},
	{C.PCIC_WIRELESS, C.PCIS_WIRELESS_RF, "RF"},
	{C.PCIC_INTELLIIO, -1, "intelligent I/O controller"},
	{C.PCIC_INTELLIIO, C.PCIS_INTELLIIO_I2O, "I2O"},
	{C.PCIC_SATCOM, -1, "satellite communication"},
	{C.PCIC_SATCOM, C.PCIS_SATCOM_TV, "sat TV"},
	{C.PCIC_SATCOM, C.PCIS_SATCOM_AUDIO, "sat audio"},
	{C.PCIC_SATCOM, C.PCIS_SATCOM_VOICE, "sat voice"},
	{C.PCIC_SATCOM, C.PCIS_SATCOM_DATA, "sat data"},
	{C.PCIC_CRYPTO, -1, "encrypt/decrypt"},
	{C.PCIC_CRYPTO, C.PCIS_CRYPTO_NETCOMP, "network/computer crypto"},
	{C.PCIC_CRYPTO, C.PCIS_CRYPTO_NETCOMP, "entertainment crypto"},
	{C.PCIC_DASP, -1, "dasp"},
	{C.PCIC_DASP, C.PCIS_DASP_DPIO, "DPIO module"},
}

func guessClass(class uint8) string {
	for _, entry := range pciNomatchTab {
		if entry.class == class && entry.subclass == -1 {
			return entry.desc
		}
	}
	return ""
}

func guessSubclass(class, subclass uint8) string {
	for _, entry := range pciNomatchTab {
		if entry.class == class && entry.subclass == int(subclass) {
			return entry.desc
		}
	}
	return ""
}

func parsePCIDatabase(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var currentVendor uint16
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "\t") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			id, err := strconv.ParseUint(fields[0], 16, 16)
			if err != nil {
				continue
			}
			currentVendor = uint16(id)
			pciVendors[currentVendor] = strings.Join(fields[1:], " ")
		} else {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			id, err := strconv.ParseUint(fields[0], 16, 16)
			if err != nil {
				continue
			}
			pciDevices[uint32(currentVendor)<<16|uint32(id)] = strings.Join(fields[1:], " ")
		}
	}
	return scanner.Err()
}

func GetPCIDevices() ([]PCIDevice, error) {
	var devices []PCIDevice

	dbfile := "/usr/share/misc/pci_vendors"

	parseOnce.Do(func() {
		parseErr = parsePCIDatabase(dbfile)
	})

	if parseErr != nil {
		return devices, fmt.Errorf("failed to parse PCI database: %w", parseErr)
	}

	cDev := C.CString("/dev/pci")
	defer C.free(unsafe.Pointer(cDev))

	fd := C.wrap_open(cDev, C.O_RDONLY, 0)
	if fd < 0 {
		return devices, fmt.Errorf("failed to open /dev/pci")
	}
	defer C.close(fd)

	confSize := C.size_t(C.sizeof_struct_pci_conf * maxDevices)
	confPtr := C.malloc(confSize)
	if confPtr == nil {
		return devices, fmt.Errorf("failed to allocate memory for PCI config")
	}
	defer C.free(confPtr)

	var pc C.struct_pci_conf_io
	pc.match_buf_len = C.u_int32_t(confSize)
	pc.matches = (*C.struct_pci_conf)(confPtr)

	for {
		ret := C.wrap_pci_getconf(fd, &pc)
		if ret < 0 {
			return devices, fmt.Errorf("PCIOCGETCONF ioctl failed")
		}

		switch pc.status {
		case C.PCI_GETCONF_LIST_CHANGED:
			return devices, fmt.Errorf("PCI device list changed; please retry")
		case C.PCI_GETCONF_ERROR:
			return devices, fmt.Errorf("PCIOCGETCONF ioctl returned an error")
		}

		for i := 0; i < int(pc.num_matches); i++ {
			p := (*C.struct_pci_conf)(unsafe.Pointer(uintptr(confPtr) + uintptr(i)*C.sizeof_struct_pci_conf))

			name := C.GoString(&p.pd_name[0])
			unit := int(p.pd_unit)
			if name == "" {
				name = "none"
			}

			vendorName := pciVendors[uint16(p.pc_vendor)]
			deviceName := pciDevices[uint32(p.pc_vendor)<<16|uint32(p.pc_device)]
			className := guessClass(uint8(p.pc_class))
			subclassName := guessSubclass(uint8(p.pc_class), uint8(p.pc_subclass))

			devices = append(devices, PCIDevice{
				Name:      name,
				Unit:      unit,
				Domain:    int(p.pc_sel.pc_domain),
				Bus:       int(p.pc_sel.pc_bus),
				Device:    int(p.pc_sel.pc_dev),
				Function:  int(p.pc_sel.pc_func),
				Class:     uint32(p.pc_class)<<16 | uint32(p.pc_subclass)<<8 | uint32(p.pc_progif),
				Rev:       uint8(p.pc_revid),
				HDR:       uint8(p.pc_hdr),
				Vendor:    uint16(p.pc_vendor),
				SubVendor: uint16(p.pc_subvendor),
				SubDevice: uint16(p.pc_subdevice),
				Names: struct {
					Vendor   string `json:"vendor"`
					Device   string `json:"device"`
					Class    string `json:"class"`
					Subclass string `json:"subclass"`
				}{
					Vendor:   vendorName,
					Device:   deviceName,
					Class:    className,
					Subclass: subclassName,
				},
			})
		}

		if pc.status != C.PCI_GETCONF_MORE_DEVS {
			break
		}
	}

	return devices, nil
}
