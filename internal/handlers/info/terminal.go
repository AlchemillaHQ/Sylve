// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	hostWSReadLimit         = 10 << 20 // 1 MiB
	hostWSWriteTimeout      = 10 * time.Second
	hostWSPongWait          = 60 * time.Second
	hostWSPingPeriod        = (hostWSPongWait * 9) / 10
	hostDefaultRows         = 24
	hostDefaultCols         = 80
	hostControlInput   byte = 0
	hostControlResize  byte = 1
	hostControlKill    byte = 2
)

type hostObserver struct {
	Conn *websocket.Conn
	Mu   sync.Mutex
}

func (o *hostObserver) WriteMessage(messageType int, payload []byte) error {
	o.Mu.Lock()
	defer o.Mu.Unlock()

	_ = o.Conn.SetWriteDeadline(time.Now().Add(hostWSWriteTimeout))
	return o.Conn.WriteMessage(messageType, payload)
}

func (o *hostObserver) WriteControl(messageType int, payload []byte, deadline time.Time) error {
	o.Mu.Lock()
	defer o.Mu.Unlock()

	_ = o.Conn.SetWriteDeadline(deadline)
	return o.Conn.WriteControl(messageType, payload, deadline)
}

func (o *hostObserver) Close() error {
	o.Mu.Lock()
	defer o.Mu.Unlock()
	return o.Conn.Close()
}

type hostTerminalSession struct {
	ID        string
	Cmd       *exec.Cmd
	Pty       *os.File
	Observers map[*hostObserver]struct{}

	Mu        sync.Mutex
	closeOnce sync.Once
	closed    bool
}

type hostSessionManager struct {
	sessions map[string]*hostTerminalSession
	mu       sync.RWMutex
}

var (
	hostTerminalSessionManager = &hostSessionManager{
		sessions: make(map[string]*hostTerminalSession),
	}
	hostTerminalWSUpgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
)

type hostWindowSize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
	X    uint16 `json:"x"`
	Y    uint16 `json:"y"`
}

func (sm *hostSessionManager) GetOrCreateSession(sessionID string) (*hostTerminalSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, exists := sm.sessions[sessionID]; exists && !session.IsClosed() {
		return session, nil
	}

	cmd := exec.Command("login", "-f", "root")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptymx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	if err := pty.Setsize(ptymx, &pty.Winsize{Rows: hostDefaultRows, Cols: hostDefaultCols}); err != nil {
		_ = ptymx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}

	session := &hostTerminalSession{
		ID:        sessionID,
		Cmd:       cmd,
		Pty:       ptymx,
		Observers: make(map[*hostObserver]struct{}),
	}

	sm.sessions[sessionID] = session
	go session.PumpOutput(sm)

	return session, nil
}

func (sm *hostSessionManager) removeSession(sessionID string, session *hostTerminalSession) {
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

func (ts *hostTerminalSession) IsClosed() bool {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()
	return ts.closed
}

func (ts *hostTerminalSession) Close(sm *hostSessionManager) {
	ts.closeOnce.Do(func() {
		ts.Mu.Lock()
		ts.closed = true

		observers := make([]*hostObserver, 0, len(ts.Observers))
		for obs := range ts.Observers {
			observers = append(observers, obs)
		}
		ts.Observers = make(map[*hostObserver]struct{})
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

func (ts *hostTerminalSession) AddObserver(obs *hostObserver) error {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()

	if ts.closed {
		return errors.New("session is closed")
	}

	ts.Observers[obs] = struct{}{}
	return nil
}

func (ts *hostTerminalSession) RemoveObserver(obs *hostObserver, sm *hostSessionManager) {
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

func (ts *hostTerminalSession) BroadcastBinary(payload []byte) {
	ts.Mu.Lock()
	if ts.closed {
		ts.Mu.Unlock()
		return
	}

	observers := make([]*hostObserver, 0, len(ts.Observers))
	for obs := range ts.Observers {
		observers = append(observers, obs)
	}
	ts.Mu.Unlock()

	for _, obs := range observers {
		if err := obs.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			logger.L.Warn().Err(err).Str("session", ts.ID).Msg("failed to write PTY output to websocket")
		}
	}
}

func (ts *hostTerminalSession) PumpOutput(sm *hostSessionManager) {
	defer ts.Close(sm)

	buf := make([]byte, 4096)
	for {
		n, err := ts.Pty.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.L.Error().Err(err).Str("session", ts.ID).Msg("error reading from PTY")
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

func (sm *hostSessionManager) KillSession(sessionID string) {
	sm.mu.RLock()
	session, exists := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if exists {
		session.Close(sm)
	}
}

func HandleHostTerminal(c *gin.Context) {
	conn, err := hostTerminalWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	conn.SetReadLimit(hostWSReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(hostWSPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(hostWSPongWait))
	})

	const sessionID = "host-terminal"
	session, err := hostTerminalSessionManager.GetOrCreateSession(sessionID)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Error starting session: "+err.Error()))
		_ = conn.Close()
		return
	}

	observer := &hostObserver{Conn: conn}
	if err := session.AddObserver(observer); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Session unavailable"))
		_ = conn.Close()
		return
	}

	defer session.RemoveObserver(observer, hostTerminalSessionManager)

	done := make(chan struct{})
	defer close(done)

	go func() {
		ticker := time.NewTicker(hostWSPingPeriod)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := observer.WriteControl(websocket.PingMessage, nil, time.Now().Add(hostWSWriteTimeout)); err != nil {
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
			logger.L.Warn().Int("message_type", messageType).Str("session", sessionID).Msg("rejected non-binary websocket frame")
			return
		}

		data, err := io.ReadAll(reader)
		if err != nil {
			logger.L.Warn().Err(err).Str("session", sessionID).Msg("failed to read websocket frame")
			return
		}

		if len(data) == 0 {
			continue
		}

		switch data[0] {
		case hostControlInput:
			if len(data) == 1 {
				continue
			}
			if _, err := session.Pty.Write(data[1:]); err != nil {
				logger.L.Warn().Err(err).Str("session", sessionID).Msg("failed to write terminal input to PTY")
				return
			}

		case hostControlResize:
			if len(data) == 1 {
				continue
			}

			var ws hostWindowSize
			if err := json.Unmarshal(data[1:], &ws); err != nil {
				logger.L.Warn().Err(err).Str("session", sessionID).Msg("invalid resize payload")
				continue
			}

			if ws.Rows == 0 || ws.Cols == 0 {
				logger.L.Warn().Str("session", sessionID).Msg("ignored zero-sized resize payload")
				continue
			}

			if err := pty.Setsize(session.Pty, &pty.Winsize{Rows: ws.Rows, Cols: ws.Cols, X: ws.X, Y: ws.Y}); err != nil {
				logger.L.Warn().Err(err).Str("session", sessionID).Msg("failed to resize PTY")
			}

		case hostControlKill:
			hostTerminalSessionManager.KillSession(sessionID)
			return

		default:
			logger.L.Warn().Uint8("control", data[0]).Str("session", sessionID).Msg("rejected unknown websocket control byte")
			return
		}
	}
}
