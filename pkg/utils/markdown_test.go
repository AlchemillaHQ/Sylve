// SPDX-License-Identifier: BSD-2-Clause

package utils

import (
	"strings"
	"testing"
)

func TestRenderMarkdown(t *testing.T) {
	got := RenderMarkdown("# Heading\n\n**bold** and *italic*\n\n[link](https://example.com)")
	for _, want := range []string{"<h1>Heading</h1>", "<strong>bold</strong>", "<em>italic</em>", `href="https://example.com"`} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered Markdown missing %q: %s", want, got)
		}
	}
}

func TestRenderMarkdownRejectsUnsafeContent(t *testing.T) {
	for _, markdown := range []string{
		`<img src=x onerror=alert(1)>`,
		`<script>alert(1)</script>`,
		`[click](javascript:alert%281%29)`,
		`[click](jav&#x61;script:alert%281%29)`,
		`[click](data:text/html,unsafe)`,
	} {
		got := RenderMarkdown(markdown)
		for _, unsafe := range []string{"<img", "<script", "onerror=", "href="} {
			if strings.Contains(got, unsafe) {
				t.Errorf("unsafe output for %q: %s", markdown, got)
			}
		}
	}
}
