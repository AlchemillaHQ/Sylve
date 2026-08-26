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
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	vmWSReadLimit          = 64 << 10
	vmWSWriteTimeout       = 10 * time.Second
	vmWSPongWait           = 60 * time.Second
	vmWSPingPeriod         = (vmWSPongWait * 9) / 10
	vmControlInput    byte = 0
	vmControlResize   byte = 1
	vmControlTakeover byte = 2
)

type vmConsoleService interface {
	GetVMByRID(rid uint) (vmModels.VM, error)
	CanMutateProtectedVM(rid uint) (bool, error)
	GetLvDomain(rid uint) (*libvirtServiceInterfaces.LvDomain, error)
}

type vmConsoleRequest = libvirtService.VMSerialConsoleRequest

var vmConsoleDeviceStat = os.Stat

func parseVMConsoleRequest(ridText, baudRate string) (vmConsoleRequest, error) {
	return libvirtService.ParseVMSerialConsoleRequest(ridText, baudRate)
}

type VMObserver struct {
	Conn                 *websocket.Conn
	Username             string
	JoinSequence         uint64
	Mu                   sync.Mutex
	writeMessageOverride func(messageType int, payload []byte) error
	closeOverride        func() error
}

func (o *VMObserver) WriteMessage(messageType int, payload []byte) error {
	o.Mu.Lock()
	defer o.Mu.Unlock()
	return o.writeMessageLocked(messageType, payload)
}

func (o *VMObserver) writeMessageLocked(messageType int, payload []byte) error {
	if o.writeMessageOverride != nil {
		return o.writeMessageOverride(messageType, payload)
	}
	if o.Conn == nil {
		return errors.New("observer websocket is unavailable")
	}

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
	if o == nil {
		return nil
	}

	o.Mu.Lock()
	defer o.Mu.Unlock()
	if o.closeOverride != nil {
		return o.closeOverride()
	}
	if o.Conn == nil {
		return nil
	}
	return o.Conn.Close()
}

type VMSession struct {
	ID               string
	BaudRate         string
	Cmd              *exec.Cmd
	Pty              *os.File
	Observers        map[*VMObserver]struct{}
	Controller       *VMObserver
	NextJoinSequence uint64
	Mu               sync.Mutex
	closeOnce        sync.Once
	closed           bool
	History          []byte
	HistoryLimit     int
}

type vmConsoleControlState struct {
	Type               string `json:"type"`
	HasControl         bool   `json:"hasControl"`
	ControllerUsername string `json:"controllerUsername"`
	ObserverCount      int    `json:"observerCount"`
}

type vmConsoleControlMessage struct {
	Observer *VMObserver
	Payload  []byte
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
		ts.Controller = nil

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

func (ts *VMSession) controlStateLocked(obs *VMObserver) vmConsoleControlState {
	controllerUsername := ""
	if ts.Controller != nil {
		controllerUsername = ts.Controller.Username
	}

	return vmConsoleControlState{
		Type:               "control-state",
		HasControl:         ts.Controller == obs,
		ControllerUsername: controllerUsername,
		ObserverCount:      len(ts.Observers),
	}
}

func (ts *VMSession) controlMessages(skip *VMObserver) []vmConsoleControlMessage {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()

	if ts.closed {
		return nil
	}

	messages := make([]vmConsoleControlMessage, 0, len(ts.Observers))
	for obs := range ts.Observers {
		if obs == skip {
			continue
		}

		payload, err := json.Marshal(ts.controlStateLocked(obs))
		if err != nil {
			logger.L.Error().Err(err).Str("session", ts.ID).Msg("failed to encode VM console control state")
			continue
		}
		messages = append(messages, vmConsoleControlMessage{Observer: obs, Payload: payload})
	}

	return messages
}

func (ts *VMSession) BroadcastControlState(sm *VMSessionManager, skip *VMObserver) {
	messages := ts.controlMessages(skip)
	failed := make([]*VMObserver, 0)

	for _, message := range messages {
		if err := message.Observer.WriteMessage(websocket.TextMessage, message.Payload); err != nil {
			logger.L.Warn().Err(err).Str("session", ts.ID).Msg("failed to write VM console control state to websocket")
			failed = append(failed, message.Observer)
		}
	}

	for _, obs := range failed {
		ts.RemoveObserver(obs, sm)
	}
}

func (ts *VMSession) SendControlState(obs *VMObserver) error {
	ts.Mu.Lock()
	if ts.closed {
		ts.Mu.Unlock()
		return errors.New("session is closed")
	}
	if _, exists := ts.Observers[obs]; !exists {
		ts.Mu.Unlock()
		return errors.New("observer is not attached")
	}
	state := ts.controlStateLocked(obs)
	ts.Mu.Unlock()

	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return obs.WriteMessage(websocket.TextMessage, payload)
}

func (ts *VMSession) AddObserverAndReplay(obs *VMObserver, sm *VMSessionManager) error {
	obs.Mu.Lock()

	ts.Mu.Lock()
	if ts.closed {
		ts.Mu.Unlock()
		obs.Mu.Unlock()
		return errors.New("session is closed")
	}

	ts.NextJoinSequence++
	obs.JoinSequence = ts.NextJoinSequence
	ts.Observers[obs] = struct{}{}
	if ts.Controller == nil {
		ts.Controller = obs
	}

	history := append([]byte(nil), ts.History...)
	controlPayload, err := json.Marshal(ts.controlStateLocked(obs))
	ts.Mu.Unlock()
	if err != nil {
		obs.Mu.Unlock()
		return err
	}

	if err := obs.writeMessageLocked(websocket.TextMessage, controlPayload); err != nil {
		obs.Mu.Unlock()
		return err
	}
	if len(history) > 0 {
		if err := obs.writeMessageLocked(websocket.BinaryMessage, history); err != nil {
			obs.Mu.Unlock()
			return err
		}
	}
	obs.Mu.Unlock()

	ts.BroadcastControlState(sm, obs)
	return nil
}

func (ts *VMSession) RemoveObserver(obs *VMObserver, sm *VMSessionManager) {
	ts.Mu.Lock()
	if _, exists := ts.Observers[obs]; !exists {
		ts.Mu.Unlock()
		_ = obs.Close()
		return
	}

	delete(ts.Observers, obs)
	if ts.Controller == obs {
		ts.Controller = nil
		for candidate := range ts.Observers {
			if ts.Controller == nil || candidate.JoinSequence < ts.Controller.JoinSequence {
				ts.Controller = candidate
			}
		}
	}

	closeSession := !ts.closed && len(ts.Observers) == 0
	if closeSession {
		ts.closed = true
		ts.Controller = nil
	}
	ts.Mu.Unlock()

	_ = obs.Close()
	if closeSession {
		ts.Close(sm)
		return
	}

	ts.BroadcastControlState(sm, nil)
}

func (ts *VMSession) TakeControl(obs *VMObserver, sm *VMSessionManager) bool {
	ts.Mu.Lock()
	if ts.closed {
		ts.Mu.Unlock()
		return false
	}
	if _, exists := ts.Observers[obs]; !exists {
		ts.Mu.Unlock()
		return false
	}

	previous := ts.Controller
	ts.Controller = obs
	ts.Mu.Unlock()

	if previous != obs {
		previousUsername := ""
		if previous != nil {
			previousUsername = previous.Username
		}
		logger.L.Info().
			Str("session", ts.ID).
			Str("previous_controller", previousUsername).
			Str("controller", obs.Username).
			Msg("VM serial console control transferred")
	}

	ts.BroadcastControlState(sm, nil)
	return true
}

func (ts *VMSession) RunControllerAction(obs *VMObserver, action func(*os.File) error) (bool, error) {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()

	if ts.closed || ts.Controller != obs {
		return false, nil
	}
	return true, action(ts.Pty)
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

func vmConsoleErrorStatus(code string) int {
	switch code {
	case "invalid_rid_format", "invalid_baud_rate", "invalid_console_request":
		return http.StatusBadRequest
	case "vm_not_found":
		return http.StatusNotFound
	case "replication_lease_not_owned":
		return http.StatusForbidden
	case "vm_serial_console_disabled", "vm_domain_not_defined", "vm_console_requires_running_vm":
		return http.StatusConflict
	case "vm_console_guard_unavailable", "libvirt_connection_unavailable", "vm_serial_device_unavailable", "vm_service_unavailable":
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
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
func HandleLibvirtTerminalWebsocket(service vmConsoleService) gin.HandlerFunc {
	return func(c *gin.Context) {
		request, err := parseVMConsoleRequest(c.Param("rid"), c.Query("baudrate"))
		if err != nil {
			var validationErr *libvirtService.VMSerialConsoleError
			if errors.As(err, &validationErr) {
				writeVMConsoleError(c, http.StatusBadRequest, validationErr.Code, validationErr.Detail)
				return
			}
			writeVMConsoleError(c, http.StatusBadRequest, "invalid_console_request", err.Error())
			return
		}

		_, err = libvirtService.PreflightVMSerialConsole(service, request, vmConsoleDeviceStat)
		if err != nil {
			code := "vm_console_preflight_failed"
			detail := err.Error()
			var consoleErr *libvirtService.VMSerialConsoleError
			if errors.As(err, &consoleErr) {
				code = consoleErr.Code
				if strings.TrimSpace(consoleErr.Detail) != "" {
					detail = consoleErr.Detail
				}
			}
			writeVMConsoleError(c, vmConsoleErrorStatus(code), code, detail)
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

		sessionID := "vm-console-" + strconv.FormatUint(uint64(request.RID), 10)
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

		username := strings.TrimSpace(c.GetString("Username"))
		if username == "" {
			username = "unknown"
		}

		observer := &VMObserver{Conn: conn, Username: username}
		if err := session.AddObserverAndReplay(observer, GlobalVMSessionManager); err != nil {
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

				allowed, err := session.RunControllerAction(observer, func(ptymx *os.File) error {
					_, err := ptymx.Write(data[1:])
					return err
				})
				if err != nil {
					logger.L.Warn().Err(err).Str("session", sessionID).Msg("failed to write serial input to PTY")
					return
				}
				if !allowed {
					if err := session.SendControlState(observer); err != nil {
						return
					}
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

				allowed, err := session.RunControllerAction(observer, func(ptymx *os.File) error {
					return pty.Setsize(ptymx, &pty.Winsize{
						Rows: ws.Rows,
						Cols: ws.Cols,
						X:    ws.X,
						Y:    ws.Y,
					})
				})
				if err != nil {
					logger.L.Warn().Err(err).Str("session", sessionID).Msg("failed to resize PTY")
				}
				if !allowed {
					if err := session.SendControlState(observer); err != nil {
						return
					}
				}

			case vmControlTakeover:
				if len(data) != 1 {
					logger.L.Warn().Str("session", sessionID).Msg("ignored VM console takeover payload with unexpected data")
					continue
				}
				if !session.TakeControl(observer, GlobalVMSessionManager) {
					return
				}

			default:
				logger.L.Warn().Uint8("control", data[0]).Str("session", sessionID).Msg("rejected unknown websocket control byte")
				return
			}
		}
	}
}
