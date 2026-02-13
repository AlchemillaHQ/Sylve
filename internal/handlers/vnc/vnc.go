// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package vncHandler

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:    128 * 1024,
	WriteBufferSize:   128 * 1024,
	EnableCompression: false,
	CheckOrigin:       func(r *http.Request) bool { return true },
}

var (
	activeConnections = make(map[string]bool)
	connectionsMutex  sync.RWMutex
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 10 * time.Minute
	pingPeriod = pongWait / 2

	inputBufferSize  = 32 * 1024
	outputBufferSize = 256 * 1024
	maxMessageSize   = 10 * 1024 * 1024
)

type connectionMetrics struct {
	startTime     time.Time
	bytesReceived atomic.Uint64
	bytesSent     atomic.Uint64
}

func (m *connectionMetrics) addReceived(n int) {
	m.bytesReceived.Add(uint64(n))
}

func (m *connectionMetrics) addSent(n int) {
	m.bytesSent.Add(uint64(n))
}

func VNCProxyHandler(c *gin.Context) {
	port := c.Param("port")
	if port == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing 'port' parameter"})
		return
	}

	i, err := strconv.ParseInt(port, 10, 32)
	if err != nil || !utils.IsValidPort(int(i)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid port"})
		return
	}

	connectionsMutex.Lock()
	if activeConnections[port] {
		connectionsMutex.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "VNC port is already in use"})
		return
	}
	activeConnections[port] = true
	connectionsMutex.Unlock()

	defer func() {
		connectionsMutex.Lock()
		delete(activeConnections, port)
		connectionsMutex.Unlock()
	}()

	wsConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer wsConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	var dialer net.Dialer
	rawConn, err := dialer.DialContext(ctx, "tcp", "localhost:"+port)
	cancel()

	if err != nil {
		wsConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Failed to connect to VNC backend"))
		return
	}
	defer rawConn.Close()

	if tcp, ok := rawConn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetReadBuffer(256 * 1024)
		_ = tcp.SetWriteBuffer(64 * 1024)
	}

	wsConn.SetReadLimit(maxMessageSize)
	metrics := &connectionMetrics{startTime: time.Now()}

	defer func() {
		logger.L.Info().
			Str("port", port).
			Str("backend", "bhyve-vnc").
			Dur("duration", time.Since(metrics.startTime)).
			Uint64("rx", metrics.bytesReceived.Load()).
			Uint64("tx", metrics.bytesSent.Load()).
			Msg("VNC session ended")
	}()

	quit := make(chan struct{})
	closeOnce := sync.Once{}

	closeConns := func() {
		closeOnce.Do(func() {
			close(quit)
			wsConn.Close()
			rawConn.Close()
		})
	}

	wsConn.SetReadDeadline(time.Now().Add(pongWait))
	wsConn.SetPongHandler(func(string) error {
		wsConn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()

		for {
			select {
			case <-quit:
				return
			case <-ticker.C:
				if err := wsConn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
					closeConns()
					return
				}
			}
		}
	}()

	go func() {
		defer wg.Done()
		defer closeConns()

		inputBuf := make([]byte, inputBufferSize)
		for {
			_, reader, err := wsConn.NextReader()
			if err != nil {
				return
			}

			n, err := io.CopyBuffer(rawConn, reader, inputBuf)
			if err != nil {
				return
			}
			metrics.addReceived(int(n))
		}
	}()

	go func() {
		defer wg.Done()
		defer closeConns()

		buf := make([]byte, outputBufferSize)

		for {
			rawConn.SetReadDeadline(time.Time{})
			n, err := rawConn.Read(buf)
			if err != nil {
				return
			}

			if n < len(buf) {
				// Drain any bytes that are already buffered without adding delay.
				for n < len(buf) {
					rawConn.SetReadDeadline(time.Now())
					m, err := rawConn.Read(buf[n:])
					if m > 0 {
						n += m
					}
					if err == nil {
						continue
					}

					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						break
					}

					// Flush what we have if the peer closed after sending data.
					if errors.Is(err, io.EOF) && n > 0 {
						break
					}

					return
				}
			}

			wsConn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				return
			}

			metrics.addSent(n)
		}
	}()

	wg.Wait()
}
