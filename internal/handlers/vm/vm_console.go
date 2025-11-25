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
	"syscall"
	"unsafe"

	"github.com/alchemillahq/sylve/internal/logger"

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

func HandleLibvirtTerminalWebsocket(c *gin.Context) {
	rid := c.Query("rid")
	if rid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rid is required"})
		return
	}

	baudRate := c.DefaultQuery("baudrate", "115200")

	sessionName := "sylve-vmc-" + rid
	checkSession := exec.Command("tmux", "has-session", "-t", sessionName)
	ridInt, err := strconv.Atoi(rid)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid rid"})
		return
	}

	if err := checkSession.Run(); err != nil {
		createSession := exec.Command(
			"tmux",
			"new-session",
			"-s", sessionName,
			"-d",
			"cu", "-l", "/dev/nmdm"+strconv.Itoa(ridInt)+"B", "-s", baudRate,
		)

		if err := createSession.Run(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to create tmux jail session"})
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
		safeWrite(websocket.TextMessage, []byte(err.Error()))
		return
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Process.Wait()
		tty.Close()
	}()

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			select {
			case <-done:
				return
			default:
				n, err := tty.Read(buf)
				if err != nil {
					safeWrite(websocket.TextMessage, []byte("Terminal session closed."))
					return
				}
				safeWrite(websocket.BinaryMessage, buf[:n])
			}
		}
	}()

	for {
		messageType, reader, err := conn.NextReader()
		if err != nil {
			close(done)
			return
		}

		if messageType == websocket.TextMessage {
			safeWrite(websocket.TextMessage, []byte("Unexpected text message"))
			continue
		}

		header := make([]byte, 1)
		if _, err := reader.Read(header); err != nil {
			close(done)
			return
		}

		switch header[0] {
		case 0: // stdin
			io.Copy(tty, reader)

		case 1: // resize
			var ws WindowSize
			if err := json.NewDecoder(reader).Decode(&ws); err != nil {
				safeWrite(websocket.TextMessage, []byte("Error decoding resize: "+err.Error()))
				continue
			}
			_, _, errno := syscall.Syscall(
				syscall.SYS_IOCTL,
				tty.Fd(),
				syscall.TIOCSWINSZ,
				uintptr(unsafe.Pointer(&ws)),
			)
			if errno != 0 {
				safeWrite(websocket.TextMessage, []byte("Resize error: "+errno.Error()))
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
			exec.Command("tmux", "kill-session", "-t", sid).Run()
			safeWrite(websocket.TextMessage, []byte("Session killed: "+sid))
			if sid == sessionName {
				close(done)
				return
			}
		}
	}
}
