package pciconf

import ()

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
	SubVendor uint16 `json:"subVendor"`
	SubDevice uint16 `json:"subDevice"`
	Names     struct {
		Vendor   string `json:"vendor"`
		Device   string `json:"device"`
		Class    string `json:"class"`
		Subclass string `json:"subclass"`
	} `json:"names"`
}
