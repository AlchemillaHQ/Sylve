// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

const (
	firewallCounterSampleInterval = 5 * time.Second
	firewallCounterFlushInterval  = 60 * time.Second
	firewallTelemetryRetention    = 7 * 24 * time.Hour
	firewallLiveHitRetention      = 6 * time.Hour
	firewallLiveHitMaxEntries     = 30000
	firewallLiveLogRestartDelay   = 5 * time.Second
	firewallLiveDefaultLimit      = 200
	firewallLiveMaxLimit          = 2000
)

var (
	pfVerboseRuleNumberPattern = regexp.MustCompile(`^@([0-9]+)\b`)
	firewallLogRulePattern     = regexp.MustCompile(`\brule\s+([0-9]+)\/([0-9]+)(?:\(([^)]*)\))?`)
	firewallLogIfacePattern    = regexp.MustCompile(`\bon\s+([^:\s]+):`)
	firewallLogActionPattern   = regexp.MustCompile(`\):\s*([a-z]+)(?:\s+([a-z]+))?\s+on\s+`)
	firewallLogLengthPattern   = regexp.MustCompile(`\blength\s+([0-9]+)\b`)
)

type firewallCounterKey struct {
	RuleType string
	RuleID   uint
}

type firewallTelemetryRuntime struct {
	mu sync.RWMutex

	counters  map[firewallCounterKey]trafficRuleCounterTotals
	persisted map[firewallCounterKey]trafficRuleCounterTotals

	ruleNames          map[firewallCounterKey]string
	trafficRuleNumbers map[int]uint
	natRuleNumbers     map[int]uint

	countersUpdatedAt time.Time
	countersAvailable bool
	countersError     string

	liveHits         []networkServiceInterfaces.FirewallLiveHitEvent
	liveCursor       int64
	liveSourceStatus string
	liveSourceError  string
	liveUpdatedAt    time.Time
}

type parsedFirewallLogLine struct {
	RuleNumber    int
	SubruleNumber int
	Ruleset       string
	Interface     string
	Action        string
	Direction     string
	Bytes         uint64
}

func newFirewallTelemetryRuntime() *firewallTelemetryRuntime {
	return &firewallTelemetryRuntime{
		counters:           make(map[firewallCounterKey]trafficRuleCounterTotals),
		persisted:          make(map[firewallCounterKey]trafficRuleCounterTotals),
		ruleNames:          make(map[firewallCounterKey]string),
		trafficRuleNumbers: make(map[int]uint),
		natRuleNumbers:     make(map[int]uint),
		liveHits:           make([]networkServiceInterfaces.FirewallLiveHitEvent, 0, 1024),
		liveSourceStatus:   "unavailable",
	}
}

func (s *Service) getFirewallTelemetryRuntime() *firewallTelemetryRuntime {
	s.firewallTelOnce.Do(func() {
		if s.firewallTelemetry == nil {
			s.firewallTelemetry = newFirewallTelemetryRuntime()
		}
	})
	return s.firewallTelemetry
}

func parseLabeledRuleCounters(output string, labelPattern *regexp.Regexp) (map[uint]trafficRuleCounterTotals, map[int]uint) {
	counters := make(map[uint]trafficRuleCounterTotals)
	ruleNumberToID := make(map[int]uint)
	currentRuleID := uint(0)
	currentRuleNumber := 0
	currentRuleNumberSet := false

	lines := strings.Split(output, "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		if ruleMatch := pfVerboseRuleNumberPattern.FindStringSubmatch(line); len(ruleMatch) == 2 {
			ruleNumber, err := strconv.Atoi(ruleMatch[1])
			if err != nil {
				currentRuleNumber = 0
				currentRuleNumberSet = false
			} else {
				currentRuleNumber = ruleNumber
				currentRuleNumberSet = true
			}
		}

		if strings.Contains(line, "label") || strings.Contains(line, "tag") {
			labelMatch := labelPattern.FindStringSubmatch(line)
			if len(labelMatch) >= 2 {
				idToken := ""
				for i := 1; i < len(labelMatch); i++ {
					if strings.TrimSpace(labelMatch[i]) != "" {
						idToken = labelMatch[i]
						break
					}
				}
				id64, err := strconv.ParseUint(idToken, 10, 64)
				if err == nil {
					currentRuleID = uint(id64)
					if currentRuleNumberSet {
						ruleNumberToID[currentRuleNumber] = currentRuleID
					}
				} else {
					currentRuleID = 0
				}
			} else {
				currentRuleID = 0
			}
		}

		if currentRuleID == 0 {
			continue
		}

		counterMatch := pfTrafficRuleCounterPattern.FindStringSubmatch(line)
		if len(counterMatch) != 3 {
			continue
		}

		packets, errPackets := strconv.ParseUint(counterMatch[1], 10, 64)
		bytes, errBytes := strconv.ParseUint(counterMatch[2], 10, 64)
		if errPackets != nil || errBytes != nil {
			continue
		}

		current := counters[currentRuleID]
		current.Packets += packets
		current.Bytes += bytes
		counters[currentRuleID] = current
	}

	return counters, ruleNumberToID
}

func (s *Service) loadFirewallRuleNames() (map[uint]string, map[uint]string, error) {
	traffic := make(map[uint]string)
	nat := make(map[uint]string)

	type row struct {
		ID   uint
		Name string
	}

	var trafficRows []row
	if err := s.DB.Model(&networkModels.FirewallTrafficRule{}).
		Select("id", "name").
		Find(&trafficRows).Error; err != nil {
		return nil, nil, err
	}
	for _, r := range trafficRows {
		traffic[r.ID] = strings.TrimSpace(r.Name)
	}

	var natRows []row
	if err := s.DB.Model(&networkModels.FirewallNATRule{}).
		Select("id", "name").
		Find(&natRows).Error; err != nil {
		return nil, nil, err
	}
	for _, r := range natRows {
		nat[r.ID] = strings.TrimSpace(r.Name)
	}

	return traffic, nat, nil
}

func (s *Service) sampleFirewallCounters() {
	runtime := s.getFirewallTelemetryRuntime()

	now := time.Now().UTC()
	trafficTotals, trafficRuleNumbers, trafficErr := s.collectTrafficCountersFromPF()
	natTotals, natRuleNumbers, natErr := s.collectNATCountersFromPF()

	trafficNames, natNames, namesErr := s.loadFirewallRuleNames()
	if namesErr != nil {
		logger.L.Warn().Err(namesErr).Msg("failed to load firewall rule names for telemetry")
	}

	combined := make(map[firewallCounterKey]trafficRuleCounterTotals, len(trafficTotals)+len(natTotals))
	for id, totals := range trafficTotals {
		combined[firewallCounterKey{RuleType: "traffic", RuleID: id}] = totals
	}
	for id, totals := range natTotals {
		combined[firewallCounterKey{RuleType: "nat", RuleID: id}] = totals
	}

	names := make(map[firewallCounterKey]string, len(trafficNames)+len(natNames))
	for id, name := range trafficNames {
		names[firewallCounterKey{RuleType: "traffic", RuleID: id}] = name
	}
	for id, name := range natNames {
		names[firewallCounterKey{RuleType: "nat", RuleID: id}] = name
	}

	availability := trafficErr == nil || natErr == nil
	errorsText := make([]string, 0, 2)
	if trafficErr != nil {
		errorsText = append(errorsText, fmt.Sprintf("traffic_counters: %v", trafficErr))
	}
	if natErr != nil {
		errorsText = append(errorsText, fmt.Sprintf("nat_counters: %v", natErr))
	}

	runtime.mu.Lock()
	runtime.counters = combined
	if trafficRuleNumbers != nil {
		runtime.trafficRuleNumbers = trafficRuleNumbers
	} else {
		runtime.trafficRuleNumbers = make(map[int]uint)
	}
	if natRuleNumbers != nil {
		runtime.natRuleNumbers = natRuleNumbers
	} else {
		runtime.natRuleNumbers = make(map[int]uint)
	}
	if len(names) > 0 {
		runtime.ruleNames = names
	}
	runtime.countersUpdatedAt = now
	runtime.countersAvailable = availability
	runtime.countersError = strings.Join(errorsText, "; ")
	runtime.mu.Unlock()

	if !availability {
		logger.L.Warn().Msgf("firewall counter sample unavailable: %s", strings.Join(errorsText, "; "))
	}
}

func (s *Service) flushFirewallCounterDeltas() {
	if s.TelemetryDB == nil {
		return
	}

	runtime := s.getFirewallTelemetryRuntime()

	runtime.mu.RLock()
	current := make(map[firewallCounterKey]trafficRuleCounterTotals, len(runtime.counters))
	for key, value := range runtime.counters {
		current[key] = value
	}
	previous := make(map[firewallCounterKey]trafficRuleCounterTotals, len(runtime.persisted))
	for key, value := range runtime.persisted {
		previous[key] = value
	}
	runtime.mu.RUnlock()

	nextPersisted := make(map[firewallCounterKey]trafficRuleCounterTotals, len(current))
	rows := make([]infoModels.FirewallRuleDelta, 0, len(current))
	for key, absolute := range current {
		prior := previous[key]

		packetsDelta := uint64(0)
		if absolute.Packets >= prior.Packets {
			packetsDelta = absolute.Packets - prior.Packets
		} else {
			packetsDelta = absolute.Packets
		}

		bytesDelta := uint64(0)
		if absolute.Bytes >= prior.Bytes {
			bytesDelta = absolute.Bytes - prior.Bytes
		} else {
			bytesDelta = absolute.Bytes
		}

		if packetsDelta > 0 || bytesDelta > 0 {
			rows = append(rows, infoModels.FirewallRuleDelta{
				RuleType:     key.RuleType,
				RuleID:       key.RuleID,
				PacketsDelta: packetsDelta,
				BytesDelta:   bytesDelta,
			})
		}
		nextPersisted[key] = absolute
	}

	if len(rows) > 0 {
		if err := s.TelemetryDB.CreateInBatches(&rows, 200).Error; err != nil {
			logger.L.Warn().Err(err).Msg("failed to persist firewall counter deltas")
			return
		}
	}

	cutoff := time.Now().UTC().Add(-firewallTelemetryRetention)
	if err := s.TelemetryDB.Where("created_at < ?", cutoff).Delete(&infoModels.FirewallRuleDelta{}).Error; err != nil {
		logger.L.Warn().Err(err).Msg("failed to prune firewall telemetry deltas")
	}

	runtime.mu.Lock()
	runtime.persisted = nextPersisted
	runtime.mu.Unlock()
}

func parseFirewallLogLine(line string) (parsedFirewallLogLine, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return parsedFirewallLogLine{}, false
	}

	ruleMatch := firewallLogRulePattern.FindStringSubmatch(trimmed)
	if len(ruleMatch) < 3 {
		return parsedFirewallLogLine{}, false
	}
	ruleNumber, err := strconv.Atoi(ruleMatch[1])
	if err != nil {
		return parsedFirewallLogLine{}, false
	}

	parsed := parsedFirewallLogLine{
		RuleNumber:    ruleNumber,
		SubruleNumber: -1,
	}
	if len(ruleMatch) >= 3 {
		subruleNumber, convErr := strconv.Atoi(ruleMatch[2])
		if convErr == nil {
			parsed.SubruleNumber = subruleNumber
		}
	}
	if len(ruleMatch) >= 4 {
		parsed.Ruleset = strings.ToLower(strings.TrimSpace(ruleMatch[3]))
	}

	if ifaceMatch := firewallLogIfacePattern.FindStringSubmatch(trimmed); len(ifaceMatch) == 2 {
		parsed.Interface = ifaceMatch[1]
	}
	if actionMatch := firewallLogActionPattern.FindStringSubmatch(trimmed); len(actionMatch) >= 2 {
		parsed.Action = strings.TrimSpace(strings.ToLower(actionMatch[1]))
		if len(actionMatch) >= 3 {
			direction := strings.TrimSpace(strings.ToLower(actionMatch[2]))
			if direction == "in" || direction == "out" {
				parsed.Direction = direction
			}
		}
	}
	if lengthMatch := firewallLogLengthPattern.FindStringSubmatch(trimmed); len(lengthMatch) == 2 {
		n, convErr := strconv.ParseUint(lengthMatch[1], 10, 64)
		if convErr == nil {
			parsed.Bytes = n
		}
	}

	return parsed, true
}

func resolveRuleIDByNumbers(mapping map[int]uint, primary int, secondary int) (uint, bool) {
	if primary >= 0 {
		if ruleID, ok := mapping[primary]; ok {
			return ruleID, true
		}
	}
	if secondary >= 0 && secondary != primary {
		if ruleID, ok := mapping[secondary]; ok {
			return ruleID, true
		}
	}
	return 0, false
}

func isNATAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "nat", "rdr", "binat":
		return true
	default:
		return false
	}
}

func (s *Service) resolveFirewallRuleReference(parsed parsedFirewallLogLine) (string, uint, string, bool) {
	runtime := s.getFirewallTelemetryRuntime()
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()

	ruleset := strings.ToLower(strings.TrimSpace(parsed.Ruleset))
	isTrafficRuleset := strings.Contains(ruleset, "traffic-rules")
	isNATRuleset := strings.Contains(ruleset, "nat-rules")

	if isTrafficRuleset {
		if ruleID, ok := resolveRuleIDByNumbers(runtime.trafficRuleNumbers, parsed.SubruleNumber, parsed.RuleNumber); ok {
			key := firewallCounterKey{RuleType: "traffic", RuleID: ruleID}
			return "traffic", ruleID, runtime.ruleNames[key], true
		}
		return "", 0, "", false
	}
	if isNATRuleset {
		if ruleID, ok := resolveRuleIDByNumbers(runtime.natRuleNumbers, parsed.SubruleNumber, parsed.RuleNumber); ok {
			key := firewallCounterKey{RuleType: "nat", RuleID: ruleID}
			return "nat", ruleID, runtime.ruleNames[key], true
		}
		return "", 0, "", false
	}

	// If ruleset metadata is unavailable, use action semantics to choose the safer default.
	// Translation actions should prefer nat-rule mapping over filter-rule mapping.
	if isNATAction(parsed.Action) {
		if ruleID, ok := resolveRuleIDByNumbers(runtime.natRuleNumbers, parsed.RuleNumber, parsed.SubruleNumber); ok {
			key := firewallCounterKey{RuleType: "nat", RuleID: ruleID}
			return "nat", ruleID, runtime.ruleNames[key], true
		}
		if ruleID, ok := resolveRuleIDByNumbers(runtime.trafficRuleNumbers, parsed.RuleNumber, parsed.SubruleNumber); ok {
			key := firewallCounterKey{RuleType: "traffic", RuleID: ruleID}
			return "traffic", ruleID, runtime.ruleNames[key], true
		}
		return "", 0, "", false
	}

	if ruleID, ok := resolveRuleIDByNumbers(runtime.trafficRuleNumbers, parsed.RuleNumber, parsed.SubruleNumber); ok {
		key := firewallCounterKey{RuleType: "traffic", RuleID: ruleID}
		return "traffic", ruleID, runtime.ruleNames[key], true
	}
	if ruleID, ok := resolveRuleIDByNumbers(runtime.natRuleNumbers, parsed.RuleNumber, parsed.SubruleNumber); ok {
		key := firewallCounterKey{RuleType: "nat", RuleID: ruleID}
		return "nat", ruleID, runtime.ruleNames[key], true
	}

	return "", 0, "", false
}

func normalizeNullTerminatedBytes(in []byte) string {
	return strings.TrimSpace(string(bytes.TrimRight(in, "\x00")))
}

func pfLogActionName(action uint8) string {
	switch action {
	case 0:
		return "pass"
	case 1:
		return "block"
	case 2:
		return "scrub"
	case 4:
		return "nat"
	case 6:
		return "binat"
	case 8:
		return "rdr"
	default:
		return fmt.Sprintf("action_%d", action)
	}
}

func pfLogDirectionName(direction layers.PFDirection) string {
	switch direction {
	case layers.PFDirectionIn:
		return "in"
	case layers.PFDirectionOut:
		return "out"
	default:
		return ""
	}
}

func parsePFLogPacket(packet gopacket.Packet) (parsedFirewallLogLine, string, bool) {
	layer := packet.Layer(layers.LayerTypePFLog)
	if layer == nil {
		return parsedFirewallLogLine{}, "", false
	}

	pfLog, ok := layer.(*layers.PFLog)
	if !ok {
		return parsedFirewallLogLine{}, "", false
	}

	bytesCount := uint64(packet.Metadata().CaptureInfo.Length)
	if bytesCount == 0 {
		bytesCount = uint64(len(packet.Data()))
	}

	parsed := parsedFirewallLogLine{
		RuleNumber:    int(pfLog.RuleNum),
		SubruleNumber: int(pfLog.SubruleNum),
		Ruleset:       strings.ToLower(strings.TrimSpace(normalizeNullTerminatedBytes(pfLog.Ruleset))),
		Interface:     normalizeNullTerminatedBytes(pfLog.IFName),
		Action:        strings.ToLower(strings.TrimSpace(pfLogActionName(pfLog.Action))),
		Direction:     strings.ToLower(strings.TrimSpace(pfLogDirectionName(pfLog.Direction))),
		Bytes:         bytesCount,
	}

	ruleset := strings.TrimSpace(parsed.Ruleset)
	rawLine := fmt.Sprintf(
		"rule %d/%d(%s): %s %s on %s length %d",
		pfLog.RuleNum,
		pfLog.SubruleNum,
		ruleset,
		parsed.Action,
		parsed.Direction,
		parsed.Interface,
		parsed.Bytes,
	)

	return parsed, strings.TrimSpace(rawLine), true
}

func (s *Service) ingestFirewallLiveHitParsed(parsed parsedFirewallLogLine, rawLine string) {
	ruleType, ruleID, ruleName, ok := s.resolveFirewallRuleReference(parsed)
	if !ok || ruleID == 0 {
		return
	}

	now := time.Now().UTC()
	runtime := s.getFirewallTelemetryRuntime()
	runtime.mu.Lock()
	runtime.liveCursor++
	event := networkServiceInterfaces.FirewallLiveHitEvent{
		Cursor:    runtime.liveCursor,
		Timestamp: now,
		RuleType:  ruleType,
		RuleID:    ruleID,
		RuleName:  ruleName,
		Action:    parsed.Action,
		Direction: parsed.Direction,
		Interface: parsed.Interface,
		Bytes:     parsed.Bytes,
		RawLine:   strings.TrimSpace(rawLine),
	}
	runtime.liveHits = append(runtime.liveHits, event)

	cutoff := now.Add(-firewallLiveHitRetention)
	pruneIndex := 0
	for pruneIndex < len(runtime.liveHits) {
		if runtime.liveHits[pruneIndex].Timestamp.After(cutoff) {
			break
		}
		pruneIndex++
	}

	dropCount := pruneIndex
	if overflow := len(runtime.liveHits) - firewallLiveHitMaxEntries; overflow > dropCount {
		dropCount = overflow
	}
	if dropCount > 0 {
		copy(runtime.liveHits, runtime.liveHits[dropCount:])
		runtime.liveHits = runtime.liveHits[:len(runtime.liveHits)-dropCount]
	}
	runtime.liveUpdatedAt = now
	runtime.mu.Unlock()
}

func (s *Service) ingestFirewallLiveHit(line string) {
	parsed, ok := parseFirewallLogLine(line)
	if !ok {
		return
	}
	s.ingestFirewallLiveHitParsed(parsed, line)
}

func (s *Service) ingestFirewallLivePacket(packet gopacket.Packet) {
	parsed, rawLine, ok := parsePFLogPacket(packet)
	if !ok {
		return
	}
	s.ingestFirewallLiveHitParsed(parsed, rawLine)
}

func (s *Service) setFirewallLiveSourceStatus(status string, sourceError string) {
	runtime := s.getFirewallTelemetryRuntime()
	runtime.mu.Lock()
	runtime.liveSourceStatus = status
	runtime.liveSourceError = strings.TrimSpace(sourceError)
	if status == "ok" {
		runtime.liveUpdatedAt = time.Now().UTC()
	}
	runtime.mu.Unlock()
}

func isPflogAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	lowered := strings.ToLower(err.Error())
	return strings.Contains(lowered, "file exists") ||
		strings.Contains(lowered, "already exists") ||
		strings.Contains(lowered, "exists")
}

func (s *Service) ensurePFLogInterfaceReady() error {
	if _, err := firewallRunCommand("/sbin/ifconfig", "pflog0"); err == nil {
		return nil
	}

	if _, err := firewallRunCommand("/sbin/ifconfig", "pflog0", "create"); err != nil && !isPflogAlreadyExistsError(err) {
		if _, checkErr := firewallRunCommand("/sbin/ifconfig", "pflog0"); checkErr == nil {
			return nil
		}
		return fmt.Errorf("failed_to_prepare_pflog_interface: %w", err)
	}

	if _, err := firewallRunCommand("/sbin/ifconfig", "pflog0"); err != nil {
		return fmt.Errorf("failed_to_verify_pflog_interface: %w", err)
	}

	return nil
}

func (s *Service) runFirewallLogWatcher(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := s.ensurePFLogInterfaceReady(); err != nil {
			s.setFirewallLiveSourceStatus("unavailable", err.Error())
			logger.L.Warn().Err(err).Msg("failed to prepare pflog interface for capture")
			select {
			case <-ctx.Done():
				return
			case <-time.After(firewallLiveLogRestartDelay):
			}
			continue
		}

		ih, err := pcap.NewInactiveHandle("pflog0")
		if err != nil {
			s.setFirewallLiveSourceStatus("unavailable", err.Error())
			logger.L.Warn().Err(err).Msg("failed to create pflog capture handle")
			select {
			case <-ctx.Done():
				return
			case <-time.After(firewallLiveLogRestartDelay):
			}
			continue
		}
		_ = ih.SetSnapLen(262144)
		_ = ih.SetPromisc(false)
		_ = ih.SetTimeout(500 * time.Millisecond)
		_ = ih.SetBufferSize(4 << 20)
		_ = ih.SetImmediateMode(true)

		handle, err := ih.Activate()
		ih.CleanUp()
		if err != nil {
			s.setFirewallLiveSourceStatus("unavailable", err.Error())
			logger.L.Warn().Err(err).Msg("failed to activate pflog capture handle")
			select {
			case <-ctx.Done():
				return
			case <-time.After(firewallLiveLogRestartDelay):
			}
			continue
		}

		s.setFirewallLiveSourceStatus("ok", "")

		packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
		packetSource.Lazy = true
		packetSource.NoCopy = true
		packetCh := packetSource.Packets()

		errText := "pflog_capture_closed"
	captureLoop:
		for {
			select {
			case <-ctx.Done():
				handle.Close()
				return
			case packet, ok := <-packetCh:
				if !ok {
					break captureLoop
				}
				s.ingestFirewallLivePacket(packet)
			}
		}
		handle.Close()
		if ctx.Err() != nil {
			return
		}

		s.setFirewallLiveSourceStatus("unavailable", errText)
		logger.L.Warn().Msgf("firewall live log watcher unavailable: %s", errText)

		select {
		case <-ctx.Done():
			return
		case <-time.After(firewallLiveLogRestartDelay):
		}
	}
}

func (s *Service) runFirewallCounterSampler(ctx context.Context) {
	s.sampleFirewallCounters()

	ticker := time.NewTicker(firewallCounterSampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sampleFirewallCounters()
		}
	}
}

func (s *Service) runFirewallCounterFlusher(ctx context.Context) {
	ticker := time.NewTicker(firewallCounterFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.flushFirewallCounterDeltas()
		}
	}
}

func (s *Service) StartFirewallMonitor(ctx context.Context) {
	_ = s.getFirewallTelemetryRuntime()

	s.firewallMonOnce.Do(func() {
		go s.runFirewallCounterSampler(ctx)
		go s.runFirewallCounterFlusher(ctx)
		go s.runFirewallLogWatcher(ctx)
	})
}

func (s *Service) getCounterSnapshotByType(ruleType string) (map[uint]trafficRuleCounterTotals, time.Time, bool) {
	runtime := s.getFirewallTelemetryRuntime()
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()

	out := make(map[uint]trafficRuleCounterTotals)
	for key, totals := range runtime.counters {
		if key.RuleType != ruleType {
			continue
		}
		out[key.RuleID] = totals
	}

	return out, runtime.countersUpdatedAt, runtime.countersAvailable
}

func (s *Service) fallbackTrafficCounters() map[uint]trafficRuleCounterTotals {
	totals, _, err := s.collectTrafficCountersFromPF()
	if err != nil {
		logger.L.Warn().Err(err).Msg("failed to collect pf traffic counters")
		return map[uint]trafficRuleCounterTotals{}
	}
	return totals
}

func (s *Service) fallbackNATCounters() map[uint]trafficRuleCounterTotals {
	totals, _, err := s.collectNATCountersFromPF()
	if err != nil {
		logger.L.Warn().Err(err).Msg("failed to collect pf nat counters")
		return map[uint]trafficRuleCounterTotals{}
	}
	return totals
}

func (s *Service) GetFirewallNATRuleCounters() ([]networkServiceInterfaces.FirewallNATRuleCounter, error) {
	rules, err := s.GetFirewallNATRules()
	if err != nil {
		return nil, err
	}

	if len(rules) == 0 {
		return []networkServiceInterfaces.FirewallNATRuleCounter{}, nil
	}

	totalsByRuleID, updatedAt, available := s.getCounterSnapshotByType("nat")
	if !available || updatedAt.IsZero() {
		totalsByRuleID = s.fallbackNATCounters()
		updatedAt = time.Now().UTC()
	}

	counters := make([]networkServiceInterfaces.FirewallNATRuleCounter, 0, len(rules))
	for _, rule := range rules {
		totals := totalsByRuleID[rule.ID]
		counters = append(counters, networkServiceInterfaces.FirewallNATRuleCounter{
			ID:        rule.ID,
			Packets:   totals.Packets,
			Bytes:     totals.Bytes,
			UpdatedAt: updatedAt,
		})
	}

	return counters, nil
}

func firewallLiveHitMatchesFilter(
	item networkServiceInterfaces.FirewallLiveHitEvent,
	filter *networkServiceInterfaces.FirewallLiveHitsFilter,
) bool {
	if filter == nil {
		return true
	}

	if filter.RuleType != "" && !strings.EqualFold(item.RuleType, filter.RuleType) {
		return false
	}
	if filter.RuleID != nil && item.RuleID != *filter.RuleID {
		return false
	}
	if filter.Action != "" && !strings.EqualFold(strings.TrimSpace(item.Action), filter.Action) {
		return false
	}
	if filter.Direction != "" && !strings.EqualFold(strings.TrimSpace(item.Direction), filter.Direction) {
		return false
	}
	if filter.Interface != "" && !strings.EqualFold(strings.TrimSpace(item.Interface), filter.Interface) {
		return false
	}

	if filter.Query != "" {
		q := strings.ToLower(strings.TrimSpace(filter.Query))
		if q != "" {
			rawLine := strings.ToLower(item.RawLine)
			ruleName := strings.ToLower(item.RuleName)
			ruleID := strconv.FormatUint(uint64(item.RuleID), 10)
			if !strings.Contains(rawLine, q) && !strings.Contains(ruleName, q) && !strings.Contains(ruleID, q) {
				return false
			}
		}
	}

	return true
}

func (s *Service) GetFirewallLiveHits(cursor int64, limit int, filter *networkServiceInterfaces.FirewallLiveHitsFilter) (*networkServiceInterfaces.FirewallLiveHitsResponse, error) {
	if limit <= 0 {
		limit = firewallLiveDefaultLimit
	}
	if limit > firewallLiveMaxLimit {
		limit = firewallLiveMaxLimit
	}

	runtime := s.getFirewallTelemetryRuntime()
	runtime.mu.RLock()
	items := make([]networkServiceInterfaces.FirewallLiveHitEvent, 0, limit)
	// For initial "live view opened" requests, bootstrap the caller to current cursor
	// without replaying historical entries.
	if cursor > 0 && len(runtime.liveHits) > 0 {
		// Cursors are monotonic, so binary search avoids scanning the full ring.
		start := sort.Search(len(runtime.liveHits), func(i int) bool {
			return runtime.liveHits[i].Cursor > cursor
		})

		for i := start; i < len(runtime.liveHits); i++ {
			item := runtime.liveHits[i]
			if !firewallLiveHitMatchesFilter(item, filter) {
				continue
			}
			items = append(items, item)
			if len(items) >= limit {
				break
			}
		}
	}

	nextCursor := cursor
	if len(items) > 0 {
		nextCursor = items[len(items)-1].Cursor
	} else if runtime.liveCursor > nextCursor {
		nextCursor = runtime.liveCursor
	}

	updatedAt := runtime.liveUpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	resp := &networkServiceInterfaces.FirewallLiveHitsResponse{
		Items:        items,
		NextCursor:   nextCursor,
		SourceStatus: runtime.liveSourceStatus,
		SourceError:  runtime.liveSourceError,
		UpdatedAt:    updatedAt,
	}
	runtime.mu.RUnlock()

	if strings.TrimSpace(resp.SourceStatus) == "" {
		resp.SourceStatus = "unavailable"
	}

	return resp, nil
}
