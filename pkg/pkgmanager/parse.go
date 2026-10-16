// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const InventoryFormat = "%n\t%v\t%o\t%R\t%sb\t%a\t%k"

func ParseInventory(output string) ([]Package, error) {
	packages := make([]Package, 0)
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(fields) != 7 || !ValidName(fields[0]) || fields[1] == "" || fields[2] == "" || seen[fields[0]] {
			return nil, fmt.Errorf("invalid_package_inventory")
		}
		size, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil || size < 0 || (fields[5] != "0" && fields[5] != "1") || (fields[6] != "0" && fields[6] != "1") {
			return nil, fmt.Errorf("invalid_package_inventory")
		}
		seen[fields[0]] = true
		if !ValidName(fields[3]) {
			fields[3] = "unknown-repository"
		}
		kind := Classify(fields[0], fields[2])
		packages = append(packages, Package{Name: fields[0], Version: fields[1], Origin: fields[2], Repository: fields[3], Size: size, Automatic: fields[5] == "1", Locked: fields[6] == "1", Protected: kind != KindApplication, Kind: kind, Advisories: []Advisory{}})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].Name < packages[j].Name })
	return packages, nil
}

func ParseAudit(output string, exitCode int, inventory []Package) (Audit, error) {
	var report struct {
		Count    *int `json:"pkg_count"`
		Packages map[string]struct {
			Version string `json:"version"`
			Count   *int   `json:"issue_count"`
			Issues  []struct {
				ID          string   `json:"id"`
				Description string   `json:"description"`
				URL         string   `json:"url"`
				CVEs        []string `json:"cve"`
			} `json:"issues"`
		} `json:"packages"`
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(output)))
	if err := decoder.Decode(&report); err != nil || report.Count == nil || *report.Count != len(report.Packages) || (exitCode != 0 && exitCode != 1) {
		return Audit{}, fmt.Errorf("invalid_package_audit_report")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Audit{}, fmt.Errorf("invalid_package_audit_report")
	}
	installed := make(map[string]string, len(inventory))
	for _, pkg := range inventory {
		installed[pkg.Name] = pkg.Version
	}
	result := Audit{Packages: []AuditPackage{}}
	for name, entry := range report.Packages {
		if installed[name] != entry.Version || entry.Count == nil || *entry.Count != len(entry.Issues) || len(entry.Issues) == 0 {
			return Audit{}, fmt.Errorf("invalid_package_audit_report")
		}
		pkg := AuditPackage{Name: name, Version: entry.Version, Advisories: []Advisory{}}
		seen := make(map[string]bool)
		for _, issue := range entry.Issues {
			u, err := url.Parse(issue.URL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
				return Audit{}, fmt.Errorf("invalid_package_advisory")
			}
			id := issue.ID
			if id == "" {
				id = strings.TrimSuffix(strings.TrimPrefix(u.Path, "/freebsd/"), ".html")
			}
			if id == "" || seen[id] {
				return Audit{}, fmt.Errorf("invalid_package_advisory")
			}
			seen[id] = true
			pkg.Advisories = append(pkg.Advisories, Advisory{ID: id, Description: issue.Description, URL: issue.URL, CVEs: issue.CVEs})
		}
		sort.Slice(pkg.Advisories, func(i, j int) bool { return pkg.Advisories[i].ID < pkg.Advisories[j].ID })
		result.Packages = append(result.Packages, pkg)
	}
	if *report.Count == 0 && exitCode != 0 {
		return Audit{}, fmt.Errorf("package_audit_failed")
	}
	sort.Slice(result.Packages, func(i, j int) bool { return result.Packages[i].Name < result.Packages[j].Name })
	return result, nil
}

var (
	planHeader    = regexp.MustCompile(`^The following ([0-9]+) package\(s\) will be affected \(of [0-9]+ checked\):$`)
	changeLine    = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9+_.-]*): ([^ ]+) -> ([^ ]+)(?: \[([^\]]+)\])?(?: \([^\r\n]*\))?$`)
	installLine   = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9+_.-]*): ([^ ]+)(?: \[([^\]]+)\])?(?: \([^\r\n]*\))?$`)
	reinstallLine = regexp.MustCompile(`^(.+)-([0-9][^ ]*)(?: \[([^\]]+)\])?(?: \([^\r\n]*\))*$`)
	countLine     = regexp.MustCompile(`^Number of packages to be (upgraded|installed|removed|downgraded|reinstalled): ([0-9]+)$`)
)

func ParsePlan(output string) (Plan, error) {
	plan := Plan{Changes: []Change{}}
	active, finished, expected := false, false, -1
	section := ""
	counts := map[string]int{}
	observed := map[string]int{}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(output, "\r", "")))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if match := planHeader.FindStringSubmatch(line); match != nil {
			plan = Plan{Changes: []Change{}}
			expected, _ = strconv.Atoi(match[1])
			active = true
			finished = false
			section = ""
			counts = map[string]int{}
			observed = map[string]int{}
			seen = map[string]bool{}
			continue
		}
		if !active || line == "" {
			continue
		}
		switch line {
		case "Installed packages to be UPGRADED:":
			section = "upgrade"
			continue
		case "New packages to be INSTALLED:":
			section = "install"
			continue
		case "Installed packages to be REMOVED:":
			section = "remove"
			continue
		case "Installed packages to be DOWNGRADED:":
			section = "downgrade"
			continue
		case "Installed packages to be REINSTALLED:":
			section = "reinstall"
			continue
		}
		if match := countLine.FindStringSubmatch(line); match != nil {
			finished = true
			section = ""
			counts[match[1]], _ = strconv.Atoi(match[2])
			continue
		}
		if finished {
			if strings.HasSuffix(line, "to be downloaded.") {
				plan.Download = line
			}
			if strings.HasPrefix(line, "The operation will free ") || strings.HasPrefix(line, "The process will require ") {
				plan.Space = line
			}
			continue
		}
		if section == "" || len(raw) == len(strings.TrimLeft(raw, " \t")) {
			return Plan{}, fmt.Errorf("unsupported_package_plan")
		}
		change := Change{Action: section}
		if section == "upgrade" || section == "downgrade" {
			match := changeLine.FindStringSubmatch(line)
			if match == nil {
				return Plan{}, fmt.Errorf("unsupported_package_plan")
			}
			change.Name, change.From, change.To, change.Repository = match[1], match[2], match[3], match[4]
		} else {
			match := installLine.FindStringSubmatch(line)
			if section == "reinstall" {
				match = reinstallLine.FindStringSubmatch(line)
			}
			if match == nil || !ValidName(match[1]) {
				return Plan{}, fmt.Errorf("unsupported_package_plan")
			}
			change.Name, change.Repository = match[1], match[3]
			if section == "remove" {
				change.From = match[2]
			} else {
				change.To = match[2]
				if section == "reinstall" {
					change.From = match[2]
				}
			}
		}
		if seen[change.Name] {
			return Plan{}, fmt.Errorf("duplicate_package_plan_action")
		}
		seen[change.Name] = true
		plan.Changes = append(plan.Changes, change)
		observed[section]++
	}
	if scanner.Err() != nil {
		return Plan{}, scanner.Err()
	}
	if !active {
		if strings.Contains(output, "Your packages are up to date.") {
			plan.Seal()
			return plan, nil
		}
		return Plan{}, fmt.Errorf("unsupported_package_plan")
	}
	if !finished || expected != len(plan.Changes) {
		return Plan{}, fmt.Errorf("incomplete_package_plan")
	}
	for plural, count := range counts {
		action := map[string]string{"upgraded": "upgrade", "installed": "install", "removed": "remove", "downgraded": "downgrade", "reinstalled": "reinstall"}[plural]
		if observed[action] != count {
			return Plan{}, fmt.Errorf("incomplete_package_plan")
		}
		delete(observed, action)
	}
	if len(observed) != 0 {
		return Plan{}, fmt.Errorf("incomplete_package_plan")
	}
	plan.Seal()
	return plan, nil
}
