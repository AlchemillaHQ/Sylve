package repl

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
)

func handleTasks(ctx *Context, args []string) {
	jsonMode := hasJSONFlag(args)
	cleanArgs := dropJSONFlag(args)

	if len(cleanArgs) == 0 {
		printSubHelp(ctx, "tasks", []cmdHelp{
			{"active [--guest-type <type>] [--guest-id <id>]", "List queued and running lifecycle tasks"},
			{"recent [--guest-type <type>] [--guest-id <id>] [--limit <n>]", "List recent lifecycle tasks"},
			{"get <task_id>", "Get a lifecycle task by ID"},
		})
		return
	}

	subCmd := cleanArgs[0]
	subArgs := cleanArgs[1:]

	switch subCmd {
	case "active":
		guestType, guestID, _, err := parseTaskFilters(subArgs, false)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		tasks, err := listActiveLifecycleTasks(ctx, guestType, guestID)
		if err != nil {
			printOperationError(ctx, jsonMode, "Error fetching active lifecycle tasks", err)
			return
		}
		printLifecycleTasks(ctx, jsonMode, tasks)

	case "recent":
		guestType, guestID, limit, err := parseTaskFilters(subArgs, true)
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		tasks, err := listRecentLifecycleTasks(ctx, guestType, guestID, limit)
		if err != nil {
			printOperationError(ctx, jsonMode, "Error fetching recent lifecycle tasks", err)
			return
		}
		printLifecycleTasks(ctx, jsonMode, tasks)

	case "get":
		if len(subArgs) != 1 {
			println(ctx, styledErrorf("Usage: tasks get <task_id>"))
			return
		}
		taskID, err := parsePositiveUint(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid task ID '%s'", subArgs[0]))
			return
		}
		task, err := getLifecycleTask(ctx, taskID)
		if err != nil {
			printOperationError(ctx, jsonMode, "Error fetching lifecycle task", err)
			return
		}
		if jsonMode {
			println(ctx, mustJSON(task))
			return
		}
		println(ctx, formatLifecycleTaskDetails(task))

	default:
		println(ctx, styledErrorf("Unknown tasks command '%s'", subCmd))
	}
}

func parseTaskFilters(args []string, allowLimit bool) (string, uint, int, error) {
	usage := "Usage: tasks active [--guest-type <type>] [--guest-id <id>]"
	if allowLimit {
		usage = "Usage: tasks recent [--guest-type <type>] [--guest-id <id>] [--limit <n>]"
	}

	var guestType string
	var guestID uint
	var limit int
	seen := map[string]bool{}

	for index := 0; index < len(args); index++ {
		flag := args[index]
		if seen[flag] {
			return "", 0, 0, fmt.Errorf("%s", usage)
		}
		seen[flag] = true

		if index+1 >= len(args) {
			return "", 0, 0, fmt.Errorf("%s", usage)
		}
		value := args[index+1]
		index++

		switch flag {
		case "--guest-type":
			guestType = strings.TrimSpace(value)
			if guestType == "" {
				return "", 0, 0, fmt.Errorf("%s", usage)
			}
		case "--guest-id":
			parsed, err := parsePositiveUint(value)
			if err != nil {
				return "", 0, 0, fmt.Errorf("%s", usage)
			}
			guestID = parsed
		case "--limit":
			if !allowLimit {
				return "", 0, 0, fmt.Errorf("%s", usage)
			}
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 200 {
				return "", 0, 0, fmt.Errorf("%s", usage)
			}
			limit = parsed
		default:
			return "", 0, 0, fmt.Errorf("%s", usage)
		}
	}

	return guestType, guestID, limit, nil
}

func listActiveLifecycleTasks(ctx *Context, guestType string, guestID uint) ([]taskModels.GuestLifecycleTask, error) {
	if ctx == nil || ctx.Lifecycle == nil {
		return nil, fmt.Errorf("lifecycle_service_unavailable")
	}

	tasks, err := ctx.Lifecycle.ListActiveTasks(guestType, guestID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_active_lifecycle_tasks: %w", err)
	}
	return tasks, nil
}

func listRecentLifecycleTasks(ctx *Context, guestType string, guestID uint, limit int) ([]taskModels.GuestLifecycleTask, error) {
	if ctx == nil || ctx.Lifecycle == nil {
		return nil, fmt.Errorf("lifecycle_service_unavailable")
	}

	tasks, err := ctx.Lifecycle.ListRecentTasks(guestType, guestID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_recent_lifecycle_tasks: %w", err)
	}
	return tasks, nil
}

func getLifecycleTask(ctx *Context, taskID uint) (*taskModels.GuestLifecycleTask, error) {
	if taskID == 0 {
		return nil, fmt.Errorf("invalid_task_id")
	}
	if ctx == nil || ctx.Lifecycle == nil {
		return nil, fmt.Errorf("lifecycle_service_unavailable")
	}

	task, err := ctx.Lifecycle.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_lifecycle_task: %w", err)
	}
	if task == nil {
		return nil, fmt.Errorf("task_not_found")
	}
	return task, nil
}

func formatLifecycleTasks(tasks []taskModels.GuestLifecycleTask) string {
	if len(tasks) == 0 {
		return "No lifecycle tasks found."
	}

	headers := []string{"TASK ID", "GUEST", "GUEST ID", "ACTION", "STATUS", "CREATED"}
	rows := make([][]string, 0, len(tasks))
	for _, task := range tasks {
		rows = append(rows, []string{
			strconv.FormatUint(uint64(task.ID), 10),
			task.GuestType,
			strconv.FormatUint(uint64(task.GuestID), 10),
			task.Action,
			task.Status,
			formatLifecycleTaskTime(task.CreatedAt),
		})
	}
	return styledTable(headers, rows)
}

func formatLifecycleTaskDetails(task *taskModels.GuestLifecycleTask) string {
	lines := []string{
		styledKeyValue("Task ID:", strconv.FormatUint(uint64(task.ID), 10)),
		styledKeyValue("Guest type:", task.GuestType),
		styledKeyValue("Guest ID:", strconv.FormatUint(uint64(task.GuestID), 10)),
		styledKeyValue("Action:", task.Action),
		styledKeyValue("Source:", task.Source),
		styledKeyValue("Status:", task.Status),
		styledKeyValue("Requested by:", task.RequestedBy),
		styledKeyValue("Created:", formatLifecycleTaskTime(task.CreatedAt)),
	}
	if task.StartedAt != nil {
		lines = append(lines, styledKeyValue("Started:", formatLifecycleTaskTime(*task.StartedAt)))
	}
	if task.FinishedAt != nil {
		lines = append(lines, styledKeyValue("Finished:", formatLifecycleTaskTime(*task.FinishedAt)))
	}
	if task.Message != "" {
		lines = append(lines, styledKeyValue("Message:", task.Message))
	}
	if task.Error != "" {
		lines = append(lines, styledKeyValue("Error:", task.Error))
	}
	return strings.Join(lines, "\n")
}

func formatLifecycleTaskTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func printLifecycleTasks(ctx *Context, jsonMode bool, tasks []taskModels.GuestLifecycleTask) {
	if tasks == nil {
		tasks = []taskModels.GuestLifecycleTask{}
	}
	if jsonMode {
		println(ctx, mustJSON(tasks))
		return
	}
	println(ctx, formatLifecycleTasks(tasks))
}

func processTaskActiveSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.TaskActivePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_task_active_request: " + err.Error()}
	}
	tasks, err := listActiveLifecycleTasks(ctx, request.GuestType, request.GuestID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if tasks == nil {
		tasks = []taskModels.GuestLifecycleTask{}
	}
	return operationSuccess(request.JSON, tasks, formatLifecycleTasks(tasks))
}

func processTaskRecentSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.TaskRecentPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_task_recent_request: " + err.Error()}
	}
	if request.Limit < 0 || request.Limit > 200 {
		return socketResponse{Error: "invalid_task_recent_request: invalid_limit"}
	}
	tasks, err := listRecentLifecycleTasks(ctx, request.GuestType, request.GuestID, request.Limit)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if tasks == nil {
		tasks = []taskModels.GuestLifecycleTask{}
	}
	return operationSuccess(request.JSON, tasks, formatLifecycleTasks(tasks))
}

func processTaskGetSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.TaskGetPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_task_get_request: " + err.Error()}
	}
	task, err := getLifecycleTask(ctx, request.TaskID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, task, formatLifecycleTaskDetails(task))
}
