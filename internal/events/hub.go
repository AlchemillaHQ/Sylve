// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// under sponsorship from Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>.

package events

import (
	"sync"
	"time"
)

type Event struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
}

type Hub struct {
	mu      sync.RWMutex
	nextID  int
	clients map[int]chan Event
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[int]chan Event),
	}
}

func (h *Hub) Subscribe() (chan Event, func()) {
	h.mu.Lock()

	id := h.nextID
	h.nextID++

	ch := make(chan Event, 16)
	h.clients[id] = ch

	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if existing, ok := h.clients[id]; ok {
			delete(h.clients, id)
			close(existing)
		}
		h.mu.Unlock()
	}

	return ch, unsubscribe
}

func (h *Hub) Publish(evt Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.clients {
		select {
		case ch <- evt:
		default:
		}
	}
}

var SSE = NewHub()
