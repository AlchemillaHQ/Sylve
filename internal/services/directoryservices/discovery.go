// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
)

const controllerAttemptTimeout = 5 * time.Second
const maxControllers = 8

type ADDiscoveryInput struct {
	Domain           string `json:"domain"`
	Site             string `json:"site"`
	DomainController string `json:"domainController"`
}

type ADDiscovery struct {
	Domain           string   `json:"domain"`
	Workgroup        string   `json:"workgroup"`
	Site             string   `json:"site"`
	DomainController string   `json:"domainController"`
	Controllers      []string `json:"controllers"`
	Warning          string   `json:"warning"`
	serverSite       string
}

type controllerHealth struct {
	DomainController    string `json:"domainController"`
	Site                string `json:"site"`
	AlternateController string `json:"alternateController"`
	AlternateVerified   bool   `json:"alternateVerified"`
	Warning             string `json:"warning"`
}

func validateDiscovery(input ADDiscoveryInput) error {
	if !hostname.MatchString(input.Domain) || !strings.Contains(input.Domain, ".") {
		return invalid("a valid AD DNS domain is required")
	}
	if input.Site != "" && (!realmName.MatchString(input.Site) || strings.Contains(input.Site, ".")) {
		return invalid("AD site must be a DNS site label")
	}
	if input.DomainController != "" && !hostname.MatchString(input.DomainController) {
		return invalid("invalid domain controller hostname")
	}
	return nil
}

func discoveryInput(ad directoryModels.ActiveDirectory) ADDiscoveryInput {
	return ADDiscoveryInput{Domain: ad.Domain, Site: ad.Site, DomainController: ad.DomainController}
}

// DNS supplies ordering and weighting; keep that ordering while bounding work.
// A site is a preference, not a reason to lose otherwise usable domain DCs.
func (s *Service) controllers(ctx context.Context, input ADDiscoveryInput) ([]string, string, error) {
	domain := strings.ToLower(input.Domain)
	var records []*net.SRV
	warning := ""
	lookup := func(name string) []*net.SRV {
		attempt, cancel := context.WithTimeout(ctx, controllerAttemptTimeout)
		defer cancel()
		_, result, _ := s.lookupSRV(attempt, "ldap", "tcp", name)
		return result
	}
	if input.Site != "" {
		records = lookup(input.Site + "._sites.dc._msdcs." + domain)
		if len(records) > maxControllers/2 {
			records = records[:maxControllers/2]
		}
	}
	if len(records) == 0 && input.Site != "" {
		warning = "No controllers were discovered in the selected AD site. Using domain-wide discovery."
	}
	records = append(records, lookup("dc._msdcs."+domain)...)
	if len(records) == 0 {
		return nil, warning, fmt.Errorf("%w: AD LDAP DNS SRV discovery failed; configure the node's DNS to use AD DNS", ErrUnavailable)
	}
	result := []string{}
	if input.DomainController != "" {
		result = append(result, strings.ToLower(input.DomainController))
	}
	for _, record := range records {
		if record == nil {
			continue
		}
		host := strings.ToLower(strings.TrimSuffix(record.Target, "."))
		if !hostname.MatchString(host) || record.Port == 0 || slices.Contains(result, host) {
			continue
		}
		result = append(result, host)
		if len(result) == maxControllers {
			break
		}
	}
	if len(result) == 0 {
		return nil, warning, fmt.Errorf("%w: no usable AD domain controllers were discovered", ErrUnavailable)
	}
	return result, warning, nil
}

func lookupFields(text string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			result[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	return result
}

func (s *Service) lookupController(ctx context.Context, input ADDiscoveryInput, host string) (ADDiscovery, error) {
	ctx, cancel := context.WithTimeout(ctx, controllerAttemptTimeout)
	defer cancel()
	// CLDAP discovery does not use enrollment credentials or change membership.
	text, err := s.command(ctx, "/usr/local/bin/net", []string{"--configfile=" + s.smbPath, "--realm=" + input.Domain, "--server=" + host, "--timeout=4", "ads", "lookup"}, "", nil)
	if err != nil {
		return ADDiscovery{}, err
	}
	fields := lookupFields(text)
	domain := strings.ToUpper(fields["domain"])
	workgroup := strings.ToUpper(fields["pre-win2k domain"])
	site := fields["client site name"]
	serverSite := fields["server site name"]
	if !strings.EqualFold(domain, input.Domain) || !netbiosName.MatchString(workgroup) {
		return ADDiscovery{}, fmt.Errorf("%w: the discovered controller did not identify the requested AD domain", ErrUnavailable)
	}
	if site != "" && (!realmName.MatchString(site) || strings.Contains(site, ".")) {
		return ADDiscovery{}, fmt.Errorf("%w: the controller returned an invalid AD site", ErrUnavailable)
	}
	if serverSite != "" && (!realmName.MatchString(serverSite) || strings.Contains(serverSite, ".")) {
		return ADDiscovery{}, fmt.Errorf("%w: the controller returned an invalid AD site", ErrUnavailable)
	}
	return ADDiscovery{Domain: domain, Workgroup: workgroup, Site: site, DomainController: host, serverSite: serverSite}, nil
}

func controllerSiteWarning(site string, discovery ADDiscovery) string {
	if site != "" && discovery.serverSite != "" && !strings.EqualFold(site, discovery.serverSite) {
		return "No controller was verified in the preferred AD site. Using a controller from another site."
	}
	return ""
}

func (s *Service) DiscoverAD(ctx context.Context, input ADDiscoveryInput) (ADDiscovery, error) {
	input.Domain = strings.ToUpper(strings.TrimSpace(input.Domain))
	input.Site = strings.TrimSpace(input.Site)
	input.DomainController = strings.TrimSpace(input.DomainController)
	if err := validateDiscovery(input); err != nil {
		return ADDiscovery{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	controllers, warning, err := s.controllers(ctx, input)
	if err != nil {
		return ADDiscovery{}, err
	}
	visited := map[string]bool{}
	for _, host := range controllers {
		if ctx.Err() != nil || len(visited) == maxControllers {
			break
		}
		visited[host] = true
		result, err := s.lookupController(ctx, input, host)
		if err != nil {
			continue
		}
		// With no site override, CLDAP tells us this node's site. Prefer its
		// controllers for enrollment without persisting a discovered override.
		if input.Site == "" && input.DomainController == "" && result.Site != "" {
			localInput := input
			localInput.Site = result.Site
			nearby, localWarning, localErr := s.controllers(ctx, localInput)
			if localErr == nil {
				controllers, warning = nearby, localWarning
				for _, candidate := range nearby {
					if candidate == host {
						break
					}
					if visited[candidate] {
						continue
					}
					if ctx.Err() != nil || len(visited) == maxControllers {
						break
					}
					visited[candidate] = true
					if local, err := s.lookupController(ctx, localInput, candidate); err == nil && local.Workgroup == result.Workgroup {
						result = local
						break
					}
				}
			}
		}
		preferredSite := input.Site
		if preferredSite == "" {
			preferredSite = result.Site
		}
		if fallback := controllerSiteWarning(preferredSite, result); fallback != "" {
			warning = fallback
		}
		result.Controllers, result.Warning = controllers, warning
		return result, nil
	}
	return ADDiscovery{}, fmt.Errorf("%w: no discovered AD domain controller could be reached and verified", ErrUnavailable)
}

func validateDiscoveredWorkgroup(ad directoryModels.ActiveDirectory, discovery ADDiscovery) error {
	if !strings.EqualFold(ad.Workgroup, discovery.Workgroup) {
		return invalid("the configured workgroup does not match the AD domain's NetBIOS name")
	}
	return nil
}

func (s *Service) checkControllers(ctx context.Context, ad directoryModels.ActiveDirectory) (controllerHealth, error) {
	input := discoveryInput(ad)
	controllers, warning, err := s.controllers(ctx, input)
	result := controllerHealth{Warning: warning}
	var lastClockError error
	if err != nil {
		return result, err
	}
	// Prefer the controller Winbind actually uses, rather than confusing an
	// administrator preference with the current secure-channel connection.
	attempt, cancel := context.WithTimeout(ctx, controllerAttemptTimeout)
	text, dcErr := s.command(attempt, "/usr/local/bin/wbinfo", []string{"--dc-info", ad.Domain}, "", nil)
	cancel()
	if dcErr == nil {
		fields := strings.Fields(text)
		if len(fields) > 0 {
			host := strings.ToLower(strings.TrimSuffix(fields[0], "."))
			if hostname.MatchString(host) && strings.Contains(host, ".") {
				controllers = append([]string{host}, slices.DeleteFunc(controllers, func(value string) bool { return value == host })...)
				if len(controllers) > maxControllers {
					controllers = controllers[:maxControllers]
				}
			}
		}
	}
	for _, host := range controllers {
		if ctx.Err() != nil {
			break
		}
		attempt, cancel := context.WithTimeout(ctx, controllerAttemptTimeout)
		discovery, verifyErr := s.lookupController(attempt, input, host)
		candidate := ad
		candidate.DomainController = host
		if verifyErr == nil {
			verifyErr = validateDiscoveredWorkgroup(ad, discovery)
		}
		if verifyErr == nil {
			_, verifyErr = s.netCommand(attempt, candidate, nil, "ads", "testjoin")
		}
		if verifyErr == nil {
			verifyErr = s.checkClock(attempt, candidate)
			if verifyErr != nil {
				lastClockError = verifyErr
			}
		}
		cancel()
		if verifyErr != nil {
			continue
		}
		if result.DomainController == "" {
			result.DomainController, result.Site = host, discovery.Site
			if fallback := controllerSiteWarning(ad.Site, discovery); fallback != "" {
				result.Warning = fallback
			}
		} else if host != result.DomainController {
			result.AlternateController, result.AlternateVerified = host, true
			break
		}
	}
	if result.DomainController == "" {
		if lastClockError != nil {
			return result, lastClockError
		}
		return result, fmt.Errorf("%w: no discovered AD domain controller could authenticate this member", ErrUnavailable)
	}
	if !result.AlternateVerified {
		result.Warning = strings.TrimSpace(result.Warning + " No alternate domain controller was verified. Domain-controller redundancy is unverified.")
	}
	return result, nil
}
