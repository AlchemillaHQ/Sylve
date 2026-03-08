package pciconf

import (
	"fmt"
	"os"
)

type PCIDevice struct {
	Name      string `json:"name"`
	Unit      int    `json:"unit"`
	Domain    int    `json:"domain"`
	Bus       int    `json:"bus"`
	Device    int    `json:"device"`
	Function  int    `json:"function"`
	Class     uint32 `json:"class"`
	Rev       uint8  `json:"rev"`
	HDR       uint8  `json:"hdr"`
	Vendor    uint16 `json:"vendor"`
	SubVendor uint16 `json:"subvendor"`
	SubDevice uint16 `json:"subdevice"`
	Names     struct {
		Vendor   string `json:"vendor"`
		Device   string `json:"device"`
		Class    string `json:"class"`
		Subclass string `json:"subclass"`
	} `json:"names"`
}

func PrintPCIDevices() {
	devices, err := GetPCIDevices()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error retrieving PCI devices: %v\n", err)
		return
	}

	for _, device := range devices {
		fmt.Printf("%s%d@pci%d:%d:%d:%d:\tclass=0x%06x rev=0x%02x hdr=0x%02x vendor=0x%04x device=0x%04x subvendor=0x%04x subdevice=0x%04x\n",
			device.Name, device.Unit,
			device.Domain, device.Bus, device.Device, device.Function,
			device.Class, device.Rev, device.HDR,
			uint16(device.Class>>16), uint16(device.Class&0xFFFF),
			device.Vendor, device.SubDevice,
		)

		if device.Names.Vendor != "" {
			fmt.Printf("\tvendor \t= %s\n", device.Names.Vendor)
		}
		if device.Names.Device != "" {
			fmt.Printf("\tdevice\t= %s\n", device.Names.Device)
		}
		if device.Names.Class != "" {
			fmt.Printf("\tclass\t= %s\n", device.Names.Class)
		}
		if device.Names.Subclass != "" {
			fmt.Printf("\tsubclass\t= %s\n", device.Names.Subclass)
		}
	}
}
