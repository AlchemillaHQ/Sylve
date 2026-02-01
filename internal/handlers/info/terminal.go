package infoHandlers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func HandleHostTerminal(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Spawn a unique shell process for THIS connection only
	cmd := exec.Command("login", "-f", "root")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	f, err := pty.Start(cmd)
	if err != nil {
		return
	}

	// Ensure the process is killed when the websocket closes
	defer func() {
		f.Close()
		cmd.Process.Kill()
		cmd.Process.Wait()
	}()

	// PTY -> WebSocket
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				return
			}
		}
	}()

	// WebSocket -> PTY
	for {
		mt, r, err := conn.NextReader()
		if err != nil {
			return
		}
		if mt != websocket.BinaryMessage {
			continue
		}

		head := make([]byte, 1)
		if _, err := r.Read(head); err != nil {
			return
		}

		switch head[0] {
		case 0: // Terminal Input
			io.Copy(f, r)
		case 1: // Resize
			var sz struct {
				Rows uint16 `json:"rows"`
				Cols uint16 `json:"cols"`
			}
			if err := json.NewDecoder(r).Decode(&sz); err == nil {
				pty.Setsize(f, &pty.Winsize{Rows: sz.Rows, Cols: sz.Cols})
			}
		}
	}
}
