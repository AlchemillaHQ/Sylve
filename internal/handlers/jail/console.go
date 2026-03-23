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
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	wsReadLimit         = 10 << 20 // 10 MiB
	wsWriteTimeout      = 10 * time.Second
	wsPongWait          = 60 * time.Second
	wsPingPeriod        = (wsPongWait * 9) / 10
	defaultRows         = 24
	defaultCols         = 80
	controlInput   byte = 0
	controlResize  byte = 1
	controlKill    byte = 2
)

type Observer struct {
	Conn *websocket.Conn
	Mu   sync.Mutex
}

func (o *Observer) WriteMessage(messageType int, payload []byte) error {
	o.Mu.Lock()
	defer o.Mu.Unlock()

	_ = o.Conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return o.Conn.WriteMessage(messageType, payload)
}

func (o *Observer) WriteControl(messageType int, payload []byte, deadline time.Time) error {
	o.Mu.Lock()
	defer o.Mu.Unlock()

	_ = o.Conn.SetWriteDeadline(deadline)
	return o.Conn.WriteControl(messageType, payload, deadline)
}

func (o *Observer) Close() error {
	o.Mu.Lock()
	defer o.Mu.Unlock()
	return o.Conn.Close()
}

type TerminalSession struct {
	ID        string
	Cmd       *exec.Cmd
	Pty       *os.File
	Observers map[*Observer]struct{}

	Mu        sync.Mutex
	closeOnce sync.Once
	closed    bool
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
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
)

type WindowSize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
	X    uint16 `json:"x"`
	Y    uint16 `json:"y"`
}

func (sm *SessionManager) GetOrCreateSession(sessionID string, ctidInt int) (*TerminalSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, exists := sm.sessions[sessionID]; exists && !session.IsClosed() {
		return session, nil
	}

	ctidHash := utils.HashIntToNLetters(ctidInt, 5)

	cmd := exec.Command("jexec", "-l", ctidHash, "login", "-f", "root")
	cmd.Env = append(os.Environ(), "TERM=xterm")

	ptymx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	if err := pty.Setsize(ptymx, &pty.Winsize{
		Rows: defaultRows,
		Cols: defaultCols,
	}); err != nil {
		_ = ptymx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}

	session := &TerminalSession{
		ID:        sessionID,
		Cmd:       cmd,
		Pty:       ptymx,
		Observers: make(map[*Observer]struct{}),
	}

	sm.sessions[sessionID] = session
	go session.PumpOutput(sm)

	return session, nil
}

func (sm *SessionManager) removeSession(sessionID string, session *TerminalSession) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	current, exists := sm.sessions[sessionID]
	if !exists {
		return
	}
	if current == session {
		delete(sm.sessions, sessionID)
	}
}

func (ts *TerminalSession) IsClosed() bool {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()
	return ts.closed
}

func (ts *TerminalSession) Close(sm *SessionManager) {
	ts.closeOnce.Do(func() {
		ts.Mu.Lock()
		ts.closed = true

		observers := make([]*Observer, 0, len(ts.Observers))
		for obs := range ts.Observers {
			observers = append(observers, obs)
		}
		ts.Observers = make(map[*Observer]struct{})
		ts.Mu.Unlock()

		for _, obs := range observers {
			_ = obs.Close()
		}

		if ts.Pty != nil {
			_ = ts.Pty.Close()
		}
		if ts.Cmd != nil && ts.Cmd.Process != nil {
			_ = ts.Cmd.Process.Kill()
		}
		if ts.Cmd != nil {
			_ = ts.Cmd.Wait()
		}

		sm.removeSession(ts.ID, ts)
	})
}

func (ts *TerminalSession) AddObserver(obs *Observer) error {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()

	if ts.closed {
		return errors.New("session is closed")
	}

	ts.Observers[obs] = struct{}{}
	return nil
}

func (ts *TerminalSession) RemoveObserver(obs *Observer, sm *SessionManager) {
	ts.Mu.Lock()
	delete(ts.Observers, obs)
	remaining := len(ts.Observers)
	closed := ts.closed
	ts.Mu.Unlock()

	_ = obs.Close()

	if !closed && remaining == 0 {
		ts.Close(sm)
	}
}

func (ts *TerminalSession) BroadcastBinary(payload []byte) {
	ts.Mu.Lock()
	if ts.closed {
		ts.Mu.Unlock()
		return
	}

	observers := make([]*Observer, 0, len(ts.Observers))
	for obs := range ts.Observers {
		observers = append(observers, obs)
	}
	ts.Mu.Unlock()

	for _, obs := range observers {
		if err := obs.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			logger.L.Warn().Err(err).Str("session", ts.ID).Msg("Failed to write PTY output to websocket")
		}
	}
}

func (ts *TerminalSession) PumpOutput(sm *SessionManager) {
	defer ts.Close(sm)

	buf := make([]byte, 4096)
	for {
		n, err := ts.Pty.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.L.Error().Err(err).Str("session", ts.ID).Msg("Error reading from PTY")
			}
			return
		}

		if n == 0 {
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		ts.BroadcastBinary(data)
	}
}

func (sm *SessionManager) KillSession(sessionID string) {
	sm.mu.RLock()
	session, exists := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if exists {
		session.Close(sm)
	}
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

		conn.SetReadLimit(wsReadLimit)
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(wsPongWait))
		})

		sessionID := "jail-" + ctid
		session, err := GlobalSessionManager.GetOrCreateSession(sessionID, ctidInt)
		if err != nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte("Error starting session: "+err.Error()))
			_ = conn.Close()
			return
		}

		observer := &Observer{Conn: conn}
		if err := session.AddObserver(observer); err != nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte("Session unavailable"))
			_ = conn.Close()
			return
		}

		defer session.RemoveObserver(observer, GlobalSessionManager)

		done := make(chan struct{})
		defer close(done)

		go func() {
			ticker := time.NewTicker(wsPingPeriod)
			defer ticker.Stop()

			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					if err := observer.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)); err != nil {
						return
					}
				}
			}
		}()

		for {
			messageType, reader, err := conn.NextReader()
			if err != nil {
				return
			}

			if messageType != websocket.BinaryMessage {
				logger.L.Warn().
					Int("message_type", messageType).
					Str("session", sessionID).
					Msg("Rejected non-binary websocket frame")
				return
			}

			data, err := io.ReadAll(reader)
			if err != nil {
				logger.L.Warn().Err(err).Str("session", sessionID).Msg("Failed to read websocket frame")
				return
			}

			if len(data) == 0 {
				continue
			}

			switch data[0] {
			case controlInput:
				if len(data) == 1 {
					continue
				}
				if _, err := session.Pty.Write(data[1:]); err != nil {
					logger.L.Warn().Err(err).Str("session", sessionID).Msg("Failed to write terminal input to PTY")
					return
				}

			case controlResize:
				if len(data) == 1 {
					continue
				}

				var ws WindowSize
				if err := json.Unmarshal(data[1:], &ws); err != nil {
					logger.L.Warn().Err(err).Str("session", sessionID).Msg("Invalid resize payload")
					continue
				}

				if ws.Rows == 0 || ws.Cols == 0 {
					logger.L.Warn().Str("session", sessionID).Msg("Ignored zero-sized resize payload")
					continue
				}

				if err := pty.Setsize(session.Pty, &pty.Winsize{
					Rows: ws.Rows,
					Cols: ws.Cols,
					X:    ws.X,
					Y:    ws.Y,
				}); err != nil {
					logger.L.Warn().Err(err).Str("session", sessionID).Msg("Failed to resize PTY")
				}

			case controlKill:
				GlobalSessionManager.KillSession(sessionID)
				return

			default:
				logger.L.Warn().
					Uint8("control", data[0]).
					Str("session", sessionID).
					Msg("Rejected unknown websocket control byte")
				return
			}
		}
	}
}
