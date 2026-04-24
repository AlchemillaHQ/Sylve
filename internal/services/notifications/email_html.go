// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package notifications

import (
	"bytes"
	"html/template"
	"strings"
	"time"

	notifier "github.com/alchemillahq/sylve/internal/notifications"
)

const sylveLogoURL = "https://public-bucket.sylve.io/mail-logo.png"

type emailTemplateData struct {
	LogoURL       string
	Title         string
	Body          string
	SeverityLabel string
	SeverityColor string
	SeverityIcon  template.HTML
	ShowMetadata  bool
	Pool          string
	VdevPath      string
	State         string
	Source        string
	Kind          string
	Year          int
}

var svgCheckCircle = template.HTML(`<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#22c55e" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="9 12 11 14 15 10"/></svg>`)
var svgWarningTriangle = template.HTML(`<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#f59e0b" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>`)
var svgXCircle = template.HTML(`<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#ef4444" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>`)
var svgAlertCircle = template.HTML(`<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#dc2626" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>`)

func severityEmailStyle(severity string) (label, color string, icon template.HTML) {
	switch strings.TrimSpace(strings.ToLower(severity)) {
	case "warning":
		return "Warning", "#f59e0b", svgWarningTriangle
	case "error":
		return "Error", "#ef4444", svgXCircle
	case "critical":
		return "Critical", "#dc2626", svgAlertCircle
	default:
		return "Info", "#22c55e", svgCheckCircle
	}
}

func buildEmailHTML(input notifier.EventInput, now time.Time) string {
	severityLabel, severityColor, severityIcon := severityEmailStyle(input.Severity)

	pool := strings.TrimSpace(input.Metadata["pool"])
	vdevPath := strings.TrimSpace(input.Metadata["vdev_path"])
	state := strings.TrimSpace(input.Metadata["state"])
	showMetadata := pool != "" || vdevPath != "" || state != ""

	data := emailTemplateData{
		LogoURL:       sylveLogoURL,
		Title:         input.Title,
		Body:          strings.TrimSpace(input.Body),
		SeverityLabel: severityLabel,
		SeverityColor: severityColor,
		SeverityIcon:  severityIcon,
		ShowMetadata:  showMetadata,
		Pool:          pool,
		VdevPath:      vdevPath,
		State:         state,
		Source:        strings.TrimSpace(input.Source),
		Kind:          strings.TrimSpace(input.Kind),
		Year:          now.Year(),
	}

	var buf bytes.Buffer
	if err := emailTemplate.Execute(&buf, data); err != nil {
		return "<html><body>" + template.HTMLEscapeString(input.Title) + "</body></html>"
	}

	return buf.String()
}

var emailTemplate = template.Must(template.New("email").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Title}}</title>
<style>
  body { margin:0; padding:0; font-family: Arial, sans-serif; background-color:#f4f4f4; color:#333; line-height:1.6; }
  .wrapper { max-width:680px; margin:40px auto; background-color:#fff; }
  .header { background-color:#161616; padding:24px 32px; text-align:center; }
  .header img { height:72px; display:inline-block; }
  .content { padding:32px 32px 24px; }
  .severity-badge { display:inline-flex; align-items:center; gap:8px; padding:6px 14px; border-radius:999px; background-color:{{.SeverityColor}}1a; border:1px solid {{.SeverityColor}}66; margin-bottom:18px; }
  .severity-badge span { font-size:13px; font-weight:600; color:{{.SeverityColor}}; letter-spacing:0.04em; text-transform:uppercase; }
  .alert-title { font-size:22px; font-weight:700; color:#161616; margin:0 0 12px; }
  .alert-body { font-size:15px; color:#444; margin:0 0 24px; }
  .meta-table { width:100%; border-collapse:collapse; margin-bottom:20px; }
  .meta-table td { padding:8px 10px; font-size:13px; border-bottom:1px solid #ebebeb; }
  .meta-table td:first-child { font-weight:600; color:#161616; width:110px; white-space:nowrap; }
  .meta-table td:last-child { color:#555; word-break:break-all; }
  .meta-row-last td { border-bottom:none; }
  .divider { border:none; border-top:1px solid #ebebeb; margin:24px 0 20px; }
  .footer-meta { font-size:11px; color:#999; margin-bottom:4px; }
  .footer { background-color:#161616; padding:18px 32px; text-align:center; }
  .footer p { margin:0; font-size:12px; color:#888; }
</style>
</head>
<body>
  <div class="wrapper">
    <div class="header">
      <img src="{{.LogoURL}}" alt="Sylve">
    </div>
    <div class="content">
      <div class="severity-badge">
        {{.SeverityIcon}}
        <span>{{.SeverityLabel}}</span>
      </div>
      <div class="alert-title">{{.Title}}</div>
      {{if .Body}}<p class="alert-body">{{.Body}}</p>{{end}}
      {{if .ShowMetadata}}
      <table class="meta-table">
        {{if .Pool}}<tr><td>Pool</td><td>{{.Pool}}</td></tr>{{end}}
        {{if .VdevPath}}<tr><td>Device</td><td>{{.VdevPath}}</td></tr>{{end}}
        {{if .State}}<tr class="meta-row-last"><td>State</td><td>{{.State}}</td></tr>{{end}}
      </table>
      {{end}}
      <hr class="divider">
      {{if .Source}}<p class="footer-meta">Source: {{.Source}}</p>{{end}}
      {{if .Kind}}<p class="footer-meta">Kind: {{.Kind}}</p>{{end}}
    </div>
    <div class="footer">
      <p>&copy; {{.Year}} <a href="https://sylve.io" style="color:#aaa; text-decoration:none;">Sylve</a>. All rights reserved.</p>
    </div>
  </div>
</body>
</html>`))
