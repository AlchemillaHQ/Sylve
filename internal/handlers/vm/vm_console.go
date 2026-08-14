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
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	vmWSReadLimit             = 64 << 10 // 64 KiB
	vmWSWriteTimeout          = 10 * time.Second
	vmWSPongWait              = 60 * time.Second
	vmWSPingPeriod            = (vmWSPongWait * 9) / 10
	vmConsoleDefaultBaud      = "115200"
	vmConsoleMinBaud          = 50
	vmConsoleMaxBaud          = 4_000_000
	vmControlInput       byte = 0
	vmControlResize      byte = 1
)

type vmConsoleService interface {
	GetVMByRID(rid uint) (vmModels.VM, error)
	CanMutateProtectedVM(rid uint) (bool, error)
	GetLvDomain(rid uint) (*libvirtServiceInterfaces.LvDomain, error)
}

type vmConsoleRequest struct {
	RID        uint
	RIDText    string
	BaudRate   string
	DevicePath string
}

type vmConsoleValidationError struct {
	Code   string
	Detail string
}

func (e *vmConsoleValidationError) Error() string {
	return e.Code
}

var vmConsoleDeviceStat = os.Stat

func parseVMConsoleRequest(ridText, baudRate string) (vmConsoleRequest, error) {
	rid, err := strconv.ParseUint(strings.TrimSpace(ridText), 10, 32)
	if err != nil || rid == 0 {
		return vmConsoleRequest{}, &vmConsoleValidationError{
			Code:   "invalid_rid_format",
			Detail: "rid must be a positive integer",
		}
	}

	baudRate = strings.TrimSpace(baudRate)
	if baudRate == "" {
		baudRate = vmConsoleDefaultBaud
	}
	baud, err := strconv.ParseUint(baudRate, 10, 32)
	if err != nil || baud < vmConsoleMinBaud || baud > vmConsoleMaxBaud {
		return vmConsoleRequest{}, &vmConsoleValidationError{
			Code:   "invalid_baud_rate",
			Detail: "baudrate must be an integer between 50 and 4000000",
		}
	}

	normalizedRID := strconv.FormatUint(rid, 10)
	return vmConsoleRequest{
		RID:        uint(rid),
		RIDText:    normalizedRID,
		BaudRate:   strconv.FormatUint(baud, 10),
		DevicePath: "/dev/nmdm" + normalizedRID + "B",
	}, nil
}

func vmDomainSupportsConsole(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "blocked", "paused", "shutdown", "pmsuspended":
		return true
	default:
		return false
	}
}

type VMObserver struct {
	Conn *websocket.Conn
	Mu   sync.Mutex
}

func (o *VMObserver) WriteMessage(messageType int, payload []byte) error {
	o.Mu.Lock()
	defer o.Mu.Unlock()
	return o.writeMessageLocked(messageType, payload)
}

func (o *VMObserver) writeMessageLocked(messageType int, payload []byte) error {
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
	if o == nil || o.Conn == nil {
		return nil
	}

	o.Mu.Lock()
	defer o.Mu.Unlock()
	return o.Conn.Close()
}

type VMSession struct {
	ID           string
	BaudRate     string
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
		if session.BaudRate != baudRate {
			return nil, errors.New("vm_console_baud_rate_conflict")
		}
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
		BaudRate:     baudRate,
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

func (ts *VMSession) AddObserverAndReplay(obs *VMObserver) error {
	// Hold the observer writer while registering it so live output cannot
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
	return obs.writeMessageLocked(websocket.BinaryMessage, history)
}

func (ts *VMSession) RemoveObserver(obs *VMObserver, sm *VMSessionManager) {
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

func (ts *VMSession) BroadcastBinary(payload []byte, sm *VMSessionManager) {
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
			ts.RemoveObserver(obs, sm)
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
		ts.BroadcastBinary(data, sm)
	}
}

func writeVMConsoleError(c *gin.Context, status int, code, detail string) {
	if strings.TrimSpace(detail) == "" {
		detail = code
	}
	c.JSON(status, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   detail,
		Data:    nil,
	})
}

// @Summary Open a VM serial console WebSocket
// @Description Validate the VM serial console and upgrade to an authenticated interactive WebSocket session
// @Tags VM
// @Security BearerAuth
// @Param rid path int true "Virtual Machine RID"
// @Param baudrate query int false "Serial baud rate" default(115200)
// @Param auth query string true "Hex-encoded WebSocket authentication payload"
// @Success 101 {string} string "Switching Protocols"
// @Failure 400 {object} internal.APIResponse[any] "Bad Request"
// @Failure 401 {object} internal.APIResponse[any] "Unauthorized"
// @Failure 403 {object} internal.APIResponse[any] "Forbidden"
// @Failure 404 {object} internal.APIResponse[any] "VM Not Found"
// @Failure 409 {object} internal.APIResponse[any] "Conflict"
// @Failure 500 {object} internal.APIResponse[any] "Internal Server Error"
// @Failure 503 {object} internal.APIResponse[any] "Service Unavailable"
// @Router /vm/{rid}/console [get]
func HandleLibvirtTerminalWebsocket(libvirtService vmConsoleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		request, err := parseVMConsoleRequest(c.Param("rid"), c.Query("baudrate"))
		if err != nil {
			var validationErr *vmConsoleValidationError
			if errors.As(err, &validationErr) {
				writeVMConsoleError(c, http.StatusBadRequest, validationErr.Code, validationErr.Detail)
				return
			}
			writeVMConsoleError(c, http.StatusBadRequest, "invalid_console_request", err.Error())
			return
		}

		vm, err := libvirtService.GetVMByRID(request.RID)
		if err != nil {
			if isVMNotFoundError(err) {
				writeVMConsoleError(c, http.StatusNotFound, "vm_not_found", "vm_not_found")
				return
			}
			writeVMConsoleError(c, http.StatusInternalServerError, "failed_to_get_vm", err.Error())
			return
		}
		if !vm.Serial {
			writeVMConsoleError(c, http.StatusConflict, "vm_serial_console_disabled", "vm_serial_console_disabled")
			return
		}

		allowed, guardErr := libvirtService.CanMutateProtectedVM(request.RID)
		if guardErr != nil {
			writeVMConsoleError(c, http.StatusServiceUnavailable, "vm_console_guard_unavailable", guardErr.Error())
			return
		}
		if !allowed {
			writeVMConsoleError(c, http.StatusForbidden, "replication_lease_not_owned", "replication_lease_not_owned")
			return
		}

		domain, err := libvirtService.GetLvDomain(request.RID)
		if err != nil {
			if isLibvirtDomainAbsent(err) {
				writeVMConsoleError(c, http.StatusConflict, "vm_domain_not_defined", "vm_domain_not_defined")
				return
			}
			writeVMConsoleError(c, http.StatusServiceUnavailable, "libvirt_connection_unavailable", err.Error())
			return
		}
		if domain == nil {
			writeVMConsoleError(c, http.StatusServiceUnavailable, "libvirt_connection_unavailable", "vm_domain_unavailable")
			return
		}
		if !vmDomainSupportsConsole(domain.Status) {
			writeVMConsoleError(c, http.StatusConflict, "vm_console_requires_running_vm", "vm_console_requires_running_vm")
			return
		}

		if _, err := vmConsoleDeviceStat(request.DevicePath); err != nil {
			writeVMConsoleError(c, http.StatusServiceUnavailable, "vm_serial_device_unavailable", err.Error())
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

		sessionID := "vm-console-" + request.RIDText
		session, err := GlobalVMSessionManager.GetOrCreateSession(
			sessionID,
			int(request.RID),
			request.BaudRate,
		)
		if err != nil {
			logger.L.Error().Err(err).Uint("rid", request.RID).Msg("failed to start VM serial console session")
			_ = conn.WriteMessage(websocket.TextMessage, []byte("VM serial console unavailable"))
			_ = conn.Close()
			return
		}

		observer := &VMObserver{Conn: conn}
		if err := session.AddObserverAndReplay(observer); err != nil {
			logger.L.Warn().Err(err).Str("session", sessionID).Msg("failed to join VM serial console session")
			session.RemoveObserver(observer, GlobalVMSessionManager)
			return
		}

		defer session.RemoveObserver(observer, GlobalVMSessionManager)

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

			default:
				logger.L.Warn().Uint8("control", data[0]).Str("session", sessionID).Msg("rejected unknown websocket control byte")
				return
			}
		}
	}
}
