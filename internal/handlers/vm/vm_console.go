// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type VMSession struct {
	ID           string
	Cmd          *exec.Cmd
	Pty          *os.File
	Observers    map[*websocket.Conn]bool
	Mu           sync.Mutex
	History      []byte
	HistoryLimit int
}

type VMSessionManager struct {
	sessions map[string]*VMSession
	mu       sync.RWMutex
}

var (
	GlobalVMSessionManager = &VMSessionManager{
		sessions: make(map[string]*VMSession),
	}
	WSUpgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

type WindowSize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
	X    uint16
	Y    uint16
}

func (sm *VMSessionManager) GetOrCreateSession(sessionID string, ridInt int, baudRate string) (*VMSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// 1. Check for existing session
	if session, exists := sm.sessions[sessionID]; exists {
		// If process is dead, clean up
		if session.Cmd.ProcessState != nil && session.Cmd.ProcessState.Exited() {
			delete(sm.sessions, sessionID)
		} else {
			return session, nil
		}
	}

	// 2. Spawn new 'cu' process
	// We use 'cu' because it handles serial line discipline nicely (locking, parity, etc)
	devPath := "/dev/nmdm" + strconv.Itoa(ridInt) + "B"

	// Command: cu -l /dev/nmdmXB -s 115200
	cmd := exec.Command("cu", "-l", devPath, "-s", baudRate)

	// TERM=xterm is usually safe for serial consoles expecting basic capabilities
	cmd.Env = append(os.Environ(), "TERM=xterm")

	ptymx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	session := &VMSession{
		ID:           sessionID,
		Cmd:          cmd,
		Pty:          ptymx,
		Observers:    make(map[*websocket.Conn]bool),
		History:      make([]byte, 0, 16384),
		HistoryLimit: 16384,
	}

	// 3. Start the background pump
	go session.PumpOutput(sm)

	sm.sessions[sessionID] = session
	return session, nil
}

func (ts *VMSession) PumpOutput(sm *VMSessionManager) {
	defer func() {
		ts.Pty.Close()
		// If 'cu' dies, we kill the process record
		if ts.Cmd.Process != nil {
			ts.Cmd.Process.Kill()
			ts.Cmd.Wait()
		}

		sm.mu.Lock()
		delete(sm.sessions, ts.ID)
		sm.mu.Unlock()
	}()

	buf := make([]byte, 4096)
	for {
		n, err := ts.Pty.Read(buf)
		if err != nil {
			// io.EOF means 'cu' exited (maybe user typed ~.)
			// or the PTY was closed.
			if err != io.EOF {
				logger.L.Error().Err(err).Str("session", ts.ID).Msg("Error reading from VM PTY")
			}
			return
		}

		// Copy data for broadcast/history
		data := make([]byte, n)
		copy(data, buf[:n])

		ts.Mu.Lock()

		// History Recording
		ts.History = append(ts.History, data...)
		if len(ts.History) > ts.HistoryLimit {
			ts.History = ts.History[len(ts.History)-ts.HistoryLimit:]
		}

		// Broadcast
		for conn := range ts.Observers {
			err := conn.WriteMessage(websocket.BinaryMessage, data)
			if err != nil {
				conn.Close()
				delete(ts.Observers, conn)
			}
		}
		ts.Mu.Unlock()
	}
}

func (sm *VMSessionManager) KillSession(sessionID string) {
	sm.mu.Lock()
	if session, exists := sm.sessions[sessionID]; exists {
		if session.Cmd.Process != nil {
			session.Cmd.Process.Kill()
		}
		session.Pty.Close()
		delete(sm.sessions, sessionID)
	}
	sm.mu.Unlock()
}

// --- Handler ---

func HandleLibvirtTerminalWebsocket(c *gin.Context) {
	rid := c.Query("rid")
	if rid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rid is required"})
		return
	}

	baudRate := c.DefaultQuery("baudrate", "115200")
	ridInt, err := strconv.Atoi(rid)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid rid"})
		return
	}

	conn, err := WSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.L.Error().Err(err).Msg("WebSocket upgrade failed")
		return
	}

	sessionID := "vm-console-" + rid

	// Get the singleton 'cu' process for this VM
	session, err := GlobalVMSessionManager.GetOrCreateSession(sessionID, ridInt, baudRate)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("Error connecting to console: "+err.Error()))
		conn.Close()
		return
	}

	// Register Observer
	session.Mu.Lock()
	session.Observers[conn] = true

	// REPLAY HISTORY (Critical for "Persistence" feel)
	if len(session.History) > 0 {
		conn.WriteMessage(websocket.BinaryMessage, session.History)
	}
	session.Mu.Unlock()

	defer func() {
		session.Mu.Lock()
		delete(session.Observers, conn)
		session.Mu.Unlock()
		conn.Close()
	}()

	// Input Loop
	for {
		messageType, reader, err := conn.NextReader()
		if err != nil {
			return
		}

		if messageType == websocket.TextMessage {
			continue
		}

		header := make([]byte, 1)
		if _, err := reader.Read(header); err != nil {
			return
		}

		switch header[0] {
		case 0: // stdin
			// Write to 'cu', which writes to serial port
			_, err := io.Copy(session.Pty, reader)
			if err != nil {
				return
			}

		case 1: // resize
			var ws WindowSize
			if err := json.NewDecoder(reader).Decode(&ws); err != nil {
				continue
			}
			// Resizing 'cu' PTY tells 'cu' the size, but serial ports are dumb.
			// However, if the VM is running a modern shell (bash/zsh),
			// it might not detect size over serial automatically.
			// But setting the PTY size here is still the "correct" thing to do.
			pty.Setsize(session.Pty, &pty.Winsize{
				Rows: ws.Rows,
				Cols: ws.Cols,
				X:    ws.X,
				Y:    ws.Y,
			})

		case 2: // kill
			var killMsg struct {
				Kill string `json:"kill"`
			}
			_ = json.NewDecoder(reader).Decode(&killMsg)
			if killMsg.Kill == sessionID || killMsg.Kill == "" {
				GlobalVMSessionManager.KillSession(sessionID)
				return
			}
		}
	}
}
