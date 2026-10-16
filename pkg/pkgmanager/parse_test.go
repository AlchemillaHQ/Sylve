// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
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
	if err := ValidateTransaction(plan, plan); err != nil {
		t.Fatal(err)
	}
	changed := plan
	changed.Changes = append([]Change(nil), plan.Changes...)
	changed.Changes[0].To = "unexpected"
	if err := ValidateTransaction(changed, plan); err == nil {
		t.Fatal("accepted changed transaction")
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

func TestRedact(t *testing.T) {
	value := Redact("fetch pkg+https://user:secret@example.org/repo https://token@example.org/a")
	if strings.Contains(value, "secret") || strings.Contains(value, "token") {
		t.Fatal(value)
	}
}

func TestRepositoryAnnotationDoesNotExposeURLCredentials(t *testing.T) {
	packages, err := ParseInventory("app\t1\tmisc/app\thttps://user:secret@example.org/repo\t1\t0\t0")
	if err != nil || len(packages) != 1 || packages[0].Repository != "unknown-repository" {
		t.Fatalf("%+v %v", packages, err)
	}
	if value := Redact("https://example.org/repo?token=secret&branch=stable"); strings.Contains(value, "secret") {
		t.Fatal(value)
	}
}
