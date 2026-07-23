package repl

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
)

type objectCreateResult struct {
	Created bool   `json:"created"`
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
}

type objectEditResult struct {
	Updated bool   `json:"updated"`
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
}

type objectDeleteResult struct {
	Deleted bool `json:"deleted"`
	ID      uint `json:"id"`
}

func handleObjects(ctx *Context, args []string) {
	jsonMode := hasJSONFlag(args)
	cleanArgs := dropJSONFlag(args)

	if len(cleanArgs) == 0 {
		printSubHelp(ctx, "objects", []cmdHelp{
			{"list [type]", "List objects; filter by host, network, port, country, list, mac, fqdn, or duid"},
			{"create <name> <type> <value> [value...]", "Create an object"},
			{"edit <id> [--name <name>] [--type <type>] [--value <value> ...]", "Patch an object; values replace the current set"},
			{"delete <id>", "Delete an unused object"},
		})
		return
	}

	switch cleanArgs[0] {
	case "list":
		if len(cleanArgs) > 2 {
			println(ctx, styledErrorf("Usage: objects list [type]"))
			return
		}
		objectType := ""
		if len(cleanArgs) == 2 {
			objectType = cleanArgs[1]
		}
		objectsList(ctx, objectType, jsonMode)

	case "create":
		request, err := buildConsoleObjectRequest(cleanArgs[1:])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		objectsCreate(ctx, request, jsonMode)

	case "edit":
		id, request, err := buildConsoleObjectEditRequest(cleanArgs[1:])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		objectsEdit(ctx, id, request, jsonMode)

	case "delete":
		if len(cleanArgs) != 2 {
			println(ctx, styledErrorf("Usage: objects delete <id>"))
			return
		}
		id, err := parsePositiveUint(cleanArgs[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid object ID '%s'", cleanArgs[1]))
			return
		}
		objectsDelete(ctx, id, jsonMode)

	default:
		println(ctx, styledErrorf("Unknown objects command: '%s'. Type 'objects' for help.", cleanArgs[0]))
	}
}

func buildConsoleObjectRequest(args []string) (consoleprotocol.NetworkObjectRequest, error) {
	const usage = "Usage: objects create <name> <type> <value> [value...]"
	if len(args) < 3 {
		return consoleprotocol.NetworkObjectRequest{}, fmt.Errorf("%s", usage)
	}
	return normalizeNetworkObjectRequest(consoleprotocol.NetworkObjectRequest{
		Name:   args[0],
		Type:   args[1],
		Values: args[2:],
	})
}

func buildConsoleObjectEditRequest(args []string) (uint, consoleprotocol.NetworkObjectEditRequest, error) {
	const usage = "Usage: objects edit <id> [--name <name>] [--type <type>] [--value <value> ...]"
	if len(args) < 1 {
		return 0, consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("%s", usage)
	}
	id, err := parsePositiveUint(args[0])
	if err != nil {
		return 0, consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("Invalid object ID '%s'", args[0])
	}

	request := consoleprotocol.NetworkObjectEditRequest{}
	var values []string
	valuesSet := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if request.Name != nil || i+1 >= len(args) {
				return 0, consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("%s", usage)
			}
			value := args[i+1]
			request.Name = &value
			i++
		case "--type":
			if request.Type != nil || i+1 >= len(args) {
				return 0, consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("%s", usage)
			}
			value := args[i+1]
			request.Type = &value
			i++
		case "--value":
			if i+1 >= len(args) {
				return 0, consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("%s", usage)
			}
			values = append(values, args[i+1])
			valuesSet = true
			i++
		default:
			return 0, consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("unknown object edit option %q", args[i])
		}
	}
	if valuesSet {
		request.Values = &values
	}
	if request.Name == nil && request.Type == nil && request.Values == nil {
		return 0, consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("specify --name, --type, or --value")
	}

	return id, request, nil
}

func normalizeNetworkObjectRequest(request consoleprotocol.NetworkObjectRequest) (consoleprotocol.NetworkObjectRequest, error) {
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		return consoleprotocol.NetworkObjectRequest{}, fmt.Errorf("object_name_required")
	}

	objectType, err := normalizeObjectType(request.Type)
	if err != nil {
		return consoleprotocol.NetworkObjectRequest{}, err
	}
	request.Type = objectType
	if len(request.Values) == 0 {
		return consoleprotocol.NetworkObjectRequest{}, fmt.Errorf("object_values_required")
	}
	request.Values = append([]string(nil), request.Values...)
	for i := range request.Values {
		request.Values[i] = strings.TrimSpace(request.Values[i])
		if request.Values[i] == "" {
			return consoleprotocol.NetworkObjectRequest{}, fmt.Errorf("object_value_required")
		}
	}

	return request, nil
}

func normalizeNetworkObjectEditRequest(request consoleprotocol.NetworkObjectEditRequest) (consoleprotocol.NetworkObjectEditRequest, error) {
	if request.Name == nil && request.Type == nil && request.Values == nil {
		return consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("object_edit_required")
	}
	if request.Name != nil {
		value := strings.TrimSpace(*request.Name)
		if value == "" {
			return consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("object_name_required")
		}
		request.Name = &value
	}
	if request.Type != nil {
		objectType, err := normalizeObjectType(*request.Type)
		if err != nil {
			return consoleprotocol.NetworkObjectEditRequest{}, err
		}
		request.Type = &objectType
	}
	if request.Values != nil {
		values := append([]string(nil), (*request.Values)...)
		if len(values) == 0 {
			return consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("object_values_required")
		}
		for i := range values {
			values[i] = strings.TrimSpace(values[i])
			if values[i] == "" {
				return consoleprotocol.NetworkObjectEditRequest{}, fmt.Errorf("object_value_required")
			}
		}
		request.Values = &values
	}

	return request, nil
}

func normalizeObjectType(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "host", "hosts":
		return "Host", nil
	case "network", "networks":
		return "Network", nil
	case "port", "ports":
		return "Port", nil
	case "country", "countries":
		return "Country", nil
	case "list", "lists":
		return "List", nil
	case "mac", "macs":
		return "Mac", nil
	case "fqdn", "fqdns":
		return "FQDN", nil
	case "duid", "duids":
		return "DUID", nil
	default:
		return "", fmt.Errorf("invalid_object_type")
	}
}

func listObjects(ctx *Context, objectType string) ([]networkModels.Object, error) {
	filterType := ""
	if strings.TrimSpace(objectType) != "" {
		var err error
		filterType, err = normalizeObjectType(objectType)
		if err != nil {
			return nil, err
		}
	}
	if ctx == nil || ctx.Network == nil {
		return nil, fmt.Errorf("network_service_unavailable")
	}

	objects, err := ctx.Network.GetObjects()
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_objects: %w", err)
	}
	if filterType == "" {
		return objects, nil
	}

	filtered := make([]networkModels.Object, 0, len(objects))
	for _, object := range objects {
		if object.Type == filterType {
			filtered = append(filtered, object)
		}
	}
	return filtered, nil
}

func createObject(ctx *Context, request consoleprotocol.NetworkObjectRequest) (objectCreateResult, error) {
	request, err := normalizeNetworkObjectRequest(request)
	if err != nil {
		return objectCreateResult{}, err
	}
	if ctx == nil || ctx.Network == nil {
		return objectCreateResult{}, fmt.Errorf("network_service_unavailable")
	}

	id, err := ctx.Network.CreateObject(request.Name, request.Type, request.Values)
	if err != nil {
		return objectCreateResult{}, fmt.Errorf("failed_to_create_object: %w", err)
	}
	return objectCreateResult{Created: true, ID: id, Name: request.Name, Type: request.Type}, nil
}

func editObject(ctx *Context, id uint, request consoleprotocol.NetworkObjectEditRequest) (objectEditResult, error) {
	if id == 0 {
		return objectEditResult{}, fmt.Errorf("invalid_object_id")
	}
	request, err := normalizeNetworkObjectEditRequest(request)
	if err != nil {
		return objectEditResult{}, err
	}
	if ctx == nil || ctx.Network == nil {
		return objectEditResult{}, fmt.Errorf("network_service_unavailable")
	}

	object, err := objectForEdit(ctx, id)
	if err != nil {
		return objectEditResult{}, err
	}
	fullRequest := networkObjectRequestFromModel(object)
	if request.Name != nil {
		fullRequest.Name = *request.Name
	}
	if request.Type != nil {
		fullRequest.Type = *request.Type
	}
	if request.Values != nil {
		fullRequest.Values = *request.Values
	}
	fullRequest, err = normalizeNetworkObjectRequest(fullRequest)
	if err != nil {
		return objectEditResult{}, err
	}

	if err := ctx.Network.EditObject(id, fullRequest.Name, fullRequest.Type, fullRequest.Values); err != nil {
		return objectEditResult{}, fmt.Errorf("failed_to_edit_object: %w", err)
	}
	return objectEditResult{Updated: true, ID: id, Name: fullRequest.Name, Type: fullRequest.Type}, nil
}

func objectForEdit(ctx *Context, id uint) (networkModels.Object, error) {
	objects, err := listObjects(ctx, "")
	if err != nil {
		return networkModels.Object{}, err
	}
	for _, object := range objects {
		if object.ID == id {
			return object, nil
		}
	}
	return networkModels.Object{}, fmt.Errorf("object_not_found")
}

func networkObjectRequestFromModel(object networkModels.Object) consoleprotocol.NetworkObjectRequest {
	values := make([]string, 0, len(object.Entries))
	for _, entry := range object.Entries {
		values = append(values, entry.Value)
	}
	return consoleprotocol.NetworkObjectRequest{Name: object.Name, Type: object.Type, Values: values}
}

func deleteObject(ctx *Context, id uint) (objectDeleteResult, error) {
	if id == 0 {
		return objectDeleteResult{}, fmt.Errorf("invalid_object_id")
	}
	if ctx == nil || ctx.Network == nil {
		return objectDeleteResult{}, fmt.Errorf("network_service_unavailable")
	}
	if err := ctx.Network.DeleteObject(id); err != nil {
		return objectDeleteResult{}, fmt.Errorf("failed_to_delete_object: %w", err)
	}
	return objectDeleteResult{Deleted: true, ID: id}, nil
}

func formatObjects(objects []networkModels.Object) string {
	if len(objects) == 0 {
		return "No objects found."
	}

	headers := []string{"ID", "Name", "Type", "Values", "Used"}
	rows := make([][]string, 0, len(objects))
	for _, object := range objects {
		values := make([]string, 0, len(object.Entries))
		for _, entry := range object.Entries {
			values = append(values, entry.Value)
		}
		used := "no"
		if object.IsUsed {
			used = "yes"
			if object.IsUsedBy != "" {
				used += " (" + object.IsUsedBy + ")"
			}
		}
		rows = append(rows, []string{
			strconv.FormatUint(uint64(object.ID), 10),
			object.Name,
			object.Type,
			strings.Join(values, ", "),
			used,
		})
	}
	return styledTable(headers, rows)
}

func objectsList(ctx *Context, objectType string, jsonMode bool) {
	objects, err := listObjects(ctx, objectType)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching objects", err)
		return
	}
	if objects == nil {
		objects = []networkModels.Object{}
	}
	if jsonMode {
		println(ctx, mustJSON(objects))
		return
	}
	println(ctx, formatObjects(objects))
}

func objectsCreate(ctx *Context, request consoleprotocol.NetworkObjectRequest, jsonMode bool) {
	result, err := createObject(ctx, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error creating object", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Object %d (%s) created successfully.", result.ID, result.Name))
}

func objectsEdit(ctx *Context, id uint, request consoleprotocol.NetworkObjectEditRequest, jsonMode bool) {
	result, err := editObject(ctx, id, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error updating object", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Object %d (%s) updated successfully.", result.ID, result.Name))
}

func objectsDelete(ctx *Context, id uint, jsonMode bool) {
	result, err := deleteObject(ctx, id)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error deleting object", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Object %d deleted successfully.", result.ID))
}

func processObjectListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.ObjectListPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_object_list_request: " + err.Error()}
	}
	objects, err := listObjects(ctx, request.Type)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if objects == nil {
		objects = []networkModels.Object{}
	}
	return operationSuccess(request.JSON, objects, formatObjects(objects))
}

func processObjectCreateSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.ObjectCreatePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_object_create_request: " + err.Error()}
	}
	result, err := createObject(ctx, request.Request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("Object %d (%s) created successfully.", result.ID, result.Name))
}

func processObjectEditSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.ObjectEditPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_object_edit_request: " + err.Error()}
	}
	result, err := editObject(ctx, request.ID, request.Request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("Object %d (%s) updated successfully.", result.ID, result.Name))
}

func processObjectDeleteSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.ObjectDeletePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_object_delete_request: " + err.Error()}
	}
	result, err := deleteObject(ctx, request.ID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("Object %d deleted successfully.", result.ID))
}
