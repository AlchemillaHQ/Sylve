// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package metrics

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewHandler(cfg internal.MetricsConfig, sources Sources) http.Handler {
	if !cfg.Enabled {
		return http.NotFoundHandler()
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(&collector{sources})
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{MaxRequestsInFlight: 4})
	expected := sha256.Sum256([]byte(cfg.Token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if cfg.Token == "" {
			handler.ServeHTTP(w, r)
			return
		}
		values := r.Header.Values("Authorization")
		if len(values) == 1 {
			parts := strings.Fields(values[0])
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				provided := sha256.Sum256([]byte(parts[1]))
				if subtle.ConstantTimeCompare(provided[:], expected[:]) == 1 {
					handler.ServeHTTP(w, r)
					return
				}
			}
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}
