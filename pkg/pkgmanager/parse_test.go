// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"slices"
	"strings"
	"testing"
)

const examplePlan = `The following 2 package(s) will be affected (of 3 checked):

New packages to be INSTALLED:
	new-library: 1.2 [FreeBSD-ports]

Installed packages to be UPGRADED:
	test-app: 1.0_2,1 -> 2.0 [FreeBSD-ports]

Number of packages to be installed: 1
Number of packages to be upgraded: 1

2 MiB to be downloaded.
`

func TestInventory(t *testing.T) {
	packages, err := ParseInventory("foo-bar\t1.0_1,2\twww/foo\tlocal\t1024\t0\t1\nFreeBSD-runtime\t15.0\tbase/runtime\tbase\t100\t1\t0\n")
	if err != nil || len(packages) != 2 || !packages[0].Protected || !packages[1].Locked {
		t.Fatalf("inventory=%+v err=%v", packages, err)
	}
	for _, output := range []string{"foo\t1.0", "foo\t1\twww/foo\tr\t-1\t0\t0", "foo\t1\twww/foo\tr\t1\twrong\t0"} {
		if _, err := ParseInventory(output); err == nil {
			t.Fatalf("accepted %q", output)
		}
	}
}

func TestAudit(t *testing.T) {
	installed := []Package{{Name: "app", Version: "1.0"}}
	output := `{"pkg_count":1,"packages":{"app":{"version":"1.0","issue_count":1,"issues":[{"description":"Test","url":"https://vuxml.FreeBSD.org/freebsd/advisory-id.html","cve":["CVE-2099-1234"]}]}}}`
	report, err := ParseAudit(output, 1, installed)
	if err != nil || len(report.Packages) != 1 || report.Packages[0].Advisories[0].ID != "advisory-id" {
		t.Fatalf("%+v %v", report, err)
	}
	for _, data := range []string{output + "junk", `{}`, `{"pkg_count":0,"packages":{"app":{}}}`, strings.Replace(output, "https://vuxml.FreeBSD.org/freebsd/advisory-id.html", "javascript:alert(1)", 1), strings.Replace(output, `"version":"1.0"`, `"version":"2.0"`, 1)} {
		if _, err := ParseAudit(data, 1, installed); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
	if _, err := ParseAudit(`{"pkg_count":0,"packages":null}`, 0, installed); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAudit(`{"pkg_count":0,"packages":null}`, 1, installed); err == nil {
		t.Fatal("failed command reported clean")
	}
}

func TestTransactionPlan(t *testing.T) {
	plan, err := ParsePlan(examplePlan)
	if err != nil || len(plan.Changes) != 2 {
		t.Fatalf("%+v %v", plan, err)
	}
	for _, output := range []string{strings.Replace(examplePlan, "2 package(s)", "3 package(s)", 1), strings.Replace(examplePlan, "to be installed: 1", "to be installed: 2", 1), strings.Replace(examplePlan, "new-library: 1.2", "not a package row", 1), strings.Split(examplePlan, "Number of packages")[0]} {
		if _, err := ParsePlan(output); err == nil {
			t.Fatalf("accepted incomplete/unknown plan %q", output)
		}
	}
	if _, err := ParsePlan("Your packages are up to date.\n"); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedPkgPlan(t *testing.T) {
	// Trimmed from pkg 2.8.4 output read through a pty: CRLF line endings and \r progress redraws.
	output := `Updating FreeBSD-ports repository catalogue...
FreeBSD-ports repository is up to date.
Updating FreeBSD-base repository catalogue...
FreeBSD-base repository is up to date.
All repositories are up to date.
Checking for upgrades (5 candidates):   0%` + "\r" + `Checking for upgrades (5 candidates): 100%
Processing candidates (5 candidates):   0%` + "\r" + `Processing candidates (5 candidates): 100%
Checking integrity... done (0 conflicting)
The following 5 package(s) will be affected (of 0 checked):

Installed packages to be REMOVED:
	php82-zlib: 8.2.35_1

New packages to be INSTALLED:
	php85-zlib: 8.5.12_2 [FreeBSD-ports]

Installed packages to be UPGRADED:
	FreeBSD-runtime: 16.snap20261021210449 -> 16.snap20261105165144 [FreeBSD-base]
	chromium: 154.0.8037.57_1 -> 156.0.8078.79 [FreeBSD-ports] (vulnerable!)
	libarchive: 3.8.8,1 -> 3.8.11,1 [FreeBSD-ports]

Number of packages to be removed: 1
Number of packages to be installed: 1
Number of packages to be upgraded: 3

The operation will free 108 MiB.
852 MiB to be downloaded.

WARNING: 1 package(s) have known vulnerabilities
`
	plan, err := ParsePlan(strings.ReplaceAll(output, "\n", "\r\n"))
	want := []Change{
		{Action: "upgrade", Name: "FreeBSD-runtime", From: "16.snap20261021210449", To: "16.snap20261105165144", Repository: "FreeBSD-base"},
		{Action: "upgrade", Name: "chromium", From: "154.0.8037.57_1", To: "156.0.8078.79", Repository: "FreeBSD-ports"},
		{Action: "upgrade", Name: "libarchive", From: "3.8.8,1", To: "3.8.11,1", Repository: "FreeBSD-ports"},
		{Action: "remove", Name: "php82-zlib", From: "8.2.35_1"},
		{Action: "install", Name: "php85-zlib", To: "8.5.12_2", Repository: "FreeBSD-ports"},
	}
	if err != nil || !slices.Equal(plan.Changes, want) || plan.Space != "The operation will free 108 MiB." || plan.Download != "852 MiB to be downloaded." {
		t.Fatalf("%+v %v", plan, err)
	}
}

func TestRedact(t *testing.T) {
	for input, want := range map[string]string{
		"fetch pkg+https://user:secret@example.org/repo":                      "fetch pkg+https://[redacted]@example.org/repo",
		"https://token@example.org/a":                                         "https://[redacted]@example.org/a",
		"SSH://user@example.org/repo":                                         "SSH://[redacted]@example.org/repo",
		"https://example.org/repo?token=secret&branch=stable&API_KEY=other":   "https://example.org/repo?token=[redacted]&branch=stable&API_KEY=[redacted]",
		"mail admin@example.org about https://example.org/repo?branch=stable": "mail admin@example.org about https://example.org/repo?branch=stable",
	} {
		if got := Redact(input); got != want {
			t.Fatalf("%q: %q", input, got)
		}
	}
}

func TestRepositoryAnnotationDoesNotExposeURLCredentials(t *testing.T) {
	packages, err := ParseInventory("app\t1\tmisc/app\thttps://user:secret@example.org/repo\t1\t0\t0")
	if err != nil || len(packages) != 1 || packages[0].Repository != "unknown-repository" {
		t.Fatalf("%+v %v", packages, err)
	}
}
