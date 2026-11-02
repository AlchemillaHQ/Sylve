// SPDX-License-Identifier: BSD-2-Clause

package cmd

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/dustin/go-humanize"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v3"
)

var execContainerProcess = syscall.Exec

func containerOperation(ctx context.Context, command *cli.Command, write bool, payload consoleprotocol.ContainerPayload) error {
	operation := consoleprotocol.OperationContainerRead
	if write {
		operation = consoleprotocol.OperationContainerWrite
	}
	payload.JSON = command.Bool("json")
	return executeConsoleOperation(ctx, command, operation, payload, payload.JSON)
}

func containerOCID(command *cli.Command) (uint, error) {
	return commandJailCTID(command, "ocid")
}

func createStack(ctx context.Context, command *cli.Command, plan containerServiceInterfaces.CatalogPlan) error {
	for _, flag := range command.Flags {
		name := flag.Names()[0]
		if command.IsSet(name) && !slices.Contains([]string{"app", "set", "choice", "variant", "ocid", "pool", "start-at-boot", "json"}, name) {
			return fmt.Errorf("--%s sets up one container, and %s is %d; set it on each after creation", name, plan.Stack, len(plan.Containers))
		}
	}
	ocid, err := containerOCID(command)
	if err != nil {
		return err
	}
	if !command.IsSet("pool") {
		return fmt.Errorf("--pool is required")
	}
	stack := containerServiceInterfaces.StackRequest{Stack: plan.Stack, Pool: command.String("pool"), OCID: ocid,
		StartAtBoot: command.Bool("start-at-boot"), Containers: plan.Containers}
	return containerOperation(ctx, command, true, consoleprotocol.ContainerPayload{Action: "stack-create", Request: marshalRequest(stack)})
}

func stackMembers(ctx context.Context, command *cli.Command, ocid uint) ([]uint, error) {
	read := func(action string, ocid uint, out any) error {
		response, err := executeConsoleOperationResponse(ctx, command, consoleprotocol.OperationContainerRead,
			consoleprotocol.ContainerPayload{Action: action, OCID: ocid, JSON: true})
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(response.Output), out)
	}
	var detail containerServiceInterfaces.Detail
	if err := read("get", ocid, &detail); err != nil {
		return nil, err
	}
	if detail.Stack == "" {
		return nil, fmt.Errorf("container %d is not part of a stack", ocid)
	}
	var items []containerServiceInterfaces.ListItem
	if err := read("list", 0, &items); err != nil {
		return nil, err
	}
	var members []uint
	for _, item := range items {
		if item.Stack == detail.Stack {
			members = append(members, item.OCID)
		}
	}
	return members, nil
}

func marshalRequest(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func execContainerCommand(ctx context.Context, command *cli.Command, ocid uint, args ...string) error {
	response, err := executeConsoleOperationResponse(ctx, command, consoleprotocol.OperationContainerRead,
		consoleprotocol.ContainerPayload{Action: "command", OCID: ocid, Args: args, JSON: true})
	if err != nil {
		return err
	}
	var podman consoleprotocol.ContainerCommand
	if err := json.Unmarshal([]byte(response.Output), &podman); err != nil {
		return fmt.Errorf("invalid_container_command_response: %w", err)
	}
	return execContainerProcess(podman.Path, podman.Args, podman.Env)
}

func splitMount(value string) (string, string, bool, error) {
	parts := strings.Split(value, ":")
	readOnly := len(parts) == 3 && parts[2] == "ro"
	if len(parts) < 2 || len(parts) > 3 || (len(parts) == 3 && !readOnly) || parts[0] == "" || parts[1] == "" {
		return "", "", false, fmt.Errorf("invalid mount %q: expected SOURCE:TARGET[:ro]", value)
	}
	return parts[0], parts[1], readOnly, nil
}

func parsePort(value string) (containerModels.Port, error) {
	port := containerModels.Port{Protocol: "tcp"}
	if mapping, protocol, ok := strings.Cut(value, "/"); ok {
		value, port.Protocol = mapping, protocol
	}
	parts := strings.Split(value, ":")
	if len(parts) == 3 {
		port.HostIP, parts = parts[0], parts[1:]
	}
	if len(parts) != 2 {
		return port, fmt.Errorf("invalid port %q: expected [HOSTIP:]HOSTPORT:CONTAINERPORT[/tcp|udp]", value)
	}
	host, hostErr := strconv.ParseUint(parts[0], 10, 16)
	container, containerErr := strconv.ParseUint(parts[1], 10, 16)
	if hostErr != nil || containerErr != nil {
		return port, fmt.Errorf("invalid port %q", value)
	}
	port.HostPort, port.ContainerPort = uint16(host), uint16(container)
	return port, nil
}

func specFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "image", Usage: "Image reference, e.g. docker.io/library/nginx:latest"},
		&cli.StringFlag{Name: "os", Usage: "Image OS: freebsd or linux (default: detect, preferring freebsd)"},
		&cli.StringFlag{Name: "network", Usage: "Network name, or host (default: default)"},
		&cli.StringFlag{Name: "ip", Usage: "Static IPv4 address on the network"},
		&cli.StringSliceFlag{Name: "port", Usage: "Publish [HOSTIP:]HOSTPORT:CONTAINERPORT[/tcp|udp] (repeatable)"},
		&cli.StringSliceFlag{Name: "env", Usage: "Environment variable NAME=VALUE (repeatable)"},
		&cli.StringSliceFlag{Name: "volume", Usage: "Managed volume NAME:TARGET[:ro] (repeatable)"},
		&cli.StringSliceFlag{Name: "dataset", Usage: "ZFS dataset POOL/DATASET:TARGET[:ro] (repeatable)"},
		&cli.StringSliceFlag{Name: "mount", Usage: "Host directory PATH:TARGET[:ro] (repeatable)"},
		&cli.StringFlag{Name: "restart", Usage: "Restart policy: no, on-failure, always, unless-stopped"},
		&cli.StringSliceFlag{Name: "annotation", Usage: "Annotation KEY=VALUE, e.g. org.freebsd.jail.allow.mlock=true (repeatable)"},
		&cli.StringSliceFlag{Name: "add-host", Usage: "Extra /etc/hosts entry NAME:ADDRESS (repeatable)"},
		&cli.StringFlag{Name: "shm-size", Usage: "Size of /dev/shm, e.g. 1g"},
		&cli.StringFlag{Name: "hostname", Usage: "Hostname (default: the container name)"},
		&cli.StringFlag{Name: "workdir", Usage: "Working directory, replacing the image's"},
		&cli.StringFlag{Name: "user", Usage: "User or UID[:GID] to run as, replacing the image's"},
		&cli.StringSliceFlag{Name: "entrypoint", Usage: "Entrypoint argument, replacing the image's entrypoint (repeatable, in order)"},
		&cli.UintFlag{Name: "restart-retries", Usage: "Restarts after failures with --restart on-failure (0 is unlimited)"},
		&cli.UintFlag{Name: "stop-timeout", Usage: "Seconds a stop waits before killing (default: 10)"},
		&cli.StringFlag{Name: "volume-owner", Usage: "UID:GID owning the volumes --volume creates, e.g. 1000:1000"},
		&cli.StringSliceFlag{Name: "remove-port", Usage: "Stop publishing HOSTPORT[/tcp|udp] (repeatable)"},
		&cli.StringSliceFlag{Name: "unset-env", Usage: "Remove the environment variable NAME (repeatable)"},
		&cli.StringSliceFlag{Name: "remove-mount", Usage: "Remove the mount at TARGET (repeatable)"},
	}
}

func removeMatching[T any](items []T, what string, match func(T) bool) ([]T, error) {
	kept := slices.DeleteFunc(slices.Clone(items), match)
	if len(kept) == len(items) {
		return items, fmt.Errorf("nothing to remove: %s", what)
	}
	return kept, nil
}

func applySpecFlags(command *cli.Command, spec *containerServiceInterfaces.Spec) error {
	var err error
	for _, value := range command.StringSlice("remove-port") {
		port, protocol, _ := strings.Cut(value, "/")
		protocol = cmp.Or(protocol, "tcp")
		if spec.Ports, err = removeMatching(spec.Ports, "port "+value, func(item containerModels.Port) bool {
			return strconv.Itoa(int(item.HostPort)) == port && item.Protocol == protocol
		}); err != nil {
			return err
		}
	}
	for _, name := range command.StringSlice("unset-env") {
		if spec.Env, err = removeMatching(spec.Env, "environment variable "+name,
			func(item containerModels.EnvVar) bool { return item.Name == name }); err != nil {
			return err
		}
	}
	for _, target := range command.StringSlice("remove-mount") {
		if spec.Mounts, err = removeMatching(spec.Mounts, "mount at "+target,
			func(item containerModels.Mount) bool { return item.Target == target }); err != nil {
			return err
		}
	}

	if command.IsSet("image") {
		spec.OS = ""
	}
	for name, field := range map[string]*string{"image": &spec.Image, "os": &spec.OS, "network": &spec.Network, "ip": &spec.IP,
		"restart": &spec.Restart, "shm-size": &spec.ShmSize, "hostname": &spec.Hostname, "workdir": &spec.WorkDir, "user": &spec.User} {
		if command.IsSet(name) {
			*field = command.String(name)
		}
	}
	for name, field := range map[string]*uint{"restart-retries": &spec.RestartRetries, "stop-timeout": &spec.StopTimeout} {
		if command.IsSet(name) {
			*field = command.Uint(name)
		}
	}
	if command.IsSet("entrypoint") {
		spec.Entrypoint = command.StringSlice("entrypoint")
	}
	for _, value := range command.StringSlice("annotation") {
		key, val, ok := strings.Cut(value, "=")
		if !ok {
			return fmt.Errorf("invalid annotation %q: expected KEY=VALUE", value)
		}
		if spec.Annotations == nil {
			spec.Annotations = map[string]string{}
		}
		spec.Annotations[key] = val
	}
	spec.ExtraHosts = append(spec.ExtraHosts, command.StringSlice("add-host")...)
	for _, value := range command.StringSlice("port") {
		port, err := parsePort(value)
		if err != nil {
			return err
		}
		spec.Ports = append(spec.Ports, port)
	}
	for _, value := range command.StringSlice("env") {
		name, val, ok := strings.Cut(value, "=")
		if !ok {
			return fmt.Errorf("invalid env %q: expected NAME=VALUE", value)
		}
		if i := slices.IndexFunc(spec.Env, func(item containerModels.EnvVar) bool { return item.Name == name }); i >= 0 {
			spec.Env[i].Value = val
		} else {
			spec.Env = append(spec.Env, containerModels.EnvVar{Name: name, Value: val})
		}
	}
	for kind, flag := range map[containerModels.MountType]string{containerModels.MountVolume: "volume", containerModels.MountDataset: "dataset", containerModels.MountPath: "mount"} {
		for _, value := range command.StringSlice(flag) {
			source, target, readOnly, err := splitMount(value)
			if err != nil {
				return err
			}
			mount := containerModels.Mount{Type: kind, Source: source, Target: target, ReadOnly: readOnly}
			if kind == containerModels.MountVolume {
				mount.Owner = command.String("volume-owner")
			}
			spec.Mounts = append(spec.Mounts, mount)
		}
	}
	return nil
}

func keyValues(command *cli.Command, flag string) (map[string]string, error) {
	values := map[string]string{}
	for _, value := range command.StringSlice(flag) {
		name, val, ok := strings.Cut(value, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --%s %q: expected NAME=VALUE", flag, value)
		}
		values[name] = val
	}
	return values, nil
}

func newContainersCommand() *cli.Command {
	jsonFlag := &cli.BoolFlag{Name: "json", Usage: "output in JSON format"}
	ocidFlag := &cli.IntFlag{Name: "ocid", Usage: "Container ID (1-9999)", Required: true}
	poolFlag := &cli.StringFlag{Name: "pool", Usage: "ZFS pool", Required: true}

	task := func(action, usage string) *cli.Command {
		return &cli.Command{Name: action, Usage: usage, Flags: []cli.Flag{jsonFlag, ocidFlag},
			Action: func(ctx context.Context, command *cli.Command) error {
				ocid, err := containerOCID(command)
				if err != nil {
					return err
				}
				return containerOperation(ctx, command, true, consoleprotocol.ContainerPayload{Action: action, OCID: ocid})
			}}
	}

	createCmd := &cli.Command{
		Name:      "create",
		Usage:     "Create a container from flags, a catalog app, or a JSON request file",
		ArgsUsage: "[-- command [args...]]",
		Description: "Use --app to start from a catalog app (see 'containers catalog show'), --file with a complete CreateRequest JSON document, or flags. " +
			"Explicit flags override what --app or --file set; arguments after -- replace the image's command.",
		DisableSliceFlagSeparator: true,
		Flags: append([]cli.Flag{jsonFlag,
			&cli.StringFlag{Name: "file", Usage: "JSON CreateRequest file"},
			&cli.StringFlag{Name: "app", Usage: "Catalog app to create, as source/id, e.g. daemonless/jellyfin"},
			&cli.StringSliceFlag{Name: "set", Usage: "App setting NAME=VALUE (repeatable; see 'containers catalog show')"},
			&cli.StringSliceFlag{Name: "choice", Usage: "App choice ID=OPTION, e.g. database=sqlite (repeatable)"},
			&cli.StringFlag{Name: "variant", Usage: "App image variant (default: the app's default)"},
			&cli.IntFlag{Name: "ocid", Usage: "Container ID (1-9999)"},
			&cli.StringFlag{Name: "name", Usage: "Container name"},
			&cli.StringFlag{Name: "description", Usage: "Description"},
			&cli.StringFlag{Name: "pool", Usage: "ZFS pool for the image and volumes"},
			&cli.UintFlag{Name: "cores", Usage: "CPU limit in cores (0 is unlimited)"},
			&cli.StringFlag{Name: "memory", Usage: "Memory limit, e.g. 512MiB or 2GiB (empty is unlimited)"},
			&cli.BoolFlag{Name: "start-at-boot", Usage: "Start the container at boot"},
			&cli.IntFlag{Name: "start-order", Usage: "Boot start order"},
			&cli.BoolFlag{Name: "start", Usage: "Start the container once created"},
		}, specFlags()...),
		Action: func(ctx context.Context, command *cli.Command) error {
			var req containerServiceInterfaces.CreateRequest
			if command.IsSet("app") && command.IsSet("file") {
				return fmt.Errorf("use --app or --file, not both")
			}
			if path := command.String("file"); path != "" {
				var err error
				if req, err = consoleprotocol.LoadContainerFile[containerServiceInterfaces.CreateRequest](path); err != nil {
					return err
				}
			}
			if app := command.String("app"); app != "" {
				values, err := keyValues(command, "set")
				if err != nil {
					return err
				}
				choices, err := keyValues(command, "choice")
				if err != nil {
					return err
				}
				settings := containerServiceInterfaces.CatalogRenderRequest{Variant: command.String("variant"), Values: values, Choices: choices}
				response, err := executeConsoleOperationResponse(ctx, command, consoleprotocol.OperationContainerRead,
					consoleprotocol.ContainerPayload{Action: "catalog-render", Args: []string{app}, Request: marshalRequest(settings), JSON: true})
				if err != nil {
					return err
				}
				var plan containerServiceInterfaces.CatalogPlan
				if err := json.Unmarshal([]byte(response.Output), &plan); err != nil || len(plan.Containers) == 0 {
					return fmt.Errorf("invalid_catalog_render_response: %v", err)
				}
				if plan.Stack != "" {
					return createStack(ctx, command, plan)
				}
				req = plan.Containers[0]
			}
			if command.IsSet("description") {
				req.Description = command.String("description")
			}
			if command.IsSet("start-order") {
				req.StartOrder = command.Int("start-order")
			}
			if command.IsSet("ocid") {
				ocid, err := containerOCID(command)
				if err != nil {
					return err
				}
				req.OCID = ocid
			}
			if command.IsSet("name") {
				req.Name = command.String("name")
			}
			if command.IsSet("pool") {
				req.Pool = command.String("pool")
			}
			if command.IsSet("start-at-boot") {
				req.StartAtBoot = command.Bool("start-at-boot")
			}
			if command.IsSet("cores") {
				req.Cores = command.Uint("cores")
			}
			if command.IsSet("memory") {
				memory, err := humanize.ParseBytes(command.String("memory"))
				if err != nil {
					return fmt.Errorf("invalid --memory: %w", err)
				}
				req.Memory = memory
			}
			if command.IsSet("start") {
				req.StartAfterCreate = command.Bool("start")
			}
			if err := applySpecFlags(command, &req.Spec); err != nil {
				return err
			}
			if args := command.Args().Slice(); len(args) > 0 {
				req.Command = args
			}
			if req.OCID == 0 || req.Name == "" || req.Pool == "" || req.Image == "" {
				return fmt.Errorf("--ocid, --name, --pool and --image are required (directly, or from --app or --file)")
			}
			return containerOperation(ctx, command, true, consoleprotocol.ContainerPayload{Action: "create", OCID: req.OCID, Request: marshalRequest(req)})
		},
	}

	updateCmd := &cli.Command{
		Name:      "update",
		Usage:     "Recreate a container from an edited spec, keeping its volumes",
		ArgsUsage: "[-- command [args...]]",
		Description: "Use --file with a complete Spec JSON document, or change the current spec with flags. Flags that add ports or mounts append to the current spec, " +
			"--env replaces a variable's value, and --remove-port, --unset-env and --remove-mount take things out; arguments after -- replace the command.",
		DisableSliceFlagSeparator: true,
		Flags: append([]cli.Flag{jsonFlag, ocidFlag,
			&cli.StringFlag{Name: "file", Usage: "JSON Spec file"},
			&cli.BoolFlag{Name: "pull", Usage: "Pull the image even if it is present"},
		}, specFlags()...),
		Action: func(ctx context.Context, command *cli.Command) error {
			ocid, err := containerOCID(command)
			if err != nil {
				return err
			}
			var spec containerServiceInterfaces.Spec
			if path := command.String("file"); path != "" {
				if spec, err = consoleprotocol.LoadContainerFile[containerServiceInterfaces.Spec](path); err != nil {
					return err
				}
			} else {
				response, err := executeConsoleOperationResponse(ctx, command, consoleprotocol.OperationContainerRead,
					consoleprotocol.ContainerPayload{Action: "get", OCID: ocid, JSON: true})
				if err != nil {
					return err
				}
				var detail containerServiceInterfaces.Detail
				if err := json.Unmarshal([]byte(response.Output), &detail); err != nil {
					return fmt.Errorf("invalid_container_response: %w", err)
				}
				spec = containerServiceInterfaces.SpecOf(detail.Container)
			}
			if err := applySpecFlags(command, &spec); err != nil {
				return err
			}
			if args := command.Args().Slice(); len(args) > 0 {
				spec.Command = args
			}
			spec.PullImage = command.Bool("pull")
			return containerOperation(ctx, command, true, consoleprotocol.ContainerPayload{Action: "update", OCID: ocid, Request: marshalRequest(spec)})
		},
	}

	optionsCmd := &cli.Command{
		Name:  "options",
		Usage: "Change a container's name, description, or boot options",
		Flags: []cli.Flag{jsonFlag, ocidFlag,
			&cli.StringFlag{Name: "name", Usage: "New name"},
			&cli.StringFlag{Name: "description", Usage: "Description"},
			&cli.UintFlag{Name: "cores", Usage: "CPU limit in cores (0 is unlimited)"},
			&cli.StringFlag{Name: "memory", Usage: "Memory limit, e.g. 512MiB or 2GiB (0 is unlimited)"},
			&cli.BoolFlag{Name: "start-at-boot", Usage: "Start at boot"},
			&cli.IntFlag{Name: "start-order", Usage: "Boot start order"},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			ocid, err := containerOCID(command)
			if err != nil {
				return err
			}
			var req containerServiceInterfaces.OptionsRequest
			if command.IsSet("name") {
				req.Name = new(string)
				*req.Name = command.String("name")
			}
			if command.IsSet("description") {
				req.Description = new(string)
				*req.Description = command.String("description")
			}
			if command.IsSet("start-at-boot") {
				req.StartAtBoot = new(bool)
				*req.StartAtBoot = command.Bool("start-at-boot")
			}
			if command.IsSet("start-order") {
				req.StartOrder = new(int)
				*req.StartOrder = command.Int("start-order")
			}
			if command.IsSet("cores") {
				req.Cores = new(uint)
				*req.Cores = command.Uint("cores")
			}
			if command.IsSet("memory") {
				memory, err := humanize.ParseBytes(command.String("memory"))
				if err != nil {
					return fmt.Errorf("invalid --memory: %w", err)
				}
				req.Memory = &memory
			}
			return containerOperation(ctx, command, true, consoleprotocol.ContainerPayload{Action: "options", OCID: ocid, Request: marshalRequest(req)})
		},
	}

	stopOnCommand := 1
	execCmd := &cli.Command{
		Name:         "exec",
		Usage:        "Run a command in a running container",
		ArgsUsage:    "-- <command> [args...]",
		Description:  "Input, output, exit status, and signals are preserved; a terminal is allocated when stdin is one.",
		StopOnNthArg: &stopOnCommand,
		Flags:        []cli.Flag{ocidFlag},
		Action: func(ctx context.Context, command *cli.Command) error {
			ocid, err := containerOCID(command)
			if err != nil {
				return err
			}
			args := command.Args().Slice()
			if len(args) == 0 || args[0] == "" {
				return fmt.Errorf("specify a command after --")
			}
			podman := []string{"exec", "--interactive"}
			if isatty.IsTerminal(os.Stdin.Fd()) {
				podman = append(podman, "--tty")
			}
			return execContainerCommand(ctx, command, ocid, append(append(podman, "{}"), args...)...)
		},
	}

	consoleCmd := &cli.Command{
		Name:  "console",
		Usage: "Open an interactive shell in a running container",
		Flags: []cli.Flag{ocidFlag, &cli.StringFlag{Name: "shell", Value: "/bin/sh", Usage: "Shell to run"}},
		Action: func(ctx context.Context, command *cli.Command) error {
			ocid, err := containerOCID(command)
			if err != nil {
				return err
			}
			return execContainerCommand(ctx, command, ocid, "exec", "--interactive", "--tty", "{}", command.String("shell"))
		},
	}

	logsCmd := &cli.Command{
		Name:  "logs",
		Usage: "Show a container's output",
		Flags: []cli.Flag{ocidFlag,
			&cli.IntFlag{Name: "tail", Value: 500, Usage: "Number of lines to show (-1 for all)"},
			&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, Usage: "Follow new output"},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			ocid, err := containerOCID(command)
			if err != nil {
				return err
			}
			args := []string{"logs", "--tail", strconv.Itoa(command.Int("tail"))}
			if command.Bool("follow") {
				args = append(args, "--follow")
			}
			return execContainerCommand(ctx, command, ocid, append(args, "{}")...)
		},
	}

	read := func(name, usage, action string, flags ...cli.Flag) *cli.Command {
		return &cli.Command{Name: name, Usage: usage, Flags: append([]cli.Flag{jsonFlag}, flags...),
			Action: func(ctx context.Context, command *cli.Command) error {
				payload := consoleprotocol.ContainerPayload{Action: action, Pool: command.String("pool")}
				if command.IsSet("ocid") {
					ocid, err := containerOCID(command)
					if err != nil {
						return err
					}
					payload.OCID = ocid
				}
				return containerOperation(ctx, command, false, payload)
			}}
	}
	idFlag := &cli.IntFlag{Name: "id", Usage: "ID", Required: true}
	byID := func(name, usage, action string) *cli.Command {
		return &cli.Command{Name: name, Usage: usage, Flags: []cli.Flag{jsonFlag, idFlag},
			Action: func(ctx context.Context, command *cli.Command) error {
				id, err := commandPositiveUint(command, "id")
				if err != nil {
					return err
				}
				return containerOperation(ctx, command, true, consoleprotocol.ContainerPayload{Action: action, ID: id})
			}}
	}
	poolAction := func(name, usage, action string, flags []cli.Flag, build func(*cli.Command, *consoleprotocol.ContainerPayload) error) *cli.Command {
		return &cli.Command{Name: name, Usage: usage, Flags: append([]cli.Flag{jsonFlag, poolFlag}, flags...),
			Action: func(ctx context.Context, command *cli.Command) error {
				payload := consoleprotocol.ContainerPayload{Action: action, Pool: command.String("pool")}
				if build != nil {
					if err := build(command, &payload); err != nil {
						return err
					}
				}
				return containerOperation(ctx, command, true, payload)
			}}
	}

	return &cli.Command{
		Name:  "containers",
		Usage: "Manage Podman containers",
		Commands: []*cli.Command{
			read("list", "List containers", "list"),
			read("get", "Show a container", "get", ocidFlag),
			createCmd,
			updateCmd,
			{Name: "delete", Usage: "Delete a container, or with --stack its whole stack (volumes are kept unless --delete-volumes)",
				Flags: []cli.Flag{jsonFlag, ocidFlag,
					&cli.BoolFlag{Name: "delete-volumes", Usage: "destroy the containers' volumes"},
					&cli.BoolFlag{Name: "stack", Usage: "delete every container of the container's stack"}},
				Action: func(ctx context.Context, command *cli.Command) error {
					ocid, err := containerOCID(command)
					if err != nil {
						return err
					}
					ocids := []uint{ocid}
					if command.Bool("stack") {
						if ocids, err = stackMembers(ctx, command, ocid); err != nil {
							return err
						}
					}
					request := marshalRequest(containerServiceInterfaces.DeleteRequest{DeleteVolumes: command.Bool("delete-volumes")})
					for _, member := range ocids {
						if err := containerOperation(ctx, command, true, consoleprotocol.ContainerPayload{Action: "delete", OCID: member, Request: request}); err != nil {
							return err
						}
					}
					return nil
				}},
			task("start", "Start a container"),
			task("stop", "Stop a container"),
			task("restart", "Restart a container"),
			task("kill", "Kill a container"),
			optionsCmd,
			execCmd,
			consoleCmd,
			logsCmd,
			read("ports", "List host ports in use, by listening programs or other containers", "ports",
				&cli.IntFlag{Name: "ocid", Usage: "Leave out this container's own ports"}),
			{Name: "catalog", Usage: "Browse the daemonless application catalogs", Commands: []*cli.Command{
				{Name: "list", Usage: "List apps that run on this host",
					Flags: []cli.Flag{jsonFlag,
						&cli.StringFlag{Name: "search", Usage: "Only apps whose name, ID or description contains this"},
						&cli.StringFlag{Name: "category", Usage: "Only apps in this category"},
					},
					Action: func(ctx context.Context, command *cli.Command) error {
						return containerOperation(ctx, command, false, consoleprotocol.ContainerPayload{Action: "catalog",
							Args: []string{command.String("search"), command.String("category")}})
					}},
				{Name: "show", Usage: "Show an app's settings, choices, and where it keeps its data",
					Flags: []cli.Flag{jsonFlag, &cli.StringFlag{Name: "app", Required: true, Usage: "App as source/id, e.g. daemonless/jellyfin"}},
					Action: func(ctx context.Context, command *cli.Command) error {
						return containerOperation(ctx, command, false, consoleprotocol.ContainerPayload{Action: "catalog-show",
							Args: []string{command.String("app")}})
					}},
			}},
			{Name: "networks", Usage: "Manage container networks", Commands: []*cli.Command{
				read("list", "List networks", "networks"),
				{Name: "create", Usage: "Create a NAT network, or with --switch, a network attached to a switch",
					Flags: []cli.Flag{jsonFlag,
						&cli.StringFlag{Name: "name", Required: true, Usage: "Network name"},
						&cli.StringFlag{Name: "switch", Usage: "Attach to this standard or manual switch"},
						&cli.StringFlag{Name: "subnet", Usage: "IPv4 subnet (chosen automatically for NAT networks)"},
						&cli.StringFlag{Name: "gateway", Usage: "Gateway (required with --switch)"},
						&cli.StringFlag{Name: "range-start", Usage: "First address handed to containers"},
						&cli.StringFlag{Name: "range-end", Usage: "Last address handed to containers"},
					},
					Action: func(ctx context.Context, command *cli.Command) error {
						req := containerServiceInterfaces.NetworkRequest{Name: command.String("name"), Switch: command.String("switch"),
							Subnet: command.String("subnet"), Gateway: command.String("gateway"),
							RangeStart: command.String("range-start"), RangeEnd: command.String("range-end")}
						return containerOperation(ctx, command, true, consoleprotocol.ContainerPayload{Action: "network-create", Request: marshalRequest(req)})
					}},
				byID("delete", "Delete an unused network", "network-delete"),
			}},
			{Name: "images", Usage: "Manage images on a pool", Commands: []*cli.Command{
				read("list", "List images", "images", poolFlag),
				poolAction("pull", "Pull an image", "image-pull",
					[]cli.Flag{&cli.StringFlag{Name: "image", Required: true, Usage: "Image reference"}, &cli.StringFlag{Name: "os", Usage: "freebsd or linux (default: detect)"}},
					func(command *cli.Command, payload *consoleprotocol.ContainerPayload) error {
						payload.Request = marshalRequest(map[string]string{"Image": command.String("image"), "OS": command.String("os")})
						return nil
					}),
				poolAction("remove", "Remove an image", "image-remove",
					[]cli.Flag{&cli.StringFlag{Name: "id", Required: true, Usage: "Image ID or name"}},
					func(command *cli.Command, payload *consoleprotocol.ContainerPayload) error {
						payload.Args = []string{command.String("id")}
						return nil
					}),
				poolAction("prune", "Remove images no container uses", "image-prune", nil, nil),
			}},
			{Name: "registry", Usage: "Manage registry logins used for pulls", Commands: []*cli.Command{
				read("list", "List registry logins", "credentials"),
				{Name: "login", Usage: "Save a registry login; the password is read from stdin",
					Flags: []cli.Flag{jsonFlag,
						&cli.StringFlag{Name: "registry", Required: true, Usage: "Registry host, e.g. ghcr.io"},
						&cli.StringFlag{Name: "username", Required: true, Usage: "Username"},
					},
					Action: func(ctx context.Context, command *cli.Command) error {
						if isatty.IsTerminal(os.Stdin.Fd()) {
							fmt.Fprint(os.Stderr, "Password: ")
						}
						password, err := bufio.NewReader(os.Stdin).ReadString('\n')
						password = strings.TrimRight(password, "\r\n")
						if password == "" {
							return fmt.Errorf("a password is required on stdin: %v", err)
						}
						req := containerServiceInterfaces.RegistryCredentialRequest{Registry: command.String("registry"), Username: command.String("username"), Password: password}
						return containerOperation(ctx, command, true, consoleprotocol.ContainerPayload{Action: "credential-save", Request: marshalRequest(req)})
					}},
				byID("logout", "Delete a registry login", "credential-delete"),
			}},
		},
	}
}
