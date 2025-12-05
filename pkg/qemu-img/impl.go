package qemuimg

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/alchemillahq/sylve/pkg/exe"
)

type qimg struct {
	exec exe.Executor
	sudo bool
}

func (q *qimg) run(in io.Reader, out io.Writer, cmd string, args ...string) (string, error) {
	if q.sudo {
		args = append([]string{cmd}, args...)
		cmd = "sudo"
	}

	var stdout, stderr bytes.Buffer
	cmdOut := out
	if cmdOut == nil {
		cmdOut = &stdout
	}

	if err := q.exec.Run(in, cmdOut, &stderr, cmd, args...); err != nil {
		return "", fmt.Errorf("qemuimg: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	if out != nil {
		return "", nil
	}

	return stdout.String(), nil
}
