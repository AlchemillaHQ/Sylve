package repl

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
)

type deleteResult struct {
	Deleted bool `json:"deleted"`
	ID      uint `json:"id"`
}

func handleNotes(ctx *Context, args []string) {
	jsonMode := hasJSONFlag(args)
	cleanArgs := dropJSONFlag(args)

	if len(cleanArgs) == 0 {
		printSubHelp(ctx, "notes", []cmdHelp{
			{"list", "List all notes"},
			{"add <title> <content>", `Add a note; quote multi-word values: notes add "Release notes" "Text with spaces"`},
			{"get <id>", "Get a note by ID"},
			{"delete <id>", "Delete a note by ID"},
		})
		return
	}

	subCmd := cleanArgs[0]
	subArgs := cleanArgs[1:]

	switch subCmd {
	case "list":
		if len(subArgs) != 0 {
			println(ctx, styledErrorf("Usage: notes list"))
			return
		}
		notesList(ctx, jsonMode)

	case "add":
		if len(subArgs) != 2 {
			println(ctx, styledErrorf("Usage: notes add <title> <content>"))
			return
		}
		title := subArgs[0]
		content := subArgs[1]
		notesAdd(ctx, title, content, jsonMode)

	case "get":
		if len(subArgs) != 1 {
			println(ctx, styledErrorf("Usage: notes get <id>"))
			return
		}
		id, err := parsePositiveUint(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid ID '%s'", subArgs[0]))
			return
		}
		notesGet(ctx, id, jsonMode)

	case "delete":
		if len(subArgs) != 1 {
			println(ctx, styledErrorf("Usage: notes delete <id>"))
			return
		}
		id, err := parsePositiveUint(subArgs[0])
		if err != nil {
			println(ctx, styledErrorf("Invalid ID '%s'", subArgs[0]))
			return
		}
		notesDelete(ctx, id, jsonMode)

	default:
		println(ctx, styledErrorf("Unknown notes command: '%s'. Type 'notes' for help.", subCmd))
	}
}

func hasJSONFlag(args []string) bool {
	for _, a := range args {
		if a == "--json" {
			return true
		}
	}
	return false
}

func dropJSONFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != "--json" {
			out = append(out, a)
		}
	}
	return out
}

func notesList(ctx *Context, jsonMode bool) {
	notes, err := listNotes(ctx)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching notes", err)
		return
	}

	if jsonMode {
		if notes == nil {
			notes = []infoModels.Note{}
		}
		println(ctx, mustJSON(notes))
		return
	}

	println(ctx, formatNotes(notes))
}

func listNotes(ctx *Context) ([]infoModels.Note, error) {
	if ctx == nil || ctx.Info == nil {
		return nil, fmt.Errorf("info_service_unavailable")
	}

	notes, err := ctx.Info.GetNotes()
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_notes: %w", err)
	}
	return notes, nil
}

func formatNotes(notes []infoModels.Note) string {
	if len(notes) == 0 {
		return "No notes found."
	}

	headers := []string{"ID", "Title", "Created"}
	rows := make([][]string, 0, len(notes))
	for _, note := range notes {
		rows = append(rows, []string{
			strconv.FormatUint(uint64(note.ID), 10),
			note.Title,
			note.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return styledTable(headers, rows)
}

func formatNote(note infoModels.Note) string {
	return strings.Join([]string{
		styledKeyValue("ID:", strconv.FormatUint(uint64(note.ID), 10)),
		styledKeyValue("Title:", note.Title),
		styledKeyValue("Content:", note.Content),
		styledKeyValue("Created:", note.CreatedAt.Format("2006-01-02 15:04")),
		styledKeyValue("Updated:", note.UpdatedAt.Format("2006-01-02 15:04")),
	}, "\n")
}

func notesAdd(ctx *Context, title, content string, jsonMode bool) {
	note, err := addNote(ctx, title, content)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error adding note", err)
		return
	}

	if jsonMode {
		println(ctx, mustJSON(note))
	} else {
		println(ctx, styledSuccessf("Note added: %d - %s", note.ID, note.Title))
	}
}

func addNote(ctx *Context, title, content string) (infoModels.Note, error) {
	if ctx == nil || ctx.Info == nil {
		return infoModels.Note{}, fmt.Errorf("info_service_unavailable")
	}

	note, err := ctx.Info.AddNote(title, content)
	if err != nil {
		return infoModels.Note{}, fmt.Errorf("failed_to_add_note: %w", err)
	}
	return note, nil
}

func notesGet(ctx *Context, id uint, jsonMode bool) {
	note, err := getNote(ctx, id)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching note", err)
		return
	}

	if jsonMode {
		println(ctx, mustJSON(note))
		return
	}
	println(ctx, formatNote(note))
}

func getNote(ctx *Context, id uint) (infoModels.Note, error) {
	if ctx == nil || ctx.Info == nil {
		return infoModels.Note{}, fmt.Errorf("info_service_unavailable")
	}

	note, err := ctx.Info.GetNoteByID(int(id))
	if err != nil {
		return infoModels.Note{}, fmt.Errorf("failed_to_get_note: %w", err)
	}
	return note, nil
}

func notesDelete(ctx *Context, id uint, jsonMode bool) {
	result, err := deleteNote(ctx, id)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error deleting note", err)
		return
	}

	if jsonMode {
		println(ctx, mustJSON(result))
	} else {
		println(ctx, styledSuccessf("Note %d deleted successfully.", id))
	}
}

func deleteNote(ctx *Context, id uint) (deleteResult, error) {
	if ctx == nil || ctx.Info == nil {
		return deleteResult{}, fmt.Errorf("info_service_unavailable")
	}
	if err := ctx.Info.DeleteNoteByID(int(id)); err != nil {
		return deleteResult{}, fmt.Errorf("failed_to_delete_note: %w", err)
	}
	return deleteResult{Deleted: true, ID: id}, nil
}

func processNoteAddSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.NoteAddPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_note_add_request: " + err.Error()}
	}

	note, err := addNote(ctx, request.Title, request.Content)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, note, styledSuccessf("Note added: %d - %s", note.ID, note.Title))
}

func processNoteDeleteSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.NoteDeletePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_note_delete_request: " + err.Error()}
	}
	if request.ID == 0 {
		return socketResponse{Error: "invalid_note_delete_request: id_required"}
	}

	result, err := deleteNote(ctx, request.ID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("Note %d deleted successfully.", request.ID))
}

func processNoteListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.NoteListPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_note_list_request: " + err.Error()}
	}

	notes, err := listNotes(ctx)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if notes == nil {
		notes = []infoModels.Note{}
	}
	if request.JSON {
		return operationSuccess(true, notes, "")
	}
	return operationSuccess(false, notes, formatNotes(notes))
}

func processNoteGetSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.NoteGetPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_note_get_request: " + err.Error()}
	}
	if request.ID == 0 {
		return socketResponse{Error: "invalid_note_get_request: id_required"}
	}

	note, err := getNote(ctx, request.ID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if request.JSON {
		return operationSuccess(true, note, "")
	}

	return operationSuccess(false, note, formatNote(note))
}
