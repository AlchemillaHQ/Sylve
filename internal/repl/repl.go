package repl

import (
	"fmt"
	"os"
	"strings"

	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/chzyer/readline"
)

type Context struct {
	Auth           *auth.Service
	Jail           *jail.Service
	VirtualMachine *libvirt.Service
	Network        *network.Service
	QuitChan       chan os.Signal
}

func Start(ctx *Context) {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "sylve> ",
		HistoryFile:     "/tmp/sylve.repl.history",
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Println("REPL init failed:", err)
		return
	}
	defer rl.Close()

	fmt.Println("Sylve REPL ready. Type `help`.")

	for {
		line, err := rl.Readline()
		if err != nil {
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if !handle(ctx, line) {
			return
		}
	}
}
