package repl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func newTasksTestContext(t *testing.T) (*Context, *gorm.DB) {
	t.Helper()
	dbConn := testutil.NewSQLiteTestDB(t, &taskModels.GuestLifecycleTask{})
	return &Context{Lifecycle: lifecycle.NewService(dbConn, nil, nil, nil)}, dbConn
}

func TestTaskSocketOperations(t *testing.T) {
	ctx, dbConn := newTasksTestContext(t)
	active := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   101,
		Action:    "start",
		Status:    taskModels.LifecycleTaskStatusQueued,
	}
	completed := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   102,
		Action:    "stop",
		Status:    taskModels.LifecycleTaskStatusSuccess,
	}
	for _, task := range []*taskModels.GuestLifecycleTask{&active, &completed} {
		if err := dbConn.Create(task).Error; err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}

	activePayload, err := json.Marshal(consoleprotocol.TaskActivePayload{GuestType: "vm", JSON: true})
	if err != nil {
		t.Fatalf("marshal active payload: %v", err)
	}
	activeResponse := processSocketRequest(ctx, socketRequest{
		Operation: consoleprotocol.OperationTaskListActive,
		Payload:   activePayload,
	})
	if activeResponse.Error != "" {
		t.Fatalf("active response error: %s", activeResponse.Error)
	}
	var activeTasks []taskModels.GuestLifecycleTask
	if err := json.Unmarshal([]byte(activeResponse.Output), &activeTasks); err != nil {
		t.Fatalf("decode active output: %v", err)
	}
	if len(activeTasks) != 1 || activeTasks[0].ID != active.ID {
		t.Fatalf("active tasks = %#v", activeTasks)
	}

	getPayload, err := json.Marshal(consoleprotocol.TaskGetPayload{TaskID: completed.ID, JSON: true})
	if err != nil {
		t.Fatalf("marshal get payload: %v", err)
	}
	getResponse := processSocketRequest(ctx, socketRequest{
		Operation: consoleprotocol.OperationTaskGet,
		Payload:   getPayload,
	})
	if getResponse.Error != "" {
		t.Fatalf("get response error: %s", getResponse.Error)
	}
	var got taskModels.GuestLifecycleTask
	if err := json.Unmarshal([]byte(getResponse.Output), &got); err != nil {
		t.Fatalf("decode get output: %v", err)
	}
	if got.ID != completed.ID || got.Status != taskModels.LifecycleTaskStatusSuccess {
		t.Fatalf("task = %#v", got)
	}
}

func TestExecuteLineTasksGetJSON(t *testing.T) {
	ctx, dbConn := newTasksTestContext(t)
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeJail,
		GuestID:   201,
		Action:    "restart",
		Status:    taskModels.LifecycleTaskStatusRunning,
	}
	if err := dbConn.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	var output bytes.Buffer
	ctx.Out = &output
	if !ExecuteLine(ctx, fmt.Sprintf("tasks get %d --json", task.ID)) {
		t.Fatal("expected task command to keep session running")
	}

	var got taskModels.GuestLifecycleTask
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode task output: %v", err)
	}
	if got.ID != task.ID || got.Status != taskModels.LifecycleTaskStatusRunning {
		t.Fatalf("task = %#v", got)
	}
}

func TestParseTaskFilters(t *testing.T) {
	guestType, guestID, limit, err := parseTaskFilters([]string{
		"--guest-type", "vm", "--guest-id", "101", "--limit", "25",
	}, true)
	if err != nil {
		t.Fatalf("parse filters: %v", err)
	}
	if guestType != "vm" || guestID != 101 || limit != 25 {
		t.Fatalf("filters = %q, %d, %d", guestType, guestID, limit)
	}

	if _, _, _, err := parseTaskFilters([]string{"--limit", "5"}, false); err == nil {
		t.Fatal("expected active task limit to be rejected")
	}
}
