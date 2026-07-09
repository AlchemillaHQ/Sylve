package repl

import (
	"encoding/json"
	"strconv"
	"strings"

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
			{"add <title> <content>", "Add a new note"},
			{"get <id>", "Get a note by ID"},
			{"delete <id>", "Delete a note by ID"},
		})
		return
	}

	subCmd := cleanArgs[0]
	subArgs := cleanArgs[1:]

	switch subCmd {
	case "list":
		notesList(ctx, jsonMode)

	case "add":
		if len(subArgs) < 2 {
			println(ctx, styledErrorf("Usage: notes add <title> <content>"))
			return
		}
		title := subArgs[0]
		content := strings.Join(subArgs[1:], " ")
		notesAdd(ctx, title, content, jsonMode)

	case "get":
		if len(subArgs) < 1 {
			println(ctx, styledErrorf("Usage: notes get <id>"))
			return
		}
		id, err := strconv.ParseUint(subArgs[0], 10, 64)
		if err != nil {
			println(ctx, styledErrorf("Invalid ID '%s'", subArgs[0]))
			return
		}
		notesGet(ctx, uint(id), jsonMode)

	case "delete":
		if len(subArgs) < 1 {
			println(ctx, styledErrorf("Usage: notes delete <id>"))
			return
		}
		id, err := strconv.ParseUint(subArgs[0], 10, 64)
		if err != nil {
			println(ctx, styledErrorf("Invalid ID '%s'", subArgs[0]))
			return
		}
		notesDelete(ctx, uint(id), jsonMode)

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
	notes, err := ctx.Info.GetNotes()
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error fetching notes: %v", err))
		}
		return
	}

	if jsonMode {
		if notes == nil {
			notes = []infoModels.Note{}
		}
		println(ctx, mustJSON(notes))
		return
	}

	if len(notes) == 0 {
		println(ctx, "No notes found.")
		return
	}

	headers := []string{"ID", "Title", "Created"}
	var rows [][]string
	for _, n := range notes {
		rows = append(rows, []string{
			strconv.FormatUint(uint64(n.ID), 10),
			n.Title,
			n.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	println(ctx, styledTable(headers, rows))
}

func notesAdd(ctx *Context, title, content string, jsonMode bool) {
	note, err := ctx.Info.AddNote(title, content)
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error adding note: %v", err))
		}
		return
	}

	if jsonMode {
		println(ctx, mustJSON(note))
	} else {
		println(ctx, styledSuccessf("Note added: %d - %s", note.ID, note.Title))
	}
}

func notesGet(ctx *Context, id uint, jsonMode bool) {
	note, err := ctx.Info.GetNoteByID(int(id))
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error fetching note: %v", err))
		}
		return
	}

	if jsonMode {
		println(ctx, mustJSON(note))
	} else {
		println(ctx, styledKeyValue("ID:", strconv.FormatUint(uint64(note.ID), 10)))
		println(ctx, styledKeyValue("Title:", note.Title))
		println(ctx, styledKeyValue("Content:", note.Content))
		println(ctx, styledKeyValue("Created:", note.CreatedAt.Format("2006-01-02 15:04")))
		println(ctx, styledKeyValue("Updated:", note.UpdatedAt.Format("2006-01-02 15:04")))
	}
}

func notesDelete(ctx *Context, id uint, jsonMode bool) {
	err := ctx.Info.DeleteNoteByID(int(id))
	if err != nil {
		if jsonMode {
			println(ctx, mustJSON(struct{ Error string `json:"error"` }{Error: err.Error()}))
		} else {
			println(ctx, styledErrorf("Error deleting note: %v", err))
		}
		return
	}

	if jsonMode {
		println(ctx, mustJSON(deleteResult{Deleted: true, ID: id}))
	} else {
		println(ctx, styledSuccessf("Note %d deleted successfully.", id))
	}
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return `{"error":"json marshal failed"}`
	}
	return string(b)
}
