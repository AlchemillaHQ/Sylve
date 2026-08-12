// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package vncHandler

import (
	"compress/flate"
	"context"
	"errors"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/logger"
	libvirtSvc "github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:    128 * 1024,
	WriteBufferSize:   128 * 1024,
	EnableCompression: true,
	CheckOrigin:       func(r *http.Request) bool { return true },
}

var (
	activeConnections = make(map[string]*vncSessionOwner)
	connectionsMutex  sync.RWMutex
	sessionCounter    atomic.Uint64
)

type vncSessionOwner struct {
	id   uint64
	conn *websocket.Conn
}

const (
	writeWait  = 10 * time.Second
	pongWait   = 10 * time.Minute
	pingPeriod = pongWait / 2

	inputBufferSize  = 32 * 1024
	outputBufferSize = 256 * 1024
	maxMessageSize   = 10 * 1024 * 1024

	vncSessionInUseReason = "VNC session is already in use by another client"
	vncSessionTakenReason = "VNC session was overtaken by another client"
)

var errVNCServiceUnavailable = errors.New("vnc_service_unavailable")

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

func writeVNCHandshakeError(c *gin.Context, status int, code string) {
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   code,
		Data:    nil,
	})
}

func resolveVNCBackendEndpoint(svc *libvirtSvc.Service, port int) (string, error) {
	if svc == nil || svc.DB == nil {
		return "", errVNCServiceUnavailable
	}

	vm, err := svc.GetVMByVNCPort(port)
	if err != nil {
		return "", err
	}

	return net.JoinHostPort(
		libvirtSvc.NormalizeVNCBindAddressForDial(vm.VNCBind),
		strconv.Itoa(port),
	), nil
}

// @Summary Open a VM VNC WebSocket
// @Description Validate the requested VM-owned VNC port and upgrade to the existing authenticated WebSocket relay. This documents the HTTP handshake, not WebSocket frames.
// @Tags VM
// @Param port path int true "VM-owned VNC port" minimum(1) maximum(65535)
// @Param auth query string true "Opaque hex-encoded WebSocket authentication payload"
// @Param overtake query bool false "Take over an existing VNC session"
// @Success 101 {string} string "Switching Protocols"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "VNC Port Not Found"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 502 {object} internal.APIResponse[any] "Bad Gateway"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vnc/{port} [get]
func VNCProxyHandler(svc *libvirtSvc.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		port := c.Param("port")
		if port == "" {
			writeVNCHandshakeError(c, http.StatusBadRequest, "invalid_vnc_port")
			return
		}

		i, err := strconv.ParseInt(port, 10, 32)
		if err != nil || !utils.IsValidPort(int(i)) {
			writeVNCHandshakeError(c, http.StatusBadRequest, "invalid_vnc_port")
			return
		}

		backendEndpoint, err := resolveVNCBackendEndpoint(svc, int(i))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeVNCHandshakeError(c, http.StatusNotFound, "vnc_port_not_found")
			return
		}
		if err != nil {
			if !errors.Is(err, errVNCServiceUnavailable) {
				logger.L.Error().Err(err).Int64("port", i).Msg("Failed to resolve VNC backend")
			}
			writeVNCHandshakeError(c, http.StatusServiceUnavailable, "vnc_service_unavailable")
			return
		}

		wsConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer wsConn.Close()

		wsConn.EnableWriteCompression(true)
		_ = wsConn.SetCompressionLevel(flate.BestSpeed)
		if wsTCP, ok := wsConn.UnderlyingConn().(*net.TCPConn); ok {
			_ = wsTCP.SetNoDelay(true)
			_ = wsTCP.SetReadBuffer(256 * 1024)
			_ = wsTCP.SetWriteBuffer(256 * 1024)
		}

		overtake := false
		switch strings.ToLower(c.Query("overtake")) {
		case "1", "true", "yes":
			overtake = true
		}

		connectionsMutex.Lock()
		existingSession, hasExistingSession := activeConnections[port]
		if hasExistingSession && !overtake {
			connectionsMutex.Unlock()
			_ = wsConn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseTryAgainLater, vncSessionInUseReason),
				time.Now().Add(writeWait),
			)
			return
		}
		sessionID := sessionCounter.Add(1)
		activeConnections[port] = &vncSessionOwner{
			id:   sessionID,
			conn: wsConn,
		}
		connectionsMutex.Unlock()

		if hasExistingSession && overtake && existingSession != nil {
			_ = existingSession.conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, vncSessionTakenReason),
				time.Now().Add(writeWait),
			)
			_ = existingSession.conn.Close()
		}

		defer func() {
			connectionsMutex.Lock()
			if owner, ok := activeConnections[port]; ok && owner.id == sessionID {
				delete(activeConnections, port)
			}
			connectionsMutex.Unlock()
		}()

		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		var dialer net.Dialer
		rawConn, err := dialer.DialContext(ctx, "tcp", backendEndpoint)
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
		backendChecksum := crc32.NewIEEE()

		defer func() {
			logger.L.Info().
				Str("port", port).
				Str("target", backendEndpoint).
				Str("backend", "bhyve-vnc").
				Dur("duration", time.Since(metrics.startTime)).
				Uint64("rx", metrics.bytesReceived.Load()).
				Uint64("tx", metrics.bytesSent.Load()).
				Uint32("tx_crc32", backendChecksum.Sum32()).
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

				_, _ = backendChecksum.Write(buf[:n])
				metrics.addSent(n)
			}
		}()

		wg.Wait()
	}
}
