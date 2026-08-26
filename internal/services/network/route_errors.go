// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import "errors"

const (
	MaxStaticRouteNameBytes        = 128
	MaxStaticRouteDescriptionBytes = 2048
	MaxStaticRouteAddressBytes     = 256
	MaxStaticRouteInterfaceBytes   = 64
)

var (
	ErrInvalidStaticRoute               = errors.New("invalid static route")
	ErrStaticRouteNotFound              = errors.New("static route not found")
	ErrStaticRouteConflict              = errors.New("static route conflict")
	ErrStaticRouteSuggestionUnavailable = errors.New("static route suggestion unavailable")
)

func invalidStaticRoute(cause error) error {
	if cause == nil || errors.Is(cause, ErrInvalidStaticRoute) {
		return ErrInvalidStaticRoute
	}
	return errors.Join(ErrInvalidStaticRoute, cause)
}

func staticRouteNotFound(cause error) error {
	if cause == nil {
		return ErrStaticRouteNotFound
	}
	return errors.Join(ErrStaticRouteNotFound, cause)
}

func staticRouteConflict(cause error) error {
	if cause == nil {
		return ErrStaticRouteConflict
	}
	return errors.Join(ErrStaticRouteConflict, cause)
}

func staticRouteSuggestionUnavailable(cause error) error {
	if cause == nil {
		return ErrStaticRouteSuggestionUnavailable
	}
	return errors.Join(ErrStaticRouteSuggestionUnavailable, cause)
}

// StaticRouteErrorCode returns a stable code suitable for an API response.
func StaticRouteErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalidStaticRoute):
		return "invalid_static_route"
	case errors.Is(err, ErrStaticRouteNotFound):
		return "static_route_not_found"
	case errors.Is(err, ErrFirewallNATRuleNotFound):
		return "firewall_nat_rule_not_found"
	case errors.Is(err, ErrStaticRouteConflict):
		return "static_route_conflict"
	case errors.Is(err, ErrStaticRouteSuggestionUnavailable):
		return "static_route_suggestion_unavailable"
	default:
		return "static_route_operation_failed"
	}
}
