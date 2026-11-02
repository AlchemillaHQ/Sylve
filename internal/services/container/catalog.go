// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alchemillahq/sylve/internal/assets"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gopkg.in/yaml.v3"
)

var catalogSources = map[string]string{
	"daemonless": "https://catalog.daemonless.io/v1/daemonless",
}

const catalogTTL = 24 * time.Hour

var catalogDefaultOwners = map[string]string{
	"daemonless": "1000:1000",
}

var (
	catalogClient   = &http.Client{Timeout: 30 * time.Second}
	catalogMu       sync.Mutex
	catalogFiles    = regexp.MustCompile(`^(icons|manifests)/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	catalogLogo     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*\.(svg|png)$`)
	catalogVars     = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)(?::-([^}]*))?\}`)
	catalogLabelEnd = regexp.MustCompile(` \(|\s+[—–]\s+|\. |; |: |, (e\.g\.|such as) `)

	catalogNameRegex = regexp.MustCompile(`[^A-Za-z0-9-]+`)
	volumeNameRegex  = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
)

type catalogIndex struct {
	Icon string   `json:"icon"`
	OS   []string `json:"os"`
	Apps []struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		Category      string   `json:"category"`
		Class         string   `json:"class"`
		Icon          string   `json:"icon"`
		Description   string   `json:"description"`
		Version       string   `json:"version"`
		Manifest      string   `json:"manifest_url"`
		Architectures []string `json:"architectures"`
		OS            []string `json:"os"`
		Variants      []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Image   string `json:"image"`
			Default bool   `json:"default"`
		} `json:"variants"`
	} `json:"apps"`
}

type composeList []string

func (l *composeList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		var values map[string]string
		if err := node.Decode(&values); err != nil {
			return err
		}
		for _, key := range slices.Sorted(maps.Keys(values)) {
			*l = append(*l, key+"="+values[key])
		}
		return nil
	}
	return node.Decode((*[]string)(l))
}

type composePort struct {
	Short     string
	Published string `yaml:"published"`
	Target    string `yaml:"target"`
	Protocol  string `yaml:"protocol"`
}

func (p *composePort) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&p.Short)
	}
	type long composePort
	return node.Decode((*long)(p))
}

type composeService struct {
	Image           string        `yaml:"image"`
	ContainerName   string        `yaml:"container_name"`
	Restart         string        `yaml:"restart"`
	Environment     composeList   `yaml:"environment"`
	Ports           []composePort `yaml:"ports"`
	Volumes         []string      `yaml:"volumes"`
	Annotations     composeList   `yaml:"annotations"`
	NetworkMode     string        `yaml:"network_mode"`
	StopGracePeriod string        `yaml:"stop_grace_period"`
	ExtraHosts      []string      `yaml:"extra_hosts"`
	DependsOn       composeNames  `yaml:"depends_on"`
	EnvFile         composeNames  `yaml:"env_file"`
}

type composeNames []string

func (n *composeNames) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*n = composeNames{node.Value}
		return nil
	case yaml.MappingNode:
		var values map[string]yaml.Node
		if err := node.Decode(&values); err != nil {
			return err
		}
		*n = slices.Sorted(maps.Keys(values))
		return nil
	}
	return node.Decode((*[]string)(n))
}

type catalogManifest struct {
	Services map[string]composeService `yaml:"services"`
	Fjord    struct {
		Info struct {
			WebPort  string `yaml:"web_port"`
			WebHTTPS bool   `yaml:"web_https"`
		} `yaml:"info"`
		Login *struct {
			User     string `yaml:"user"`
			Password string `yaml:"password"`
		} `yaml:"login"`
		Variables  []catalogVariable `yaml:"variables"`
		Choices    []catalogChoice   `yaml:"choices"`
		Networking map[string]string `yaml:"networking"`
		Hostnames  map[string]string `yaml:"hostnames"`
	} `yaml:"x-fjord"`
}

type catalogVariable struct {
	Name            string `yaml:"name"`
	Label           string `yaml:"label"`
	Type            string `yaml:"type"`
	Default         string `yaml:"default"`
	Optional        bool   `yaml:"optional"`
	Level           string `yaml:"level"`
	Generate        *bool  `yaml:"generate"`
	HostPermissions *struct {
		UID  int    `yaml:"uid"`
		GID  int    `yaml:"gid"`
		Mode string `yaml:"mode"`
	} `yaml:"host_permissions"`
	Values map[string]string `yaml:"values"`
}

type catalogChoice struct {
	ID      string          `yaml:"id"`
	Label   string          `yaml:"label"`
	Doc     string          `yaml:"doc"`
	Default string          `yaml:"default"`
	Options []catalogOption `yaml:"options"`
}

type catalogOption struct {
	ID        string              `yaml:"id"`
	Label     string              `yaml:"label"`
	Doc       string              `yaml:"doc"`
	Env       map[string]string   `yaml:"env"`
	Defaults  map[string]string   `yaml:"defaults"`
	Secrets   []string            `yaml:"secrets"`
	Ask       []catalogVariable   `yaml:"ask"`
	Services  string              `yaml:"services"`
	Drop      []string            `yaml:"drop"`
	DependsOn map[string][]string `yaml:"depends_on"`
	Hostnames map[string]string   `yaml:"hostnames"`
}

func (s *Service) catalogFile(ctx context.Context, source, name string, refresh bool) (string, error) {
	base, ok := catalogSources[source]
	if !ok {
		return "", fmt.Errorf("catalog_not_found: %s", source)
	}
	path := s.path("catalog", source, name)
	catalogMu.Lock()
	defer catalogMu.Unlock()
	info, statErr := os.Stat(path)
	if statErr == nil && (!refresh || time.Since(info.ModTime()) < catalogTTL) {
		return path, nil
	}
	err := fetchCatalogFile(ctx, base+"/"+name, path)
	if err != nil && statErr == nil {
		logger.L.Warn().Err(err).Str("file", name).Msg("catalog_refresh_failed_using_cached_copy")
		return path, nil
	}
	if err != nil {
		data, builtinErr := fs.ReadFile(assets.CatalogFiles, "catalog/"+source+"/"+name)
		if builtinErr != nil {
			return path, err
		}
		if err := writeFile(path, string(data), 0o600); err != nil {
			return path, err
		}
		logger.L.Warn().Err(err).Str("file", name).Msg("catalog_unreachable_using_builtin_copy")
		return path, os.Chtimes(path, time.Unix(0, 0), time.Unix(0, 0))
	}
	return path, err
}

func fetchCatalogFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := catalogClient.Do(req)
	if err != nil {
		return fmt.Errorf("catalog_unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("catalog_unreachable: %s returned %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := io.Copy(file, io.LimitReader(resp.Body, 16<<20)); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (s *Service) catalogIndex(ctx context.Context, source string) (catalogIndex, error) {
	var index catalogIndex
	path, err := s.catalogFile(ctx, source, "catalog.json", true)
	if err != nil {
		return index, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return index, err
	}
	return index, json.Unmarshal(data, &index)
}

func (s *Service) Catalog(ctx context.Context) ([]containerServiceInterfaces.CatalogApp, error) {
	apps := []containerServiceInterfaces.CatalogApp{}
	var failure error
	for _, source := range slices.Sorted(maps.Keys(catalogSources)) {
		index, err := s.catalogIndex(ctx, source)
		if err != nil {
			failure = err
			continue
		}
		for i := range index.Apps {
			if app, ok := catalogApp(source, index, i); ok {
				apps = append(apps, app)
			}
		}
	}
	if len(apps) == 0 && failure != nil {
		return nil, failure
	}
	return apps, nil
}

func catalogApp(source string, index catalogIndex, i int) (containerServiceInterfaces.CatalogApp, bool) {
	entry := index.Apps[i]
	if entry.Class != "service" && entry.Class != "stack" || len(entry.Architectures) > 0 && !slices.Contains(entry.Architectures, runtime.GOARCH) {
		return containerServiceInterfaces.CatalogApp{}, false
	}
	app := containerServiceInterfaces.CatalogApp{Source: source, ID: entry.ID, Name: entry.Name, Category: entry.Category,
		Description: entry.Description, Version: entry.Version, OS: "freebsd", Stack: entry.Class == "stack"}
	if systems := slices.Concat(entry.OS, index.OS); len(systems) > 0 {
		app.OS = systems[0]
	}
	for _, variant := range entry.Variants {
		app.Variants = append(app.Variants, containerServiceInterfaces.CatalogVariant{ID: variant.ID, Label: variant.Label,
			Image: variant.Image, Default: variant.Default})
	}
	return app, true
}

func (s *Service) catalogEntry(ctx context.Context, source, id string, file func(icon, manifest string) string) (containerServiceInterfaces.CatalogApp, string, error) {
	index, err := s.catalogIndex(ctx, source)
	if err != nil {
		return containerServiceInterfaces.CatalogApp{}, "", err
	}
	for i, entry := range index.Apps {
		if entry.ID != id {
			continue
		}
		app, ok := catalogApp(source, index, i)
		name := file(entry.Icon, entry.Manifest)
		if !ok || !catalogFiles.MatchString(name) {
			break
		}
		path, err := s.catalogFile(ctx, source, name, strings.HasPrefix(name, "manifests/"))
		return app, path, err
	}
	return containerServiceInterfaces.CatalogApp{}, "", fmt.Errorf("catalog_app_not_found: %s/%s", source, id)
}

func (s *Service) CatalogIcon(ctx context.Context, source, id string) (string, error) {
	if id == "" {
		index, err := s.catalogIndex(ctx, source)
		if err != nil {
			return "", err
		}
		if !catalogLogo.MatchString(index.Icon) {
			return "", fmt.Errorf("catalog_logo_not_found: %s", source)
		}
		return s.catalogFile(ctx, source, index.Icon, false)
	}
	_, path, err := s.catalogEntry(ctx, source, id, func(icon, _ string) string { return icon })
	return path, err
}

func (s *Service) catalogManifest(ctx context.Context, source, id string) (containerServiceInterfaces.CatalogApp, catalogManifest, error) {
	var manifest catalogManifest
	app, path, err := s.catalogEntry(ctx, source, id, func(_, manifest string) string { return manifest })
	if err != nil {
		return app, manifest, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return app, manifest, err
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return app, manifest, fmt.Errorf("invalid_catalog_manifest: %w", err)
	}
	if len(manifest.Services) == 0 {
		return app, manifest, fmt.Errorf("catalog_app_has_no_containers: %s", id)
	}
	return app, manifest, nil
}

func (s *Service) CatalogTemplate(ctx context.Context, source, id string) (containerServiceInterfaces.CatalogTemplate, error) {
	app, manifest, err := s.catalogManifest(ctx, source, id)
	template := containerServiceInterfaces.CatalogTemplate{App: app, Variables: []containerServiceInterfaces.CatalogVariable{},
		Choices: []containerServiceInterfaces.CatalogChoice{}, Containers: []string{}}
	if err != nil {
		return template, err
	}
	every := catalogManifest{Services: maps.Clone(manifest.Services)}
	for _, choice := range manifest.Fjord.Choices {
		for _, option := range choice.Options {
			added, err := optionServices(option)
			if err != nil {
				return template, err
			}
			maps.Copy(every.Services, added)
		}
	}
	directories, protocols := catalogDirectories(every), catalogPortProtocols(every)
	defaults := map[string]string{}
	for _, variable := range manifest.Fjord.Variables {
		defaults[variable.Name] = variable.Default
	}
	appOwner := catalogOwner(source, defaults)
	describe := func(variable catalogVariable) containerServiceInterfaces.CatalogVariable {
		value := catalogDefault(variable.Default)
		if generatedSecret(variable) && value == "" {
			value = utils.GenerateRandomString(24)
		}
		item := containerServiceInterfaces.CatalogVariable{Name: variable.Name, Type: variable.Type, Default: value,
			Optional: variable.Optional, Advanced: variable.Level == "advanced", Protocol: protocols[variable.Name], Values: variable.Values}
		item.Label, item.Hint = catalogLabel(variable.Name, variable.Label)
		if target, ok := directories[variable.Name]; ok {
			item.Target, item.Volume = target, volumeName(target, variable.Name)
			item.Owner = appOwner
			if permissions := variable.HostPermissions; permissions != nil {
				item.Owner = fmt.Sprintf("%d:%d", permissions.UID, permissions.GID)
			}
		}
		return item
	}
	originals := []string{}
	for _, variable := range manifest.Fjord.Variables {
		originals = append(originals, strings.Join(strings.Fields(variable.Label), " "))
		template.Variables = append(template.Variables, describe(variable))
	}
	distinctLabels(template.Variables, originals)

	base, _ := resolveServices(manifest, nil)
	stack := ""
	if len(base.services) > 1 {
		stack = catalogName(app.ID)
	}
	names := base.names(app.ID, stack, func(text string) string { return text })
	for _, service := range base.order() {
		template.Containers = append(template.Containers, names[service])
	}
	for _, choice := range manifest.Fjord.Choices {
		item := containerServiceInterfaces.CatalogChoice{ID: choice.ID, Label: choice.Label, Doc: choice.Doc, Default: choice.Default}
		for _, option := range choice.Options {
			described := containerServiceInterfaces.CatalogChoiceOption{ID: option.ID, Label: option.Label, Doc: option.Doc,
				Adds: []string{}, Drops: []string{}, Ask: []containerServiceInterfaces.CatalogVariable{}}
			added, _ := optionServices(option)
			for _, service := range slices.Sorted(maps.Keys(added)) {
				described.Adds = append(described.Adds, stackContainerName(catalogName(app.ID), service))
			}
			for _, service := range option.Drop {
				described.Drops = append(described.Drops, cmp.Or(names[service], stackContainerName(catalogName(app.ID), service)))
			}
			for _, variable := range option.Ask {
				described.Ask = append(described.Ask, describe(variable))
			}
			item.Options = append(item.Options, described)
		}
		template.Choices = append(template.Choices, item)
	}
	return template, nil
}

type catalogServices struct {
	services  map[string]composeService
	dependsOn map[string][]string
	hostnames map[string]string
}

func optionServices(option catalogOption) (map[string]composeService, error) {
	services := map[string]composeService{}
	if err := yaml.Unmarshal([]byte(option.Services), &services); err != nil {
		return nil, fmt.Errorf("invalid_catalog_manifest: %w", err)
	}
	return services, nil
}

func resolveServices(manifest catalogManifest, options []catalogOption) (catalogServices, error) {
	resolved := catalogServices{services: maps.Clone(manifest.Services), dependsOn: map[string][]string{}, hostnames: map[string]string{}}
	maps.Copy(resolved.hostnames, manifest.Fjord.Hostnames)
	for _, option := range options {
		added, err := optionServices(option)
		if err != nil {
			return resolved, err
		}
		maps.Copy(resolved.services, added)
		for service, needs := range option.DependsOn {
			resolved.dependsOn[service] = append(resolved.dependsOn[service], needs...)
		}
		maps.Copy(resolved.hostnames, option.Hostnames)
	}
	for _, option := range options {
		for _, service := range option.Drop {
			delete(resolved.services, service)
		}
	}
	for name, service := range resolved.services {
		resolved.dependsOn[name] = append(resolved.dependsOn[name], service.DependsOn...)
	}
	return resolved, nil
}

func (r catalogServices) order() []string {
	ordered, seen := []string{}, map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		if _, ok := r.services[name]; !ok || seen[name] {
			return
		}
		seen[name] = true
		for _, need := range r.dependsOn[name] {
			visit(need)
		}
		ordered = append(ordered, name)
	}
	for _, name := range slices.Sorted(maps.Keys(r.services)) {
		visit(name)
	}
	return ordered
}

func (r catalogServices) main(app string, networking map[string]string, ordered []string) string {
	for _, name := range ordered {
		if networking[name] == "default" {
			return name
		}
	}
	if _, ok := r.services[app]; ok {
		return app
	}
	return ordered[len(ordered)-1]
}

func (r catalogServices) names(app, stack string, expand func(string) string) map[string]string {
	names := map[string]string{}
	for name, service := range r.services {
		if stack == "" {
			names[name] = catalogName(cmp.Or(expand(service.ContainerName), app))
		} else {
			names[name] = stackContainerName(stack, name)
		}
	}
	return names
}

func stackContainerName(stack, service string) string {
	if service = catalogName(service); service == stack || strings.HasPrefix(service, stack+"-") {
		return service
	}
	return catalogName(stack + "-" + service)
}

func catalogLabel(name, text string) (label, hint string) {
	text = strings.Join(strings.Fields(text), " ")
	text, admin := strings.CutPrefix(text, "Admin only. ")
	label = text
	for _, at := range catalogLabelEnd.FindAllStringIndex(text, -1) {
		before, after, end := text[:at[0]], text[at[1]:], text[at[0]:at[1]]
		if end == " (" {
			if after = strings.TrimSuffix(after, "."); !strings.HasSuffix(after, ")") {
				continue
			}
			after = strings.TrimSuffix(after, ")")
		}
		if example, ok := strings.CutPrefix(end, ", "); ok {
			after = example + after
		}
		if after != "" {
			label, hint = before, sentence(after)
			break
		}
	}
	if label == "" || len(label) > 60 {
		label, hint = name, sentence(text)
	}
	if admin {
		hint = strings.TrimSpace(hint + " Admin only.")
	}
	return label, hint
}

func distinctLabels(variables []containerServiceInterfaces.CatalogVariable, originals []string) {
	groups := func() map[string][]int {
		byLabel := map[string][]int{}
		for i, variable := range variables {
			byLabel[variable.Label] = append(byLabel[variable.Label], i)
		}
		return byLabel
	}
	for _, group := range groups() {
		if slices.ContainsFunc(group, func(i int) bool { return originals[i] != originals[group[0]] }) {
			for _, i := range group {
				variables[i].Label, variables[i].Hint = cmp.Or(originals[i], variables[i].Name), ""
			}
		}
	}
	for _, group := range groups() {
		if len(group) < 2 {
			continue
		}
		protocols := map[string]bool{}
		for _, i := range group {
			protocols[variables[i].Protocol] = true
		}
		for _, i := range group {
			if len(protocols) == len(group) && variables[i].Protocol != "" {
				variables[i].Label += " (" + strings.ToUpper(variables[i].Protocol) + ")"
			} else {
				variables[i].Label += " (" + variables[i].Name + ")"
			}
		}
	}
}

func sentence(text string) string {
	first, size := utf8.DecodeRuneInString(text)
	if size == 0 {
		return ""
	}
	text = string(unicode.ToUpper(first)) + text[size:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

func catalogDefault(value string) string {
	if strings.HasPrefix(value, "/containers/") || strings.Contains(value, "{{") {
		return ""
	}
	return value
}

func generatedSecret(variable catalogVariable) bool {
	return variable.Type == "secret" && (variable.Generate == nil || *variable.Generate)
}

func (s *Service) RenderCatalogApp(ctx context.Context, source, id string, req containerServiceInterfaces.CatalogRenderRequest) (containerServiceInterfaces.CatalogPlan, error) {
	plan := containerServiceInterfaces.CatalogPlan{Containers: []containerServiceInterfaces.CreateRequest{}}
	app, manifest, err := s.catalogManifest(ctx, source, id)
	if err != nil {
		return plan, err
	}
	variables := slices.Clone(manifest.Fjord.Variables)
	var options []catalogOption
	for _, choice := range manifest.Fjord.Choices {
		selected := cmp.Or(req.Choices[choice.ID], choice.Default)
		index := slices.IndexFunc(choice.Options, func(option catalogOption) bool { return option.ID == selected })
		if index < 0 {
			return plan, fmt.Errorf("catalog_choice_not_found: %s=%s", choice.ID, selected)
		}
		options = append(options, choice.Options[index])
		variables = append(variables, choice.Options[index].Ask...)
	}
	if req.Variant != "" && !slices.ContainsFunc(app.Variants, func(variant containerServiceInterfaces.CatalogVariant) bool { return variant.ID == req.Variant }) {
		return plan, fmt.Errorf("catalog_variant_not_found: %s", req.Variant)
	}
	resolved, err := resolveServices(manifest, options)
	if err != nil {
		return plan, err
	}

	r := catalogRender{os: app.OS, values: map[string]string{}, types: map[string]string{}, optional: map[string]bool{},
		owners: map[string][2]string{}, directories: catalogDirectories(catalogManifest{Services: resolved.services})}
	for _, variable := range variables {
		r.values[variable.Name], r.types[variable.Name], r.optional[variable.Name] = catalogDefault(variable.Default), variable.Type, variable.Optional
		if permissions := variable.HostPermissions; permissions != nil {
			r.owners[variable.Name] = [2]string{fmt.Sprintf("%d:%d", permissions.UID, permissions.GID), permissions.Mode}
		}
	}
	var secrets, choiceEnv []string
	for _, option := range options {
		for _, name := range slices.Concat(slices.Collect(maps.Keys(option.Defaults)), slices.Collect(maps.Keys(option.Env))) {
			r.types[name] = cmp.Or(r.types[name], "string")
		}
		for _, name := range option.Secrets {
			r.types[name] = cmp.Or(r.types[name], "secret")
			secrets = append(secrets, name)
		}
	}
	given := map[string]bool{}
	for name, value := range req.Values {
		if _, ok := r.types[name]; ok {
			r.values[name], given[name] = value, value != ""
		}
	}
	for _, option := range options {
		for name, value := range option.Defaults {
			if !given[name] {
				r.values[name] = catalogDefault(value)
			}
		}
		for name, value := range option.Env {
			r.values[name] = value
			choiceEnv = append(choiceEnv, name)
		}
	}
	for _, variable := range variables {
		if generatedSecret(variable) {
			secrets = append(secrets, variable.Name)
		}
	}
	for _, name := range secrets {
		if r.values[name] == "" {
			r.values[name] = utils.GenerateRandomString(24)
		}
	}
	if len(resolved.services) > 1 {
		plan.Stack = catalogName(app.ID)
	}
	names := resolved.names(app.ID, plan.Stack, r.expand)
	for service, variable := range resolved.hostnames {
		if name, ok := names[service]; ok && plan.Stack != "" {
			r.values[variable], r.types[variable] = name, cmp.Or(r.types[variable], "string")
		}
	}
	for _, variable := range variables {
		_, directory := r.directories[variable.Name]
		if !variable.Optional && !directory && strings.TrimSpace(r.values[variable.Name]) == "" {
			label, _ := catalogLabel(variable.Name, variable.Label)
			return plan, fmt.Errorf("catalog_setting_required: %s", label)
		}
	}
	r.appOwner = catalogOwner(source, r.values)

	ordered := resolved.order()
	main := resolved.main(app.ID, manifest.Fjord.Networking, ordered)
	for _, name := range ordered {
		service := resolved.services[name]
		spec, err := r.spec(service)
		if err != nil {
			return plan, err
		}
		create := containerServiceInterfaces.CreateRequest{Name: names[name], Description: fmt.Sprintf("%s for %s", name, app.Name),
			Spec: spec, StartAfterCreate: true, CatalogApp: source + "/" + id, Stack: plan.Stack}
		switch {
		case plan.Stack != "":
			create.Network = plan.Stack
		case service.NetworkMode == "host":
			create.Network, create.Ports = containerServiceInterfaces.HostNetwork, nil
		}
		if len(service.EnvFile) > 0 {
			for _, setting := range slices.Sorted(maps.Keys(r.types)) {
				if value := r.values[setting]; value != "" && !slices.ContainsFunc(create.Env, func(env containerModels.EnvVar) bool { return env.Name == setting }) {
					create.Env = append(create.Env, containerModels.EnvVar{Name: setting, Value: value})
				}
			}
		}
		if name == main {
			r.finishMain(&create, app, manifest, req.Variant, choiceEnv)
		}
		plan.Containers = append(plan.Containers, create)
	}
	return plan, nil
}

type catalogRender struct {
	os          string
	values      map[string]string
	types       map[string]string
	optional    map[string]bool
	owners      map[string][2]string
	directories map[string]string
	appOwner    string
}

func (r catalogRender) expand(text string) string {
	return catalogVars.ReplaceAllStringFunc(text, func(match string) string {
		parts := catalogVars.FindStringSubmatch(match)
		if value := r.values[parts[1]]; value != "" {
			return value
		}
		return parts[2]
	})
}

func (r catalogRender) spec(service composeService) (containerServiceInterfaces.Spec, error) {
	spec := containerServiceInterfaces.Spec{Image: r.expand(service.Image), OS: r.os, Network: containerServiceInterfaces.DefaultNetwork,
		Restart: cmp.Or(service.Restart, "no"), StopTimeout: 10, Annotations: map[string]string{}}
	if grace, err := time.ParseDuration(service.StopGracePeriod); err == nil && grace > 0 {
		spec.StopTimeout = uint(grace.Seconds())
	}
	for _, entry := range service.Environment {
		name, value, _ := strings.Cut(entry, "=")
		if value = r.expand(value); value != "" {
			setEnv(&spec, name, value)
		}
	}
	for _, port := range service.Ports {
		mapping, ok := renderPort(port, r.expand)
		if !ok {
			return spec, fmt.Errorf("invalid_catalog_port: %s", cmp.Or(port.Short, port.Target))
		}
		if mapping.HostPort != 0 {
			spec.Ports = append(spec.Ports, mapping)
		}
	}

	used := map[string]bool{}
	for _, volume := range service.Volumes {
		raw, _, _ := strings.Cut(volume, "#")
		parts := strings.Split(strings.TrimSpace(raw), ":")
		if len(parts) < 2 {
			return spec, fmt.Errorf("invalid_catalog_volume: %s", volume)
		}
		variable := strings.TrimSuffix(strings.TrimPrefix(parts[0], "${"), "}")
		source, target := r.expand(parts[0]), r.expand(parts[1])
		mount := containerModels.Mount{Type: containerModels.MountVolume, Source: source, Target: target,
			ReadOnly: len(parts) > 2 && parts[2] == "ro"}
		switch {
		case source == "" && (r.optional[variable] || r.types[variable] == ""):
			continue
		case source == "":
			mount.Source, mount.Owner = volumeName(target, variable), cmp.Or(r.owners[variable][0], r.appOwner)
		case strings.HasPrefix(source, "/"):
			if info, err := os.Stat(source); variable == parts[0] && (err != nil || !info.IsDir()) {
				continue
			}
			mount.Type = containerModels.MountPath
		case strings.Contains(source, "/"):
			mount.Type = containerModels.MountDataset
		default:
			mount.Source = strings.TrimLeft(source, ".")
			mount.Owner, mount.Mode = cmp.Or(r.owners[variable][0], r.appOwner), r.owners[variable][1]
		}
		if mount.Type == containerModels.MountVolume {
			for name, i := mount.Source, 2; used[mount.Source]; i++ {
				mount.Source = fmt.Sprintf("%s-%d", name, i)
			}
			used[mount.Source] = true
		}
		spec.Mounts = append(spec.Mounts, mount)
	}

	for _, entry := range service.Annotations {
		key, value, _ := strings.Cut(entry, "=")
		spec.Annotations[key] = r.expand(value)
	}
	for _, host := range service.ExtraHosts {
		spec.ExtraHosts = append(spec.ExtraHosts, r.expand(host))
	}
	return spec, nil
}

func (r catalogRender) finishMain(create *containerServiceInterfaces.CreateRequest, app containerServiceInterfaces.CatalogApp,
	manifest catalogManifest, variant string, choiceEnv []string) {
	create.Description = app.Description
	if login := manifest.Fjord.Login; login != nil {
		create.Description += fmt.Sprintf("\n\nLogin: %s / %s", r.expand(login.User), r.expand(login.Password))
	}
	for _, candidate := range app.Variants {
		if candidate.ID == variant || variant == "" && candidate.Default {
			create.Image = candidate.Image
		}
	}
	for _, name := range choiceEnv {
		setEnv(&create.Spec, name, r.expand(r.values[name]))
	}
	if web, err := strconv.ParseUint(r.expand(manifest.Fjord.Info.WebPort), 10, 16); err == nil {
		create.WebPort, create.WebHTTPS = uint16(web), manifest.Fjord.Info.WebHTTPS
		if create.Stack != "" && len(create.Ports) == 0 {
			create.Ports = []containerModels.Port{{Protocol: "tcp", HostPort: uint16(web), ContainerPort: uint16(web)}}
		}
		for _, port := range create.Ports {
			if port.HostPort == uint16(web) {
				create.WebPort = port.ContainerPort
			}
		}
	}
}

func setEnv(spec *containerServiceInterfaces.Spec, name, value string) {
	if i := slices.IndexFunc(spec.Env, func(env containerModels.EnvVar) bool { return env.Name == name }); i >= 0 {
		spec.Env[i].Value = value
		return
	}
	spec.Env = append(spec.Env, containerModels.EnvVar{Name: name, Value: value})
}

func renderPort(port composePort, expand func(string) string) (containerModels.Port, bool) {
	mapping := containerModels.Port{Protocol: cmp.Or(port.Protocol, "tcp")}
	host, target := expand(port.Published), expand(port.Target)
	if port.Short != "" {
		short, protocol, _ := strings.Cut(expand(port.Short), "/")
		mapping.Protocol = cmp.Or(protocol, "tcp")
		parts := strings.Split(short, ":")
		target = parts[len(parts)-1]
		if host = target; len(parts) > 1 {
			host = parts[len(parts)-2]
		}
		if len(parts) > 2 {
			mapping.HostIP = strings.Join(parts[:len(parts)-2], ":")
		}
	}
	if host == "" {
		return mapping, true
	}
	hostPort, err := strconv.ParseUint(host, 10, 16)
	containerPort, err2 := strconv.ParseUint(target, 10, 16)
	mapping.HostPort, mapping.ContainerPort = uint16(hostPort), uint16(containerPort)
	return mapping, err == nil && err2 == nil && hostPort > 0 && containerPort > 0
}

func catalogDirectories(manifest catalogManifest) map[string]string {
	directories := map[string]string{}
	for _, service := range manifest.Services {
		for _, volume := range service.Volumes {
			raw, _, _ := strings.Cut(volume, "#")
			parts := strings.Split(strings.TrimSpace(raw), ":")
			if name, ok := strings.CutPrefix(parts[0], "${"); ok && len(parts) > 1 {
				directories[strings.TrimSuffix(name, "}")] = parts[1]
			}
		}
	}
	return directories
}

func catalogOwner(source string, values map[string]string) string {
	if _, err := strconv.Atoi(values["PUID"]); err == nil {
		if _, err := strconv.Atoi(values["PGID"]); err == nil {
			return values["PUID"] + ":" + values["PGID"]
		}
	}
	return catalogDefaultOwners[source]
}

func catalogPortProtocols(manifest catalogManifest) map[string]string {
	protocols := map[string]string{}
	for _, service := range manifest.Services {
		for _, port := range service.Ports {
			host, protocol := port.Published, cmp.Or(port.Protocol, "tcp")
			if port.Short != "" {
				mapping, short, _ := strings.Cut(port.Short, "/")
				parts := strings.Split(mapping, ":")
				host, protocol = parts[0], cmp.Or(short, "tcp")
				if len(parts) > 1 {
					host = parts[len(parts)-2]
				}
			}
			if name, ok := strings.CutPrefix(host, "${"); ok {
				protocols[strings.TrimSuffix(name, "}")] = protocol
			}
		}
	}
	return protocols
}

func volumeName(target, variable string) string {
	name := strings.TrimLeft(volumeNameRegex.ReplaceAllString(path.Base(target), "-"), "._-")
	if name == "" {
		name = strings.ToLower(variable)
	}
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

func catalogName(name string) string {
	name = strings.Trim(catalogNameRegex.ReplaceAllString(name, "-"), "-")
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}
