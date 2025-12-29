// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package vncHandler

import (
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:    8 * 1024,
	WriteBufferSize:   8 * 1024,
	EnableCompression: false,
	CheckOrigin:       func(r *http.Request) bool { return true },
}

// activeConnections tracks active VNC connections per port
var (
	activeConnections = make(map[string]bool)
	connectionsMutex  = sync.RWMutex{}
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 10 * time.Minute // how long we’ll wait for the next pong
	pingPeriod = pongWait / 2     // how often we’ll send pings (must be < pongWait)
)

func VNCProxyHandler(c *gin.Context) {
	port := c.Param("port")
	if port == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing 'port' parameter"})
		return
	}

	// Check if there's already an active connection to this port
	connectionsMutex.Lock()
	portInUse := activeConnections[port]
	if !portInUse {
		// Reserve this port
		activeConnections[port] = true
	}
	connectionsMutex.Unlock()

	// Ensure we clean up the connection tracking when we're done
	defer func() {
		if !portInUse {
			connectionsMutex.Lock()
			delete(activeConnections, port)
			connectionsMutex.Unlock()
		}
	}()

	// Upgrade to WebSocket first
	wsConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer wsConn.Close()

	// If port is in use, close WebSocket with custom error
	if portInUse {
		wsConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4409, "VNC port is already in use by another client"))
		return
	}

	rawConn, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		wsConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Failed to connect to VNC"))
		return
	}
	defer rawConn.Close()

	if tcp, ok := rawConn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
	}

	// Keepalive: expect a pong within pongWait; extend on each pong.
	wsConn.SetReadDeadline(time.Now().Add(pongWait))
	wsConn.SetPongHandler(func(string) error {
		wsConn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	done := make(chan struct{})
	var once sync.Once
	closeDone := func() { once.Do(func() { close(done) }) }

	// Ping loop (WS → client)
	pingTicker := time.NewTicker(pingPeriod)
	defer pingTicker.Stop()
	go func() {
		defer closeDone()
		for {
			select {
			case <-done:
				return
			case <-pingTicker.C:
				// Write a ping; if it fails, terminate.
				_ = wsConn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := wsConn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
					return
				}
			}
		}
	}()

	buf := make([]byte, 32*1024)

	// WS → VNC
	go func() {
		defer closeDone()
		for {
			msgType, reader, err := wsConn.NextReader()
			if err != nil {
				return
			}
			if msgType != websocket.BinaryMessage {
				_, _ = io.Copy(io.Discard, reader)
				continue
			}
			if _, err := io.Copy(rawConn, reader); err != nil {
				return
			}
		}
	}()

	// VNC → WS
	go func() {
		defer closeDone()
		for {
			n, err := rawConn.Read(buf)
			if err != nil {
				if err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") {
					logger.L.Debug().Err(err).Msg("VNC read error")
				}
				return
			}
			_ = wsConn.SetWriteDeadline(time.Now().Add(writeWait))
			w, err := wsConn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return
			}
			if _, err := w.Write(buf[:n]); err != nil {
				_ = w.Close()
				return
			}
			if err := w.Close(); err != nil {
				return
			}
		}
	}()

	<-done
}
