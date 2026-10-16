// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"time"
)

const DefaultBinary = "/usr/local/sbin/pkg-static"

const SupportedVersion = "2.8.4"

type Advisory struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	CVEs        []string `json:"cves"`
}

type Package struct {
	Name       string      `json:"name"`
	Version    string      `json:"version"`
	Origin     string      `json:"origin"`
	Repository string      `json:"repository"`
	Size       int64       `json:"size"`
	Automatic  bool        `json:"automatic"`
	Locked     bool        `json:"locked"`
	Protected  bool        `json:"protected"`
	Kind       PackageKind `json:"kind,omitempty"`
	Available  string      `json:"available"`
	Advisories []Advisory  `json:"advisories"`
}

type AuditPackage struct {
	Name       string     `json:"name"`
	Version    string     `json:"version"`
	Advisories []Advisory `json:"advisories"`
}

type Audit struct {
	Packages   []AuditPackage `json:"packages"`
	DatabaseAt *time.Time     `json:"databaseAt"`
}

type Change struct {
	Action     string `json:"action"`
	Name       string `json:"name"`
	From       string `json:"from"`
	To         string `json:"to"`
	Repository string `json:"repository"`
}

type Plan struct {
	Changes  []Change `json:"changes"`
	Download string   `json:"download"`
	Space    string   `json:"space"`
	Digest   string   `json:"digest"`
}

var packageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.-]*$`)

func ValidName(name string) bool { return len(name) <= 255 && packageName.MatchString(name) }

func Protected(name, origin string) bool {
	return Classify(name, origin) != KindApplication
}

func InventoryDigest(packages []Package) string {
	copy := append([]Package(nil), packages...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].Name < copy[j].Name })
	for i := range copy {
		copy[i].Available = ""
		copy[i].Advisories = nil
		copy[i].Kind = ""
	}
	return digest(copy)
}

func AuditInventoryDigest(packages []Package) string {
	versions := make([][2]string, 0, len(packages))
	for _, pkg := range packages {
		versions = append(versions, [2]string{pkg.Name, pkg.Version})
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i][0] < versions[j][0] })
	return digest(versions)
}

func digest(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (p *Plan) Seal() {
	sort.Slice(p.Changes, func(i, j int) bool {
		a, b := p.Changes[i], p.Changes[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Action < b.Action
	})
	p.Digest = digest(p.Changes)
}
