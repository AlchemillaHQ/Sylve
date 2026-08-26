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
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	wsReadLimit         = 64 << 10 // 64 KiB
	wsWriteTimeout      = 10 * time.Second
	wsPongWait          = 60 * time.Second
	wsPingPeriod        = (wsPongWait * 9) / 10
	wsHistoryLimit      = 16 << 10 // 16 KiB
	defaultRows         = 24
	defaultCols         = 80
	controlInput   byte = 0
	controlResize  byte = 1
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
	if o == nil || o.Conn == nil {
		return nil
	}

	o.Mu.Lock()
	defer o.Mu.Unlock()
	return o.Conn.Close()
}

type TerminalSession struct {
	ID        string
	Cmd       *exec.Cmd
	Pty       *os.File
	Observers map[*Observer]struct{}

	Mu           sync.Mutex
	closeOnce    sync.Once
	closed       bool
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

	cmd := exec.Command("jexec", "-l", ctidHash, "su", "-l", "root")
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
		ID:           sessionID,
		Cmd:          cmd,
		Pty:          ptymx,
		Observers:    make(map[*Observer]struct{}),
		History:      make([]byte, 0, wsHistoryLimit),
		HistoryLimit: wsHistoryLimit,
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

func (ts *TerminalSession) AddObserverAndReplay(obs *Observer) error {
	// Hold the observer writer while registering it so live PTY output cannot
	// overtake or duplicate the history snapshot.
	obs.Mu.Lock()
	defer obs.Mu.Unlock()

	ts.Mu.Lock()
	if ts.closed {
		ts.Mu.Unlock()
		return errors.New("session is closed")
	}

	history := append([]byte(nil), ts.History...)
	ts.Observers[obs] = struct{}{}
	ts.Mu.Unlock()

	if len(history) == 0 {
		return nil
	}
	_ = obs.Conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return obs.Conn.WriteMessage(websocket.BinaryMessage, history)
}

func (ts *TerminalSession) RemoveObserver(obs *Observer, sm *SessionManager) {
	ts.Mu.Lock()
	delete(ts.Observers, obs)
	closeSession := !ts.closed && len(ts.Observers) == 0
	if closeSession {
		// Prevent a new observer from joining between the last disconnect and
		// session cleanup.
		ts.closed = true
	}
	ts.Mu.Unlock()

	_ = obs.Close()
	if closeSession {
		ts.Close(sm)
	}
}

func (ts *TerminalSession) BroadcastBinary(payload []byte, sm *SessionManager) {
	ts.Mu.Lock()
	if ts.closed {
		ts.Mu.Unlock()
		return
	}

	ts.History = append(ts.History, payload...)
	if ts.HistoryLimit > 0 && len(ts.History) > ts.HistoryLimit {
		ts.History = ts.History[len(ts.History)-ts.HistoryLimit:]
	}

	observers := make([]*Observer, 0, len(ts.Observers))
	for obs := range ts.Observers {
		observers = append(observers, obs)
	}
	ts.Mu.Unlock()

	for _, obs := range observers {
		if err := obs.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			logger.L.Warn().Err(err).Str("session", ts.ID).Msg("Failed to write PTY output to websocket")
			ts.RemoveObserver(obs, sm)
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
		ts.BroadcastBinary(data, sm)
	}
}

type jailConsoleService interface {
	JailExistsByCTID(ctID uint) (bool, error)
	JailRestoreInProgress(ctID uint) (bool, error)
	CanMutateProtectedJail(ctID uint) (bool, error)
	GetStateByCtId(ctID uint) (jailServiceInterfaces.State, error)
}

func writeJailConsoleError(c *gin.Context, status int, code string, err error) {
	detail := code
	if err != nil {
		detail = err.Error()
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   detail,
		Data:    nil,
	})
}

// @Summary Open a jail console WebSocket
// @Description Validate that the jail console is available and upgrade to an authenticated interactive WebSocket session
// @Tags Jail
// @Security BearerAuth
// @Param ctid path int true "Jail CTID" minimum(1)
// @Param auth query string true "Hex-encoded WebSocket authentication payload"
// @Success 101 {string} string "Switching Protocols"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "Jail Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /jail/{ctid}/console [get]
func HandleJailTerminalWebsocket(jailService jailConsoleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctID, ok := parseJailCTID(c, "ctid")
		if !ok {
			return
		}
		ctIDText := strconv.FormatUint(uint64(ctID), 10)

		exists, err := jailService.JailExistsByCTID(ctID)
		if err != nil {
			writeJailConsoleError(c, http.StatusInternalServerError, "failed_to_get_jail", err)
			return
		}
		if !exists {
			writeJailConsoleError(c, http.StatusNotFound, "jail_not_found", nil)
			return
		}

		restoring, err := jailService.JailRestoreInProgress(ctID)
		if err != nil {
			writeJailConsoleError(c, http.StatusServiceUnavailable, "jail_console_guard_unavailable", err)
			return
		}
		if restoring {
			writeJailConsoleError(c, http.StatusConflict, "restore_in_progress", nil)
			return
		}

		allowed, guardErr := jailService.CanMutateProtectedJail(ctID)
		if guardErr != nil {
			writeJailConsoleError(c, http.StatusServiceUnavailable, "jail_console_guard_unavailable", guardErr)
			return
		}
		if !allowed {
			writeJailConsoleError(c, http.StatusForbidden, "replication_lease_not_owned", nil)
			return
		}

		state, err := jailService.GetStateByCtId(ctID)
		if err != nil {
			writeJailConsoleError(c, http.StatusServiceUnavailable, "jail_state_unavailable", err)
			return
		}
		if !strings.EqualFold(strings.TrimSpace(state.State), "ACTIVE") {
			writeJailConsoleError(c, http.StatusConflict, "jail_console_requires_active_jail", nil)
			return
		}

		if !websocket.IsWebSocketUpgrade(c.Request) {
			writeJailConsoleError(c, http.StatusBadRequest, "websocket_upgrade_required", nil)
			return
		}

		conn, err := WSUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			logger.L.Warn().Err(err).Uint("ctid", ctID).Msg("failed to upgrade jail console websocket")
			return
		}

		conn.SetReadLimit(wsReadLimit)
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(wsPongWait))
		})

		sessionID := "jail-" + ctIDText
		session, err := GlobalSessionManager.GetOrCreateSession(sessionID, int(ctID))
		if err != nil {
			logger.L.Error().Err(err).Uint("ctid", ctID).Msg("failed to start jail console session")
			_ = conn.WriteMessage(websocket.TextMessage, []byte("Jail console unavailable"))
			_ = conn.Close()
			return
		}

		observer := &Observer{Conn: conn}
		if err := session.AddObserverAndReplay(observer); err != nil {
			logger.L.Warn().Err(err).Str("session", sessionID).Msg("failed to join jail console session")
			session.RemoveObserver(observer, GlobalSessionManager)
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
						_ = observer.Close()
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
