package qemuimg

import "strings"

type DiskFormat string

const (
	FormatRaw   DiskFormat = "raw"
	FormatQCOW2 DiskFormat = "qcow2"
	FormatQCOW  DiskFormat = "qcow"
	FormatQED   DiskFormat = "qed"
	FormatVDI   DiskFormat = "vdi"
	FormatVMDK  DiskFormat = "vmdk"
	FormatVPC   DiskFormat = "vpc"
	FormatVHDX  DiskFormat = "vhdx"
)

var validFormats = map[DiskFormat]struct{}{
	FormatRaw:   {},
	FormatQCOW2: {},
	FormatQCOW:  {},
	FormatQED:   {},
	FormatVDI:   {},
	FormatVMDK:  {},
	FormatVPC:   {},
	FormatVHDX:  {},
}

func normalizeFormat(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func (df DiskFormat) Valid() bool {
	_, ok := validFormats[df]
	return ok
}

func FormatsList() []DiskFormat {
	out := make([]DiskFormat, 0, len(validFormats))
	for f := range validFormats {
		out = append(out, f)
	}
	return out
}

func (df DiskFormat) IsQCOW() bool {
	return df == FormatQCOW || df == FormatQCOW2
}

func (df DiskFormat) SupportsSnapshots() bool {
	return df == FormatQCOW || df == FormatQCOW2
}
