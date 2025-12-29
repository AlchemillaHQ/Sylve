package qemuimg

import (
	"fmt"
	"strings"
)

func (q *qimg) Convert(src, dst string, outFmt DiskFormat) error {
	src = strings.TrimSpace(src)
	dst = strings.TrimSpace(dst)

	if src == "" {
		return fmt.Errorf("qemu-img: source path is empty")
	}
	if dst == "" {
		return fmt.Errorf("qemu-img: destination path is empty")
	}
	if src == dst {
		return fmt.Errorf("source and destination paths are the same: %q", src)
	}

	if !outFmt.Valid() {
		return fmt.Errorf(
			"invalid output format %q (valid: %v)",
			outFmt,
			FormatsList(),
		)
	}

	info, err := q.Info(src)
	if err != nil {
		return fmt.Errorf("failed to read source image metadata: %w", err)
	}

	srcFmt := DiskFormat(normalizeFormat(info.Format))
	if !srcFmt.Valid() {
		return fmt.Errorf(
			"source image reports unsupported format %q",
			info.Format,
		)
	}

	if srcFmt == outFmt {
		return fmt.Errorf(
			"source image is already %s (%q)",
			outFmt,
			src,
		)
	}

	args := []string{
		"convert",
		"-f", string(srcFmt),
		"-O", string(outFmt),
		src,
		dst,
	}

	if _, err := q.run(nil, nil, "qemu-img", args...); err != nil {
		return fmt.Errorf(
			"convert %q -> %q (srcFmt=%s, outFmt=%s) failed: %w",
			src, dst, srcFmt, outFmt, err,
		)
	}

	return nil
}
