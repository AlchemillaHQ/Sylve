package qemuimg

const qemuImgPath = "/usr/local/bin/qemu-img"

type QemuImg interface {
	CheckTools() error
	Info(path string) (*ImageInfo, error)
	InfoBackingChain(path string) ([]*ImageInfo, error)
	Convert(src, dst string, outFmt DiskFormat) error
}

func (q *qimg) CheckTools() error {
	_, err := q.run(nil, nil, qemuImgPath, "--version")
	return err
}
