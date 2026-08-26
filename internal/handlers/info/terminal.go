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
	Username  string
	Cmd       *exec.Cmd
	Pty       *os.File
	Observer  *hostObserver
	closeOnce sync.Once
}

var (
	hostTerminalCommand = func(username string) *exec.Cmd {
		if username == "root" {
			return exec.Command("login", "-f", "root")
		}
		return exec.Command("login")
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

func newHostTerminalSession(username string, observer *hostObserver) (*hostTerminalSession, error) {
	cmd := hostTerminalCommand(username)
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

	return &hostTerminalSession{
		Username: username,
		Cmd:      cmd,
		Pty:      ptymx,
		Observer: observer,
	}, nil
}

func (ts *hostTerminalSession) Close() {
	ts.closeOnce.Do(func() {
		if ts.Observer != nil {
			_ = ts.Observer.Close()
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
	})
}

func (ts *hostTerminalSession) PumpOutput() {
	defer ts.Close()

	buf := make([]byte, 4096)
	for {
		n, err := ts.Pty.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.L.Error().Err(err).Str("username", ts.Username).Msg("error reading from host terminal PTY")
			}
			return
		}

		if n == 0 {
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		if err := ts.Observer.WriteMessage(websocket.BinaryMessage, data); err != nil {
			logger.L.Warn().Err(err).Str("username", ts.Username).Msg("failed to write host terminal output to websocket")
			return
		}
	}
}

// @Summary Open a host terminal WebSocket
// @Description Upgrade the connection to an authenticated host terminal WebSocket
// @Tags Info
// @Param auth query string true "Hex-encoded WebSocket authentication payload"
// @Success 101 {string} string "Switching Protocols"
// @Failure 400 {string} string "WebSocket upgrade failed"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Router /info/terminal [get]
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

	username, _ := c.Get("Username")
	usernameStr, _ := username.(string)
	if usernameStr == "" {
		usernameStr = "root"
	}

	observer := &hostObserver{Conn: conn}
	if usernameStr != "root" {
		banner := "\r\n\x1b[33mNote: Direct root login is disabled on this terminal.\x1b[0m\r\n" +
			"\x1b[33mLog in as a regular user, then use 'su -' to switch to root.\x1b[0m\r\n\r\n"
		_ = observer.WriteMessage(websocket.BinaryMessage, []byte(banner))
	}

	session, err := newHostTerminalSession(usernameStr, observer)
	if err != nil {
		_ = observer.WriteMessage(websocket.TextMessage, []byte("Error starting session: "+err.Error()))
		_ = observer.Close()
		return
	}
	defer session.Close()
	go session.PumpOutput()

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
			logger.L.Warn().Int("message_type", messageType).Str("username", usernameStr).Msg("rejected non-binary host terminal websocket frame")
			return
		}

		data, err := io.ReadAll(reader)
		if err != nil {
			logger.L.Warn().Err(err).Str("username", usernameStr).Msg("failed to read host terminal websocket frame")
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
				logger.L.Warn().Err(err).Str("username", usernameStr).Msg("failed to write terminal input to PTY")
				return
			}

		case hostControlResize:
			if len(data) == 1 {
				continue
			}

			var ws hostWindowSize
			if err := json.Unmarshal(data[1:], &ws); err != nil {
				logger.L.Warn().Err(err).Str("username", usernameStr).Msg("invalid host terminal resize payload")
				continue
			}

			if ws.Rows == 0 || ws.Cols == 0 {
				logger.L.Warn().Str("username", usernameStr).Msg("ignored zero-sized host terminal resize payload")
				continue
			}

			if err := pty.Setsize(session.Pty, &pty.Winsize{Rows: ws.Rows, Cols: ws.Cols, X: ws.X, Y: ws.Y}); err != nil {
				logger.L.Warn().Err(err).Str("username", usernameStr).Msg("failed to resize host terminal PTY")
			}

		case hostControlKill:
			session.Close()
			return

		default:
			logger.L.Warn().Uint8("control", data[0]).Str("username", usernameStr).Msg("rejected unknown host terminal websocket control byte")
			return
		}
	}
}
