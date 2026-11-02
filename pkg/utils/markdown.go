// SPDX-License-Identifier: BSD-2-Clause

package utils

import (
	"bytes"
	"html"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
)

var markdownHTMLPolicy = bluemonday.UGCPolicy()

func RenderMarkdown(markdown string) string {
	var rendered bytes.Buffer
	if err := goldmark.Convert([]byte(markdown), &rendered); err != nil {
		return html.EscapeString(markdown)
	}

	return markdownHTMLPolicy.Sanitize(rendered.String())
}
