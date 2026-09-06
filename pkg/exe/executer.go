package exe

import (
	"context"
	"io"
)

type Executor interface {
	Run(stdin io.Reader, stdout io.Writer, stderr io.Writer, cmd string, args ...string) error
	RunContext(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, cmd string, args ...string) error
}
