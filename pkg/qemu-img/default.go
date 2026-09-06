package qemuimg

import (
	"context"

	"github.com/alchemillahq/sylve/pkg/exe"
)

// same pattern as zfs
var qi QemuImg = &qimg{exec: exe.NewLocalExecutor(), sudo: false}

func SetDefault(img QemuImg) {
	if img != nil {
		qi = img
	}
}

func CheckTools() error {
	return qi.CheckTools()
}

func Convert(src, dst string, outFmt DiskFormat) error {
	return qi.Convert(src, dst, outFmt)
}

func ConvertContext(ctx context.Context, src, dst string, outFmt DiskFormat) error {
	return qi.ConvertContext(ctx, src, dst, outFmt)
}

func Info(img string) (*ImageInfo, error) {
	return qi.Info(img)
}
