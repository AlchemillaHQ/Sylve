package exe

import (
	"context"
	"io"
	"os/exec"
)

func NewLocalExecutor() Executor {
	return &localExec{}
}

type localExec struct{}

func (l *localExec) Run(stdin io.Reader, stdout io.Writer, stderr io.Writer, cmd string, args ...string) error {
	return l.RunContext(context.Background(), stdin, stdout, stderr, cmd, args...)
}

func (l *localExec) RunContext(
	ctx context.Context,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	cmd string,
	args ...string,
) error {
	c := exec.CommandContext(ctx, cmd, args...)
	if stdin != nil {
		c.Stdin = stdin
	}
	if stdout != nil {
		c.Stdout = stdout
	}
	if stderr != nil {
		c.Stderr = stderr
	}
	err := c.Run()
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
