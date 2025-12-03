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
	"syscall"
	"unsafe"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type WindowSize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
	X    uint16
	Y    uint16
}

var WSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_get_jail: " + err.Error()})
			return
		}
		if j == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "jail_not_found"})
			return
		}

		sessionName := "sylve-jail-" + ctid
		checkSession := exec.Command("tmux", "has-session", "-t", sessionName)

		cmdArgs := []string{
			"tmux", "new-session",
			"-s", sessionName,
			"-d", "--",
		}

		ctidHash := utils.HashIntToNLetters(ctidInt, 5)
		cmdArgs = append(cmdArgs,
			"jexec", "-l", ctidHash,
			"login", "-f", "root",
		)

		if err := checkSession.Run(); err != nil {
			createSession := exec.Command(cmdArgs[0], cmdArgs[1:]...)
			if err := createSession.Run(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "unable_to_create_tmux_jail_session"})
				return
			}
		}

		conn, err := WSUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			logger.L.Error().Err(err).Msg("WebSocket upgrade failed")
			return
		}
		defer conn.Close()

		var wsWriteMu sync.Mutex
		safeWrite := func(mt int, data []byte) error {
			wsWriteMu.Lock()
			defer wsWriteMu.Unlock()
			return conn.WriteMessage(mt, data)
		}

		cmd := exec.Command("tmux", "attach-session", "-t", sessionName)
		cmd.Env = append(os.Environ(), "TERM=xterm")

		tty, err := pty.Start(cmd)
		if err != nil {
			_ = safeWrite(websocket.TextMessage, []byte(err.Error()))
			return
		}
		defer func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			}
			_ = tty.Close()
		}()

		done := make(chan struct{})
		var closeOnce sync.Once
		closeDone := func() {
			closeOnce.Do(func() {
				close(done)
			})
		}

		// Read from tmux (pty) and forward to WebSocket
		go func() {
			buf := make([]byte, 1024)
			for {
				select {
				case <-done:
					return
				default:
					n, err := tty.Read(buf)
					if err != nil {
						// Do NOT kill the tmux session here; just inform client and stop.
						_ = safeWrite(websocket.TextMessage, []byte("Terminal session closed."))
						closeDone()
						return
					}
					if n > 0 {
						if err := safeWrite(websocket.BinaryMessage, buf[:n]); err != nil {
							// WebSocket write failed; stop this goroutine.
							closeDone()
							return
						}
					}
				}
			}
		}()

		// Read from WebSocket and forward to tmux (pty)
		for {
			messageType, reader, err := conn.NextReader()
			if err != nil {
				// WebSocket closed/errored; detach client only.
				closeDone()
				return
			}

			if messageType == websocket.TextMessage {
				_ = safeWrite(websocket.TextMessage, []byte("Unexpected text message"))
				continue
			}

			header := make([]byte, 1)
			if _, err := reader.Read(header); err != nil {
				// Reader error; don't kill tmux session.
				closeDone()
				return
			}

			switch header[0] {
			case 0: // stdin
				_, _ = io.Copy(tty, reader)

			case 1: // resize
				var ws WindowSize
				if err := json.NewDecoder(reader).Decode(&ws); err != nil {
					_ = safeWrite(websocket.TextMessage, []byte("Error decoding resize: "+err.Error()))
					continue
				}
				_, _, errno := syscall.Syscall(
					syscall.SYS_IOCTL,
					tty.Fd(),
					syscall.TIOCSWINSZ,
					uintptr(unsafe.Pointer(&ws)),
				)
				if errno != 0 {
					_ = safeWrite(websocket.TextMessage, []byte("Resize error: "+errno.Error()))
				}

			case 2: // kill
				var killMsg struct {
					Kill string `json:"kill"`
				}
				if err := json.NewDecoder(reader).Decode(&killMsg); err != nil {
					continue
				}
				sid := killMsg.Kill
				if sid == "" {
					sid = sessionName
				}
				// Only explicit kill message actually destroys tmux session.
				_ = exec.Command("tmux", "kill-session", "-t", sid).Run()
				_ = safeWrite(websocket.TextMessage, []byte("Session killed: "+sid))
				if sid == sessionName {
					closeDone()
					return
				}
			}
		}
	}
}
