// SPDX-License-Identifier: BSD-2-Clause

package repl

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/services/container"
	"github.com/dustin/go-humanize"
)

func processContainerSocketRequest(ctx *Context, payload json.RawMessage, write bool) socketResponse {
	var request consoleprotocol.ContainerPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_container_request: " + err.Error()}
	}
	if ctx == nil || ctx.Container == nil || ctx.Lifecycle == nil {
		return socketResponse{Error: "container_service_unavailable"}
	}
	value, text, err := executeContainerRequest(ctx, request, write)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, value, text)
}

func executeContainerRequest(ctx *Context, request consoleprotocol.ContainerPayload, write bool) (any, string, error) {
	service, call := ctx.Container, operationContext(ctx)
	if !write {
		switch request.Action {
		case "list":
			items, err := service.List(call)
			return items, formatContainerList(items), err
		case "get":
			detail, err := service.Get(call, request.OCID)
			return detail, mustJSON(detail), err
		case "command":
			cmd, err := service.Command(call, request.OCID, request.Args...)
			if err != nil {
				return nil, "", err
			}
			command := consoleprotocol.ContainerCommand{Path: cmd.Path, Args: cmd.Args, Env: cmd.Env}
			return command, mustJSON(command), nil
		case "networks":
			networks, err := service.ListNetworks(call)
			return networks, formatContainerNetworks(networks), err
		case "images":
			images, err := service.ListImages(call, request.Pool)
			return images, formatContainerImages(images), err
		case "credentials":
			credentials, err := service.ListRegistryCredentials(call)
			return credentials, formatRegistryCredentials(credentials), err
		case "ports":
			ports, err := service.UsedPorts(call, request.OCID)
			return ports, formatUsedPorts(ports), err
		case "catalog":
			apps, err := service.Catalog(call)
			apps = filterCatalog(apps, request.Args)
			return apps, formatCatalog(apps), err
		case "catalog-show", "catalog-render":
			if len(request.Args) != 1 {
				return nil, "", fmt.Errorf("catalog_app_required: use source/id, e.g. daemonless/jellyfin")
			}
			source, id, _ := strings.Cut(request.Args[0], "/")
			if request.Action == "catalog-show" {
				template, err := service.CatalogTemplate(call, source, id)
				return template, formatCatalogTemplate(template), err
			}
			var settings containerServiceInterfaces.CatalogRenderRequest
			if err := json.Unmarshal(request.Request, &settings); err != nil {
				return nil, "", fmt.Errorf("invalid_catalog_settings: %w", err)
			}
			create, err := service.RenderCatalogApp(call, source, id, settings)
			return create, mustJSON(create), err
		}
		return nil, "", fmt.Errorf("invalid_container_action: %s", request.Action)
	}

	if request.Action != "create-stack" && slices.Contains(container.Actions, request.Action) {
		task, outcome, err := ctx.Lifecycle.RequestActionWithPayload(call, taskModels.GuestTypeContainer, request.OCID, request.Action,
			taskModels.LifecycleTaskSourceUser, "console", string(request.Request))
		if err != nil {
			return nil, "", fmt.Errorf("failed_to_%s_container: %w", request.Action, err)
		}
		result := map[string]any{"taskId": task.ID, "ocId": request.OCID, "action": request.Action, "outcome": outcome}
		return result, styledSuccessf("Container %d: %s %s (Task: %d)", request.OCID, request.Action, outcome, task.ID), nil
	}

	var err error
	switch request.Action {
	case "stack-create":
		var stack containerServiceInterfaces.StackRequest
		if err := json.Unmarshal(request.Request, &stack); err != nil {
			return nil, "", fmt.Errorf("invalid_stack_request: %w", err)
		}
		return createStack(ctx, stack)
	case "options":
		var options containerServiceInterfaces.OptionsRequest
		if err = json.Unmarshal(request.Request, &options); err == nil {
			err = service.UpdateOptions(call, request.OCID, options)
		}
	case "network-create":
		var network containerServiceInterfaces.NetworkRequest
		if err = json.Unmarshal(request.Request, &network); err == nil {
			created, createErr := service.CreateNetwork(call, network)
			if createErr != nil {
				return nil, "", createErr
			}
			return created, styledSuccessf("Network %s created (%s).", created.Name, created.Subnet), nil
		}
	case "network-delete":
		err = service.DeleteNetwork(call, request.ID)
	case "image-pull":
		var pull struct{ Image, OS string }
		if err = json.Unmarshal(request.Request, &pull); err == nil {
			err = service.PullImage(call, request.Pool, pull.Image, pull.OS)
		}
	case "image-remove":
		if len(request.Args) != 1 {
			return nil, "", fmt.Errorf("image_id_required")
		}
		err = service.RemoveImage(call, request.Pool, request.Args[0])
	case "image-prune":
		err = service.PruneImages(call, request.Pool)
	case "credential-save":
		var credential containerServiceInterfaces.RegistryCredentialRequest
		if err = json.Unmarshal(request.Request, &credential); err == nil {
			_, err = service.SaveRegistryCredential(call, credential)
		}
	case "credential-delete":
		err = service.DeleteRegistryCredential(call, request.ID)
	default:
		err = fmt.Errorf("invalid_container_action: %s", request.Action)
	}
	if err != nil {
		return nil, "", err
	}
	return map[string]bool{"ok": true}, styledSuccessf("Done."), nil
}

func createStack(ctx *Context, req containerServiceInterfaces.StackRequest) (any, string, error) {
	prepared, task, _, err := ctx.Lifecycle.RequestContainerStack(operationContext(ctx), req, taskModels.LifecycleTaskSourceUser, "console")
	if err != nil {
		return nil, "", fmt.Errorf("failed_to_create_stack: %w", err)
	}
	type member struct {
		OCID uint   `json:"ocId"`
		Name string `json:"name"`
	}
	var members []member
	var names []string
	for _, create := range prepared.Containers {
		members = append(members, member{OCID: create.OCID, Name: create.Name})
		names = append(names, fmt.Sprintf("%d (%s)", create.OCID, create.Name))
	}
	result := map[string]any{"stack": prepared.Stack, "taskId": task.ID, "containers": members}
	return result, styledSuccessf("Stack %s: create queued (Task: %d), containers %s", prepared.Stack, task.ID, strings.Join(names, ", ")), nil
}

func formatContainerList(items []containerServiceInterfaces.ListItem) string {
	if len(items) == 0 {
		return "No containers found."
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		network := containerServiceInterfaces.HostNetwork
		if item.Network != nil {
			network = item.Network.Name
		}
		rows = append(rows, []string{strconv.FormatUint(uint64(item.OCID), 10), item.Name, item.State, item.Pool, network, item.Image})
	}
	return styledTable([]string{"OCID", "Name", "State", "Pool", "Network", "Image"}, rows)
}

func formatContainerNetworks(networks []containerModels.Network) string {
	if len(networks) == 0 {
		return "No container networks found."
	}
	rows := make([][]string, 0, len(networks))
	for _, network := range networks {
		kind := "nat"
		if network.SwitchID != nil {
			kind = "switch"
		}
		rows = append(rows, []string{strconv.FormatUint(uint64(network.ID), 10), network.Name, kind, network.Bridge, network.Subnet, network.Gateway})
	}
	return styledTable([]string{"ID", "Name", "Type", "Bridge", "Subnet", "Gateway"}, rows)
}

func formatContainerImages(images []containerServiceInterfaces.Image) string {
	if len(images) == 0 {
		return "No images found."
	}
	rows := make([][]string, 0, len(images))
	for _, image := range images {
		id := image.ID
		if len(id) > 12 {
			id = id[:12]
		}
		rows = append(rows, []string{id, strings.Join(image.Names, ", "), humanize.IBytes(uint64(image.Size)), image.Created.Format("2006-01-02 15:04")})
	}
	return styledTable([]string{"ID", "Names", "Size", "Created"}, rows)
}

func formatRegistryCredentials(credentials []containerModels.RegistryCredential) string {
	if len(credentials) == 0 {
		return "No registry credentials found."
	}
	rows := make([][]string, 0, len(credentials))
	for _, credential := range credentials {
		rows = append(rows, []string{strconv.FormatUint(uint64(credential.ID), 10), credential.Registry, credential.Username})
	}
	return styledTable([]string{"ID", "Registry", "Username"}, rows)
}

func formatUsedPorts(ports []containerServiceInterfaces.UsedPort) string {
	if len(ports) == 0 {
		return "No ports in use."
	}
	rows := make([][]string, 0, len(ports))
	for _, port := range ports {
		rows = append(rows, []string{port.Protocol, strconv.Itoa(int(port.Port)), cmp.Or(port.Address, "*"), port.By})
	}
	return styledTable([]string{"Protocol", "Port", "Address", "Used By"}, rows)
}

func filterCatalog(apps []containerServiceInterfaces.CatalogApp, args []string) []containerServiceInterfaces.CatalogApp {
	search, category := "", ""
	if len(args) > 0 {
		search = strings.ToLower(args[0])
	}
	if len(args) > 1 {
		category = args[1]
	}
	return slices.DeleteFunc(apps, func(app containerServiceInterfaces.CatalogApp) bool {
		text := strings.ToLower(strings.Join([]string{app.ID, app.Name, app.Category, app.Description}, " "))
		return !strings.Contains(text, search) || category != "" && !strings.EqualFold(app.Category, category)
	})
}

func formatCatalog(apps []containerServiceInterfaces.CatalogApp) string {
	if len(apps) == 0 {
		return "No apps found."
	}
	rows := make([][]string, 0, len(apps))
	for _, app := range apps {
		rows = append(rows, []string{app.Source + "/" + app.ID, app.Name, app.Category, app.OS, app.Version})
	}
	return styledTable([]string{"App", "Name", "Category", "OS", "Version"}, rows)
}

func formatCatalogTemplate(template containerServiceInterfaces.CatalogTemplate) string {
	app := template.App
	var out strings.Builder
	fmt.Fprintf(&out, "%s (%s/%s, %s)\n%s\n", app.Name, app.Source, app.ID, app.OS, app.Description)
	if len(app.Variants) > 1 {
		variants := make([]string, 0, len(app.Variants))
		for _, variant := range app.Variants {
			label := fmt.Sprintf("%s (%s)", variant.ID, variant.Label)
			if variant.Default {
				label += " [default]"
			}
			variants = append(variants, label)
		}
		fmt.Fprintf(&out, "\nVariants (--variant): %s\n", strings.Join(variants, ", "))
	}
	if len(template.Variables) > 0 {
		rows := make([][]string, 0, len(template.Variables))
		for _, variable := range template.Variables {
			value, notes := variable.Default, []string{}
			if variable.Hint != "" {
				notes = append(notes, strings.TrimSuffix(variable.Hint, "."))
			}
			if variable.Type == "secret" {
				value = "(generated if empty)"
			}
			switch {
			case variable.Target != "" && variable.Optional:
				notes = append(notes, "mounted at "+variable.Target+", not mounted if empty")
			case variable.Target != "":
				notes = append(notes, "mounted at "+variable.Target+", volume "+variable.Volume+" if empty")
			case variable.Protocol != "":
				notes = append(notes, variable.Protocol+" port")
			case !variable.Optional && variable.Default == "":
				notes = append(notes, "required")
			}
			if variable.Advanced {
				notes = append(notes, "advanced")
			}
			rows = append(rows, []string{variable.Name, variable.Label, value, strings.Join(notes, "; ")})
		}
		fmt.Fprintf(&out, "\nSettings (--set NAME=VALUE):\n%s\n", styledTable([]string{"Name", "Label", "Default", "Notes"}, rows))
	}
	if len(template.Containers) > 1 {
		fmt.Fprintf(&out, "\nContainers: %s\n", strings.Join(template.Containers, ", "))
	}
	for _, choice := range template.Choices {
		options := make([]string, 0, len(choice.Options))
		for _, option := range choice.Options {
			label := option.ID
			if option.ID == choice.Default {
				label += " [default]"
			}
			if len(option.Adds) > 0 {
				label += " (adds " + strings.Join(option.Adds, ", ") + ")"
			}
			if len(option.Drops) > 0 {
				label += " (without " + strings.Join(option.Drops, ", ") + ")"
			}
			for _, variable := range option.Ask {
				label += fmt.Sprintf(" [--set %s]", variable.Name)
			}
			options = append(options, label)
		}
		fmt.Fprintf(&out, "\nChoice %s (--choice %s=OPTION): %s\n", choice.Label, choice.ID, strings.Join(options, ", "))
	}
	fmt.Fprintf(&out, "\nCreate it: sylve containers create --app %s/%s --ocid ID --pool POOL [--set NAME=VALUE ...]", app.Source, app.ID)
	return out.String()
}
