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
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	vmWSReadLimit         = 10 << 20 // 10 MiB
	vmWSWriteTimeout      = 10 * time.Second
	vmWSPongWait          = 60 * time.Second
	vmWSPingPeriod        = (vmWSPongWait * 9) / 10
	vmControlInput   byte = 0
	vmControlResize  byte = 1
	vmControlKill    byte = 2
)

type VMObserver struct {
	Conn *websocket.Conn
	Mu   sync.Mutex
}

func (o *VMObserver) WriteMessage(messageType int, payload []byte) error {
	o.Mu.Lock()
	defer o.Mu.Unlock()

	_ = o.Conn.SetWriteDeadline(time.Now().Add(vmWSWriteTimeout))
	return o.Conn.WriteMessage(messageType, payload)
}

func (o *VMObserver) WriteControl(messageType int, payload []byte, deadline time.Time) error {
	o.Mu.Lock()
	defer o.Mu.Unlock()

	_ = o.Conn.SetWriteDeadline(deadline)
	return o.Conn.WriteControl(messageType, payload, deadline)
}

func (o *VMObserver) Close() error {
	o.Mu.Lock()
	defer o.Mu.Unlock()
	return o.Conn.Close()
}

type VMSession struct {
	ID           string
	Cmd          *exec.Cmd
	Pty          *os.File
	Observers    map[*VMObserver]struct{}
	Mu           sync.Mutex
	closeOnce    sync.Once
	closed       bool
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
	VMWSUpgrader = websocket.Upgrader{
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

func (sm *VMSessionManager) GetOrCreateSession(sessionID string, ridInt int, baudRate string) (*VMSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, exists := sm.sessions[sessionID]; exists && !session.IsClosed() {
		return session, nil
	}

	devPath := "/dev/nmdm" + strconv.Itoa(ridInt) + "B"
	cmd := exec.Command("cu", "-l", devPath, "-s", baudRate)
	cmd.Env = append(os.Environ(), "TERM=xterm")

	ptymx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	session := &VMSession{
		ID:           sessionID,
		Cmd:          cmd,
		Pty:          ptymx,
		Observers:    make(map[*VMObserver]struct{}),
		History:      make([]byte, 0, 16384),
		HistoryLimit: 16384,
	}

	sm.sessions[sessionID] = session
	go session.PumpOutput(sm)

	return session, nil
}

func (sm *VMSessionManager) removeSession(sessionID string, session *VMSession) {
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

func (ts *VMSession) IsClosed() bool {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()
	return ts.closed
}

func (ts *VMSession) Close(sm *VMSessionManager) {
	ts.closeOnce.Do(func() {
		ts.Mu.Lock()
		ts.closed = true

		observers := make([]*VMObserver, 0, len(ts.Observers))
		for obs := range ts.Observers {
			observers = append(observers, obs)
		}
		ts.Observers = make(map[*VMObserver]struct{})
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

func (ts *VMSession) AddObserver(obs *VMObserver) error {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()

	if ts.closed {
		return errors.New("session is closed")
	}

	ts.Observers[obs] = struct{}{}
	return nil
}

func (ts *VMSession) RemoveObserver(obs *VMObserver) {
	ts.Mu.Lock()
	delete(ts.Observers, obs)
	ts.Mu.Unlock()

	_ = obs.Close()
}

func (ts *VMSession) ReplayHistory(obs *VMObserver) error {
	ts.Mu.Lock()
	if ts.closed {
		ts.Mu.Unlock()
		return errors.New("session is closed")
	}

	if len(ts.History) == 0 {
		ts.Mu.Unlock()
		return nil
	}

	history := make([]byte, len(ts.History))
	copy(history, ts.History)
	ts.Mu.Unlock()

	return obs.WriteMessage(websocket.BinaryMessage, history)
}

func (ts *VMSession) BroadcastBinary(payload []byte) {
	ts.Mu.Lock()
	if ts.closed {
		ts.Mu.Unlock()
		return
	}

	ts.History = append(ts.History, payload...)
	if len(ts.History) > ts.HistoryLimit {
		ts.History = ts.History[len(ts.History)-ts.HistoryLimit:]
	}

	observers := make([]*VMObserver, 0, len(ts.Observers))
	for obs := range ts.Observers {
		observers = append(observers, obs)
	}
	ts.Mu.Unlock()

	for _, obs := range observers {
		if err := obs.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			logger.L.Warn().Err(err).Str("session", ts.ID).Msg("failed to write VM PTY output to websocket")
			ts.RemoveObserver(obs)
		}
	}
}

func (ts *VMSession) PumpOutput(sm *VMSessionManager) {
	defer ts.Close(sm)

	buf := make([]byte, 4096)
	for {
		n, err := ts.Pty.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.L.Error().Err(err).Str("session", ts.ID).Msg("error reading from VM PTY")
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

func (sm *VMSessionManager) KillSession(sessionID string) {
	sm.mu.RLock()
	session, exists := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if exists {
		session.Close(sm)
	}
}

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

	conn, err := VMWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.L.Error().Err(err).Msg("websocket upgrade failed")
		return
	}

	conn.SetReadLimit(vmWSReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(vmWSPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(vmWSPongWait))
	})

	sessionID := "vm-console-" + rid
	session, err := GlobalVMSessionManager.GetOrCreateSession(sessionID, ridInt, baudRate)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Error connecting to console: "+err.Error()))
		_ = conn.Close()
		return
	}

	observer := &VMObserver{Conn: conn}
	if err := session.AddObserver(observer); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Session unavailable"))
		_ = conn.Close()
		return
	}

	if err := session.ReplayHistory(observer); err != nil {
		session.RemoveObserver(observer)
		return
	}

	defer session.RemoveObserver(observer)

	done := make(chan struct{})
	defer close(done)

	go func() {
		ticker := time.NewTicker(vmWSPingPeriod)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := observer.WriteControl(websocket.PingMessage, nil, time.Now().Add(vmWSWriteTimeout)); err != nil {
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
		case vmControlInput:
			if len(data) == 1 {
				continue
			}
			if _, err := session.Pty.Write(data[1:]); err != nil {
				logger.L.Warn().Err(err).Str("session", sessionID).Msg("failed to write serial input to PTY")
				return
			}

		case vmControlResize:
			if len(data) == 1 {
				continue
			}

			var ws WindowSize
			if err := json.Unmarshal(data[1:], &ws); err != nil {
				logger.L.Warn().Err(err).Str("session", sessionID).Msg("invalid resize payload")
				continue
			}

			if ws.Rows == 0 || ws.Cols == 0 {
				logger.L.Warn().Str("session", sessionID).Msg("ignored zero-sized resize payload")
				continue
			}

			if err := pty.Setsize(session.Pty, &pty.Winsize{
				Rows: ws.Rows,
				Cols: ws.Cols,
				X:    ws.X,
				Y:    ws.Y,
			}); err != nil {
				logger.L.Warn().Err(err).Str("session", sessionID).Msg("failed to resize PTY")
			}

		case vmControlKill:
			if len(data) > 1 {
				var killMsg struct {
					Kill string `json:"kill"`
				}
				if err := json.Unmarshal(data[1:], &killMsg); err == nil {
					if killMsg.Kill != "" && killMsg.Kill != sessionID {
						continue
					}
				}
			}

			GlobalVMSessionManager.KillSession(sessionID)
			return

		default:
			logger.L.Warn().Uint8("control", data[0]).Str("session", sessionID).Msg("rejected unknown websocket control byte")
			return
		}
	}
}
