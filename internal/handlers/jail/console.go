// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type TerminalSession struct {
	ID        string
	Cmd       *exec.Cmd
	Pty       *os.File
	Observers map[*websocket.Conn]bool
	Mu        sync.Mutex
	Output    chan []byte

	History      []byte
	HistoryLimit int
}

type SessionManager struct {
	sessions map[string]*TerminalSession
	mu       sync.RWMutex
}

var (
	GlobalSessionManager = &SessionManager{
		sessions: make(map[string]*TerminalSession),
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

func (sm *SessionManager) GetOrCreateSession(sessionID string, ctidInt int) (*TerminalSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, exists := sm.sessions[sessionID]; exists {
		if session.Cmd.ProcessState != nil && session.Cmd.ProcessState.Exited() {
			delete(sm.sessions, sessionID)
		} else {
			return session, nil
		}
	}

	ctidHash := utils.HashIntToNLetters(ctidInt, 5)

	cmd := exec.Command("jexec", "-l", ctidHash, "su", "-l", "root")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptymx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	session := &TerminalSession{
		ID:           sessionID,
		Cmd:          cmd,
		Pty:          ptymx,
		Observers:    make(map[*websocket.Conn]bool),
		Output:       make(chan []byte, 10),
		History:      make([]byte, 0, 16384),
		HistoryLimit: 16384,
	}

	go session.PumpOutput(sm)

	sm.sessions[sessionID] = session
	return session, nil
}

func (ts *TerminalSession) PumpOutput(sm *SessionManager) {
	defer func() {
		ts.Pty.Close()
		ts.Cmd.Wait()

		sm.mu.Lock()
		delete(sm.sessions, ts.ID)
		sm.mu.Unlock()
	}()

	buf := make([]byte, 4096)
	for {
		n, err := ts.Pty.Read(buf)
		if err != nil {
			if err != io.EOF {
				logger.L.Error().Err(err).Str("session", ts.ID).Msg("Error reading from PTY")
			}
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		ts.Mu.Lock()

		ts.History = append(ts.History, data...)
		if len(ts.History) > ts.HistoryLimit {
			ts.History = ts.History[len(ts.History)-ts.HistoryLimit:]
		}

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

func (sm *SessionManager) KillSession(sessionID string) {
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

func HandleJailTerminalWebsocket(jailService *jail.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctid := c.Query("ctid")
		if ctid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ctid is required"})
			return
		}

		ctidInt, err := strconv.Atoi(ctid)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid ctid"})
			return
		}

		j, err := jailService.GetJailByCTID(uint(ctidInt))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_get_jail"})
			return
		}
		if j == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "jail_not_found"})
			return
		}

		conn, err := WSUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}

		sessionID := "jail-" + ctid
		session, err := GlobalSessionManager.GetOrCreateSession(sessionID, ctidInt)
		if err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte("Error starting session: "+err.Error()))
			conn.Close()
			return
		}

		session.Mu.Lock()
		session.Observers[conn] = true

		if len(session.History) > 0 {
			if err := conn.WriteMessage(websocket.BinaryMessage, session.History); err != nil {
				delete(session.Observers, conn)
				session.Mu.Unlock()
				conn.Close()
				return
			}
		}

		session.Mu.Unlock()

		defer func() {
			session.Mu.Lock()
			delete(session.Observers, conn)
			session.Mu.Unlock()
			conn.Close()
		}()

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
			case 0:
				_, err := io.Copy(session.Pty, reader)
				if err != nil {
					return
				}

			case 1:
				var ws WindowSize
				if err := json.NewDecoder(reader).Decode(&ws); err != nil {
					continue
				}
				if err := pty.Setsize(session.Pty, &pty.Winsize{
					Rows: ws.Rows,
					Cols: ws.Cols,
					X:    ws.X,
					Y:    ws.Y,
				}); err != nil {
					logger.L.Warn().Err(err).Msg("Failed to resize PTY")
				}

			case 2:
				var killMsg struct {
					Kill string `json:"kill"`
				}
				_ = json.NewDecoder(reader).Decode(&killMsg)
				if killMsg.Kill == sessionID || killMsg.Kill == "" {
					GlobalSessionManager.KillSession(sessionID)
					return
				}
			}
		}
	}
}
