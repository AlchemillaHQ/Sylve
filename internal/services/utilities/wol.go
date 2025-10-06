// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"net"
	"sync"
	"time"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

var lastSeen = make(map[string]time.Time)
var dedupWindow = time.Second

func shouldEmit(payload []byte) bool {
	key := string(payload)
	now := time.Now()
	if t, ok := lastSeen[key]; ok {
		if now.Sub(t) < dedupWindow {
			return false
		}
	}
	lastSeen[key] = now
	return true
}

func (s *Service) StartWOLServer() error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}

	const bpf = "(udp and (port 7 or port 9)) or (ether proto 0x0842) or (vlan and ether proto 0x0842)"

	var wg sync.WaitGroup

	for _, iface := range ifaces {
		if (iface.Flags&net.FlagUp) == 0 ||
			(iface.Flags&net.FlagLoopback) != 0 ||
			(iface.Flags&net.FlagBroadcast) == 0 {
			continue
		}

		ih, err := pcap.NewInactiveHandle(iface.Name)
		if err != nil {
			logger.L.Warn().Err(err).Str("iface", iface.Name).Msg("inactive handle create failed")
			continue
		}

		_ = ih.SetSnapLen(256)
		_ = ih.SetPromisc(false)
		_ = ih.SetTimeout(500 * time.Millisecond)
		_ = ih.SetBufferSize(4 << 20)
		_ = ih.SetImmediateMode(false)

		handle, err := ih.Activate()
		ih.CleanUp()
		if err != nil {
			logger.L.Warn().Err(err).Str("iface", iface.Name).Msg("pcap activate failed")
			continue
		}

		if err := handle.SetBPFFilter(bpf); err != nil {
			logger.L.Warn().Err(err).Str("iface", iface.Name).Msg("set BPF failed")
			handle.Close()
			continue
		}

		logger.L.Debug().Str("iface", iface.Name).Msg("WOL capture started")

		wg.Add(1)
		go func(h *pcap.Handle, ifname string) {
			defer wg.Done()
			defer h.Close()

			src := gopacket.NewPacketSource(h, h.LinkType())
			src.Lazy = true
			src.NoCopy = true

			for pkt := range src.Packets() {
				var payload []byte

				if udpLayer := pkt.Layer(layers.LayerTypeUDP); udpLayer != nil {
					udp := udpLayer.(*layers.UDP)
					if udp.DstPort != 7 && udp.DstPort != 9 {
						goto next
					}
					payload = udp.Payload
				} else {
					if ethL := pkt.Layer(layers.LayerTypeEthernet); ethL != nil {
						eth := ethL.(*layers.Ethernet)
						switch eth.EthernetType {
						case layers.EthernetType(0x0842):
							payload = eth.Payload
						case layers.EthernetTypeDot1Q:
							if d1qL := pkt.Layer(layers.LayerTypeDot1Q); d1qL != nil {
								d1q := d1qL.(*layers.Dot1Q)
								if d1q.Type == layers.EthernetType(0x0842) {
									payload = d1q.Payload
								}
							}
						}
					}
				}

				if len(payload) == 0 {
					goto next
				}

				if utils.IsWOLPacket(payload) && shouldEmit(payload) {
					mac := utils.FormatMAC(payload[6:12])

					if err := s.DB.Create(&utilitiesModels.WoL{
						Mac:    mac,
						Status: "pending",
					}).Error; err != nil {
						logger.L.Warn().Err(err).Str("mac", mac).Str("iface", ifname).Msg("db insert failed")
					} else {
						logger.L.Debug().Msgf("⚡ WOL packet detected on %s for MAC: %s", ifname, mac)
					}
				}

			next:
				continue
			}
		}(handle, iface.Name)
	}

	logger.L.Info().Msg("WoL server started")
	select {}
}
