// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

const (
	jailAdditionalOptionsStart = "### These are user-defined additional options ###"
	jailAdditionalOptionsEnd   = "### End user-defined additional options ###"
	jailMetadataStart          = "### Start Sylve-Managed Metadata ###"
	jailMetadataEnd            = "### End Sylve-Managed Metadata ###"
	jailUserHookStart          = "### Start User-Managed Hook ###"
	jailUserHookEnd            = "### End User-Managed Hook ###"
)

type jailOptionHostOps interface {
	DevFSRulesPath() string
	ReloadDevFS() error
}

type hostJailOptionOps struct{}

func (hostJailOptionOps) DevFSRulesPath() string {
	return "/etc/devfs.rules"
}

func (hostJailOptionOps) ReloadDevFS() error {
	if _, err := utils.RunCommand("service", "devfs", "restart"); err != nil {
		return fmt.Errorf("devfs_service_unavailable: %w", err)
	}
	return nil
}

func (s *Service) jailOptionHostOps() jailOptionHostOps {
	if s != nil && s.optionOps != nil {
		return s.optionOps
	}
	return hostJailOptionOps{}
}

func loadJailForOptions(db *gorm.DB, ctID uint) (*jailModels.Jail, error) {
	var jail jailModels.Jail
	err := db.
		Preload("Storages").
		Preload("JailHooks").
		Where("ct_id = ?", ctID).
		First(&jail).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("jail_not_found: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("failed_to_load_jail: %w", err)
	}
	return &jail, nil
}

func (s *Service) ensureJailOptionMutationAllowedLocked(ctID uint) (*jailModels.Jail, error) {
	if ctID == 0 {
		return nil, fmt.Errorf("invalid_ct_id")
	}
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("jail_service_not_initialized")
	}
	restoring, err := s.jailRestoreInProgress(ctID)
	if err != nil {
		return nil, fmt.Errorf("restore_fence_check_failed: %w", err)
	}
	if restoring {
		return nil, fmt.Errorf("restore_in_progress")
	}
	allowed, err := s.canMutateProtectedJail(ctID)
	if err != nil {
		return nil, fmt.Errorf("replication_lease_check_failed: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("replication_lease_not_owned")
	}
	return loadJailForOptions(s.DB, ctID)
}

func (s *Service) mutateJailOption(
	ctID uint,
	mutation func(jail *jailModels.Jail) error,
) error {
	if s == nil {
		return fmt.Errorf("jail_service_not_initialized")
	}
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	jail, err := s.ensureJailOptionMutationAllowedLocked(ctID)
	if err != nil {
		return err
	}
	return mutation(jail)
}

func (s *Service) jailOptionJSONPath(jail *jailModels.Jail) (string, error) {
	if jail == nil {
		return "", fmt.Errorf("jail_not_found")
	}
	mountPoint, err := s.resolveJailRoot(context.Background(), jail)
	if err != nil {
		return "", err
	}
	return filepath.Join(mountPoint, ".sylve", "jail.json"), nil
}

type jailOptionPersistence struct {
	paths            []string
	writeFiles       func() error
	updateDatabase   func() error
	restoreDatabase  func() error
	finalize         func() error
	restoreFinalized func() error
}

func (s *Service) persistJailOptionMutation(
	jail *jailModels.Jail,
	persistence jailOptionPersistence,
) error {
	paths := append([]string{}, persistence.paths...)
	jsonPath, err := s.jailOptionJSONPath(jail)
	if err != nil {
		return err
	}
	paths = append(paths, jsonPath)
	snapshots, err := captureJailFiles(paths)
	if err != nil {
		return err
	}

	fail := func(primary error, databaseUpdated bool, finalizeAttempted bool) error {
		var databaseErr error
		if databaseUpdated && persistence.restoreDatabase != nil {
			databaseErr = persistence.restoreDatabase()
			if databaseErr != nil {
				databaseErr = fmt.Errorf("failed_to_restore_jail_option_database: %w", databaseErr)
			}
		}
		filesErr := restoreJailFiles(snapshots)
		var finalizeErr error
		if finalizeAttempted && persistence.restoreFinalized != nil {
			finalizeErr = persistence.restoreFinalized()
			if finalizeErr != nil {
				finalizeErr = fmt.Errorf("failed_to_restore_jail_option_runtime_state: %w", finalizeErr)
			}
		}
		return errors.Join(primary, databaseErr, filesErr, finalizeErr)
	}

	if persistence.writeFiles != nil {
		if err := persistence.writeFiles(); err != nil {
			return fail(err, false, false)
		}
	}
	databaseUpdated := false
	if persistence.updateDatabase != nil {
		if err := persistence.updateDatabase(); err != nil {
			return fail(err, false, false)
		}
		databaseUpdated = true
	}
	if err := s.WriteJailJSON(jail.CTID); err != nil {
		return fail(fmt.Errorf("failed_to_sync_jail_metadata: %w", err), databaseUpdated, false)
	}
	if persistence.finalize != nil {
		if err := persistence.finalize(); err != nil {
			return fail(err, databaseUpdated, true)
		}
	}
	return nil
}

func updateJailOptionColumns(db *gorm.DB, ctID uint, updates map[string]any) error {
	result := db.Model(&jailModels.Jail{}).Where("ct_id = ?", ctID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("jail_not_found")
	}
	return nil
}

func updateJailAllowedOptions(db *gorm.DB, ctID uint, options []string) error {
	result := db.Model(&jailModels.Jail{}).
		Where("ct_id = ?", ctID).
		Select("AllowedOptions").
		Updates(&jailModels.Jail{AllowedOptions: append([]string{}, options...)})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("jail_not_found")
	}
	return nil
}

func jailOptionConfigPath(ctID uint) (string, error) {
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return "", fmt.Errorf("failed_to_get_jails_path: %w", err)
	}
	return filepath.Join(jailsPath, fmt.Sprintf("%d", ctID), fmt.Sprintf("%d.conf", ctID)), nil
}

func (s *Service) loadJailOptionConfig(ctID uint) (string, string, error) {
	path, err := jailOptionConfigPath(ctID)
	if err != nil {
		return "", "", err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("jail_config_not_found: %w", err)
	}
	if err != nil {
		return "", "", fmt.Errorf("failed_to_read_jail_config: %w", err)
	}
	if strings.TrimSpace(string(content)) == "" {
		return "", "", fmt.Errorf("jail_config_not_found")
	}
	return path, string(content), nil
}

func normalizeJailConfigContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	for strings.Contains(content, "\n\n\n") {
		content = strings.ReplaceAll(content, "\n\n\n", "\n\n")
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content
}

func validateJailExecTimeout(execTimeout int) error {
	if execTimeout < jailModels.MinimumExecTimeoutSeconds ||
		execTimeout > jailModels.MaximumExecTimeoutSeconds {
		return fmt.Errorf(
			"exec_timeout_out_of_range: must be between %d and %d",
			jailModels.MinimumExecTimeoutSeconds,
			jailModels.MaximumExecTimeoutSeconds,
		)
	}
	return nil
}

func effectiveJailExecTimeout(execTimeout int) (int, error) {
	if execTimeout == 0 {
		execTimeout = jailModels.DefaultExecTimeoutSeconds
	}
	if err := validateJailExecTimeout(execTimeout); err != nil {
		return 0, err
	}
	return execTimeout, nil
}

func reconcileJailExecTimeoutConfig(content string, execTimeout int) (string, error) {
	execTimeout, err := effectiveJailExecTimeout(execTimeout)
	if err != nil {
		return "", err
	}

	lines := utils.SplitLines(content)
	result := make([]string, 0, len(lines))
	inAdditional := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == jailAdditionalOptionsStart {
			inAdditional = true
			result = append(result, line)
			continue
		}
		if trimmed == jailAdditionalOptionsEnd {
			inAdditional = false
			result = append(result, line)
			continue
		}
		if !inAdditional && strings.HasPrefix(trimmed, "exec.timeout") {
			if _, ok := jailConfigAssignmentValueWithTrailingComment(line, "exec.timeout"); !ok {
				return "", fmt.Errorf("jail_option_config_conflict: malformed exec.timeout assignment")
			}
			continue
		}
		result = append(result, line)
	}

	cleaned := normalizeJailConfigContent(strings.Join(result, "\n"))
	return insertJailConfigBlock(
		cleaned,
		fmt.Sprintf("\texec.timeout = %d;", execTimeout),
		false,
	)
}

func (s *Service) syncJailExecTimeoutConfig(jail *jailModels.Jail) error {
	if jail == nil {
		return fmt.Errorf("jail_not_found")
	}
	configPath, currentConfig, err := s.loadJailOptionConfig(jail.CTID)
	if err != nil {
		return err
	}
	nextConfig, err := reconcileJailExecTimeoutConfig(currentConfig, jail.ExecTimeout)
	if err != nil {
		return err
	}
	if nextConfig == currentConfig {
		return nil
	}
	if err := utils.AtomicWriteFile(configPath, []byte(nextConfig), 0o644); err != nil {
		return fmt.Errorf("failed_to_write_jail_config: %w", err)
	}
	return nil
}

func removeJailConfigRange(content string, start, end int) string {
	prefix := strings.TrimRight(content[:start], "\r\n")
	suffix := strings.TrimLeft(content[end:], "\r\n")
	switch {
	case prefix == "" && suffix == "":
		return ""
	case prefix == "":
		return suffix
	case suffix == "":
		return prefix + "\n"
	default:
		return prefix + "\n\n" + suffix
	}
}

func insertJailConfigBlock(content, block string, beforeAdditional bool) (string, error) {
	block = strings.Trim(block, "\r\n")
	if block == "" {
		return normalizeJailConfigContent(content), nil
	}
	lines := utils.SplitLines(content)
	insertAt := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if beforeAdditional && trimmed == jailAdditionalOptionsStart {
			insertAt = index
			break
		}
		if trimmed == "persist;" {
			insertAt = index
			break
		}
	}
	if insertAt == -1 {
		for index := len(lines) - 1; index >= 0; index-- {
			if strings.TrimSpace(lines[index]) == "}" {
				insertAt = index
				break
			}
		}
	}
	if insertAt == -1 {
		return "", fmt.Errorf("jail_option_config_conflict: closing jail block not found")
	}

	blockLines := utils.SplitLines(block)
	insert := make([]string, 0, len(blockLines)+2)
	if insertAt > 0 && strings.TrimSpace(lines[insertAt-1]) != "" {
		insert = append(insert, "")
	}
	insert = append(insert, blockLines...)
	if insertAt < len(lines) && strings.TrimSpace(lines[insertAt]) != "" {
		insert = append(insert, "")
	}
	lines = append(lines[:insertAt], append(insert, lines[insertAt:]...)...)
	return normalizeJailConfigContent(strings.Join(lines, "\n")), nil
}

func containsJailOptionMarker(value string) bool {
	for _, marker := range []string{
		jailAdditionalOptionsStart,
		jailAdditionalOptionsEnd,
		jailMetadataStart,
		jailMetadataEnd,
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func jailAdditionalOptionsConfigBlock(options string) string {
	if options == "" {
		return ""
	}
	return jailAdditionalOptionsStart + "\n" + options + "\n" + jailAdditionalOptionsEnd
}

func reconcileJailAdditionalOptionsBlock(content, prior, desired string) (string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	prior = strings.ReplaceAll(prior, "\r\n", "\n")
	desired = strings.ReplaceAll(desired, "\r\n", "\n")
	if desired != "" && containsJailOptionMarker(desired) {
		return "", fmt.Errorf("reserved_jail_option_marker")
	}

	startCount := strings.Count(content, jailAdditionalOptionsStart)
	endCount := strings.Count(content, jailAdditionalOptionsEnd)
	if startCount > 1 || endCount > 1 || (startCount == 0 && endCount != 0) {
		return "", fmt.Errorf("jail_option_config_conflict: malformed additional-options block")
	}

	cleaned := content
	if startCount == 1 {
		start := strings.Index(content, jailAdditionalOptionsStart)
		afterStart := start + len(jailAdditionalOptionsStart)
		if endCount == 1 {
			endRelative := strings.Index(content[afterStart:], jailAdditionalOptionsEnd)
			if endRelative < 0 {
				return "", fmt.Errorf("jail_option_config_conflict: malformed additional-options block")
			}
			end := afterStart + endRelative + len(jailAdditionalOptionsEnd)
			cleaned = removeJailConfigRange(content, start, end)
		} else {
			if prior == "" {
				return "", fmt.Errorf("jail_option_config_conflict: legacy additional-options value missing")
			}
			tail := strings.ReplaceAll(content[afterStart:], "\r\n", "\n")
			candidates := []string{
				strings.ReplaceAll(prior, "\r\n", "\n"),
				strings.TrimSpace(strings.ReplaceAll(prior, "\r\n", "\n")),
			}
			matchedEnd := -1
			for _, candidate := range candidates {
				if candidate == "" {
					continue
				}
				index := strings.Index(tail, candidate)
				if index >= 0 && strings.TrimSpace(tail[:index]) == "" {
					matchedEnd = afterStart + index + len(candidate)
					break
				}
			}
			if matchedEnd < 0 {
				return "", fmt.Errorf("jail_option_config_conflict: legacy additional-options block does not match database")
			}
			cleaned = removeJailConfigRange(content, start, matchedEnd)
		}
	} else if prior != "" {
		return "", fmt.Errorf("jail_option_config_conflict: additional-options block missing")
	}

	if desired == "" {
		return normalizeJailConfigContent(cleaned), nil
	}
	return insertJailConfigBlock(cleaned, jailAdditionalOptionsConfigBlock(desired), false)
}

func canonicalizeJailAdditionalOptions(content string, jail *jailModels.Jail) (string, error) {
	if jail == nil {
		return "", fmt.Errorf("jail_not_found")
	}
	return reconcileJailAdditionalOptionsBlock(content, jail.AdditionalOptions, jail.AdditionalOptions)
}

func jailConfigAssignmentValue(line, key string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, key) {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, key))
	if !strings.HasPrefix(rest, "=") || !strings.HasSuffix(rest, ";") {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(rest, "="), ";"))
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err == nil {
			return unquoted, true
		}
	}
	return value, true
}

func jailConfigAssignmentValueWithTrailingComment(line, key string) (string, bool) {
	if value, ok := jailConfigAssignmentValue(line, key); ok {
		return value, true
	}

	semicolon := strings.IndexByte(line, ';')
	if semicolon < 0 || !isJailConfigCommentTail(line[semicolon+1:]) {
		return "", false
	}
	return jailConfigAssignmentValue(line[:semicolon+1], key)
}

func jailConfigLifecycleAssignment(line, key string) (string, bool, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, key) {
		return "", false, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, key))
	appendValue := strings.HasPrefix(rest, "+=")
	if appendValue {
		rest = "=" + strings.TrimPrefix(rest, "+=")
	} else if !strings.HasPrefix(rest, "=") {
		return "", false, false
	}
	value, ok := jailConfigAssignmentValueWithTrailingComment(key+" "+rest, key)
	return value, appendValue, ok
}

func isJailConfigCommentTail(tail string) bool {
	tail = strings.TrimSpace(tail)
	for tail != "" {
		if strings.HasPrefix(tail, "#") || strings.HasPrefix(tail, "//") {
			return true
		}
		if !strings.HasPrefix(tail, "/*") {
			return false
		}
		block := tail[2:]
		end := strings.Index(block, "*/")
		if end < 0 {
			return false
		}
		tail = strings.TrimSpace(block[end+2:])
	}
	return true
}

func reconcileJailFstabConfig(content, fstabPath string, enabled bool) (string, error) {
	lines := utils.SplitLines(content)
	result := make([]string, 0, len(lines))
	inAdditional := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == jailAdditionalOptionsStart {
			inAdditional = true
			result = append(result, line)
			continue
		}
		if trimmed == jailAdditionalOptionsEnd {
			inAdditional = false
			result = append(result, line)
			continue
		}
		if !inAdditional {
			if value, ok := jailConfigAssignmentValue(line, "mount.fstab"); ok && value == fstabPath {
				continue
			}
		}
		result = append(result, line)
	}
	cleaned := normalizeJailConfigContent(strings.Join(result, "\n"))
	if !enabled {
		return cleaned, nil
	}
	return insertJailConfigBlock(cleaned, fmt.Sprintf("\tmount.fstab = %s;", strconv.Quote(fstabPath)), true)
}

func managedDevFSRulesetLine(line string, ctID uint) bool {
	value, ok := jailConfigAssignmentValue(line, "devfs_ruleset")
	if !ok {
		return false
	}
	return value == "61181" || value == strconv.FormatUint(uint64(ctID), 10)
}

func reconcileJailDevFSConfig(
	content string,
	ctID uint,
	allowedOptions []string,
	rules string,
) (string, error) {
	lines := utils.SplitLines(content)
	result := make([]string, 0, len(lines))
	inAdditional := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == jailAdditionalOptionsStart {
			inAdditional = true
			result = append(result, line)
			continue
		}
		if trimmed == jailAdditionalOptionsEnd {
			inAdditional = false
			result = append(result, line)
			continue
		}
		if !inAdditional && managedDevFSRulesetLine(line, ctID) {
			continue
		}
		result = append(result, line)
	}
	cleaned := normalizeJailConfigContent(strings.Join(result, "\n"))
	if !slices.Contains(allowedOptions, "allow.mount.devfs") {
		return cleaned, nil
	}
	ruleset := uint(61181)
	if strings.TrimSpace(rules) != "" {
		ruleset = ctID
	}
	return insertJailConfigBlock(
		cleaned,
		fmt.Sprintf("\tdevfs_ruleset=%d;", ruleset),
		true,
	)
}

func removeDevFSRulesBlock(content string, ctID uint) string {
	lines := utils.SplitLines(content)
	headerPrefix := fmt.Sprintf("[devfsrules_jails_sylve_%d=", ctID)
	inBlock := false
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inBlock && strings.HasPrefix(trimmed, headerPrefix) && strings.HasSuffix(trimmed, "]") {
			inBlock = true
			continue
		}
		if inBlock && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inBlock = false
			result = append(result, line)
			continue
		}
		if !inBlock {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

func reconcileDevFSRulesFile(content string, ctID uint, rules string) string {
	cleaned := strings.TrimRight(removeDevFSRulesBlock(content, ctID), "\r\n")
	if strings.TrimSpace(rules) == "" {
		return cleaned + "\n"
	}
	block := fmt.Sprintf(
		"[devfsrules_jails_sylve_%d=%d]\nadd include $devfsrules_jails\n%s",
		ctID,
		ctID,
		strings.TrimSpace(rules),
	)
	return cleaned + "\n\n" + block + "\n"
}

func isManagedAllowedOptionLine(trimmedLine string) bool {
	if !strings.HasPrefix(trimmedLine, "allow.") || !strings.HasSuffix(trimmedLine, ";") {
		return false
	}
	option := strings.TrimSuffix(trimmedLine, ";")
	return utils.IsValidJailAllowedOpts([]string{option})
}

func normalizeJailAllowedOptions(options []string) ([]string, error) {
	normalized := make([]string, 0, len(options))
	seen := make(map[string]struct{}, len(options))
	for _, option := range options {
		trimmed := strings.TrimSpace(option)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if !utils.IsValidJailAllowedOpts(normalized) {
		return nil, fmt.Errorf("invalid_jail_allowed_options")
	}
	if config.IsDevFSDisabled() && slices.Contains(normalized, "allow.mount.devfs") {
		return nil, fmt.Errorf("devfs_management_disabled")
	}
	return normalized, nil
}

func reconcileJailAllowedOptionsConfig(
	content string,
	ctID uint,
	devFSRules string,
	options []string,
) (string, error) {
	lines := utils.SplitLines(content)
	result := make([]string, 0, len(lines))
	inAdditional := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == jailAdditionalOptionsStart {
			inAdditional = true
			result = append(result, line)
			continue
		}
		if trimmed == jailAdditionalOptionsEnd {
			inAdditional = false
			result = append(result, line)
			continue
		}
		if !inAdditional {
			if isManagedAllowedOptionLine(trimmed) || trimmed == "mount.devfs;" || managedDevFSRulesetLine(line, ctID) {
				continue
			}
		}
		result = append(result, line)
	}
	cleaned := normalizeJailConfigContent(strings.Join(result, "\n"))
	if len(options) == 0 {
		return cleaned, nil
	}

	block := make([]string, 0, len(options)+2)
	for _, option := range options {
		block = append(block, fmt.Sprintf("\t%s;", option))
	}
	if slices.Contains(options, "allow.mount.devfs") {
		block = append(block, "\tmount.devfs;")
		if strings.TrimSpace(devFSRules) == "" {
			block = append(block, "\tdevfs_ruleset=61181;")
		} else {
			block = append(block, fmt.Sprintf("\tdevfs_ruleset=%d;", ctID))
		}
	}
	return insertJailConfigBlock(cleaned, strings.Join(block, "\n"), true)
}

func validateJailMetadataValue(value string) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("invalid_jail_metadata")
	}
	return nil
}

func validateJailMetadata(meta, env string) error {
	if err := validateJailMetadataValue(meta); err != nil {
		return err
	}
	return validateJailMetadataValue(env)
}

func jailMetadataConfigBlock(meta, env string) string {
	lines := []string{jailMetadataStart}
	if meta != "" {
		lines = append(lines, fmt.Sprintf("\tmeta = %s;", strconv.Quote(meta)))
	}
	if env != "" {
		lines = append(lines, fmt.Sprintf("\tenv = %s;", strconv.Quote(env)))
	}
	lines = append(lines, jailMetadataEnd)
	if len(lines) == 2 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func reconcileJailMetadataConfig(content, meta, env string) (string, error) {
	lines := utils.SplitLines(content)
	result := make([]string, 0, len(lines))
	inAdditional := false
	inMetadata := false
	metadataBlocks := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == jailAdditionalOptionsStart {
			inAdditional = true
			result = append(result, line)
			continue
		}
		if trimmed == jailAdditionalOptionsEnd {
			inAdditional = false
			result = append(result, line)
			continue
		}
		if !inAdditional && trimmed == jailMetadataStart {
			if inMetadata {
				return "", fmt.Errorf("jail_option_config_conflict: nested metadata block")
			}
			metadataBlocks++
			if metadataBlocks > 1 {
				return "", fmt.Errorf("jail_option_config_conflict: duplicate metadata block")
			}
			inMetadata = true
			continue
		}
		if !inAdditional && trimmed == jailMetadataEnd {
			if !inMetadata {
				return "", fmt.Errorf("jail_option_config_conflict: unmatched metadata marker")
			}
			inMetadata = false
			continue
		}
		if inMetadata {
			continue
		}
		if !inAdditional {
			if _, ok := jailConfigAssignmentValue(line, "meta"); ok {
				continue
			}
			if _, ok := jailConfigAssignmentValue(line, "env"); ok {
				continue
			}
		}
		result = append(result, line)
	}
	if inMetadata {
		return "", fmt.Errorf("jail_option_config_conflict: unterminated metadata block")
	}
	cleaned := normalizeJailConfigContent(strings.Join(result, "\n"))
	return insertJailConfigBlock(cleaned, jailMetadataConfigBlock(meta, env), true)
}

func (s *Service) hasHookBody(content string) bool {
	for index, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if index == 0 && strings.HasPrefix(trimmed, "#!") {
			continue
		}
		if trimmed != "" {
			return true
		}
	}
	return false
}

func validateLifecycleHooks(hooks jailServiceInterfaces.Hooks) error {
	for _, hook := range []jailServiceInterfaces.HookPhase{
		hooks.Prestart,
		hooks.Start,
		hooks.Poststart,
		hooks.Prestop,
		hooks.Stop,
		hooks.Poststop,
	} {
		if hook.Enabled && strings.TrimSpace(hook.Script) == "" {
			return fmt.Errorf("lifecycle_hook_script_required")
		}
		if strings.Contains(hook.Script, jailUserHookStart) || strings.Contains(hook.Script, jailUserHookEnd) {
			return fmt.Errorf("reserved_jail_option_marker")
		}
	}
	return nil
}

func (s *Service) reconcileUserManagedHook(
	content string,
	hook jailServiceInterfaces.HookPhase,
) (string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = s.ensureShebang(content)
	startCount := strings.Count(content, jailUserHookStart)
	endCount := strings.Count(content, jailUserHookEnd)
	if startCount > 1 || endCount > 1 || startCount != endCount {
		return "", fmt.Errorf("jail_lifecycle_hook_conflict: malformed user-managed hook block")
	}
	if startCount == 1 {
		start := strings.Index(content, jailUserHookStart)
		endRelative := strings.Index(content[start+len(jailUserHookStart):], jailUserHookEnd)
		if endRelative < 0 {
			return "", fmt.Errorf("jail_lifecycle_hook_conflict: unterminated user-managed hook block")
		}
		end := start + len(jailUserHookStart) + endRelative + len(jailUserHookEnd)
		content = removeJailConfigRange(content, start, end)
	}
	content = s.ensureShebang(content)
	if !hook.Enabled {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content, nil
	}
	base := strings.TrimRight(content, "\r\n")
	script := strings.TrimRight(hook.Script, "\r\n")
	return base + "\n\n" + jailUserHookStart + "\n" + script + "\n" + jailUserHookEnd + "\n", nil
}

type hookEditTarget struct {
	phase       jailModels.JailHookPhase
	execKey     string
	execPath    string
	hostPath    string
	inJailPath  string
	hookPayload jailServiceInterfaces.HookPhase
}

func managedLifecycleExecLine(line string, target hookEditTarget) bool {
	value, _, ok := jailConfigLifecycleAssignment(line, target.execKey)
	return ok && value == target.execPath
}

func reconcileJailLifecycleConfig(
	content string,
	jailType jailModels.JailType,
	targets []hookEditTarget,
	shouldWire map[jailModels.JailHookPhase]bool,
) (string, error) {
	lines := utils.SplitLines(content)
	result := make([]string, 0, len(lines))
	inAdditional := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == jailAdditionalOptionsStart {
			inAdditional = true
			result = append(result, line)
			continue
		}
		if trimmed == jailAdditionalOptionsEnd {
			inAdditional = false
			result = append(result, line)
			continue
		}
		remove := false
		if !inAdditional {
			for _, target := range targets {
				if managedLifecycleExecLine(line, target) {
					remove = true
					break
				}
			}
			if !remove && jailType == jailModels.JailTypeFreeBSD {
				if value, _, ok := jailConfigLifecycleAssignment(line, "exec.start"); ok && value == defaultFreeBSDJailStartCommand {
					remove = true
				}
				if value, _, ok := jailConfigLifecycleAssignment(line, "exec.stop"); ok && value == defaultFreeBSDJailStopCommand {
					remove = true
				}
			}
		}
		if !remove {
			result = append(result, line)
		}
	}
	cleaned := normalizeJailConfigContent(strings.Join(result, "\n"))
	execLines := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.phase == jailModels.JailHookPhaseStart || target.phase == jailModels.JailHookPhaseStop {
			continue
		}
		if shouldWire[target.phase] {
			execLines = append(execLines, fmt.Sprintf("\t%s += %s;", target.execKey, strconv.Quote(target.execPath)))
		}
	}
	execLines = append(execLines, canonicalJailStartStopExecLines(
		jailType,
		shouldWire[jailModels.JailHookPhaseStart],
		shouldWire[jailModels.JailHookPhaseStop],
	)...)
	return insertJailConfigBlock(cleaned, strings.Join(execLines, "\n"), true)
}

func replaceJailHookRows(db *gorm.DB, jailID uint, hooks []jailModels.JailHooks) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("jid = ?", jailID).Delete(&jailModels.JailHooks{}).Error; err != nil {
			return err
		}
		if len(hooks) == 0 {
			return nil
		}
		copies := make([]jailModels.JailHooks, len(hooks))
		copy(copies, hooks)
		for index := range copies {
			copies[index].JailID = jailID
		}
		return tx.Create(&copies).Error
	})
}

func lifecycleHookRows(jailID uint, hooks jailServiceInterfaces.Hooks) []jailModels.JailHooks {
	return []jailModels.JailHooks{
		{JailID: jailID, Phase: jailModels.JailHookPhasePreStart, Enabled: hooks.Prestart.Enabled, Script: hooks.Prestart.Script},
		{JailID: jailID, Phase: jailModels.JailHookPhaseStart, Enabled: hooks.Start.Enabled, Script: hooks.Start.Script},
		{JailID: jailID, Phase: jailModels.JailHookPhasePostStart, Enabled: hooks.Poststart.Enabled, Script: hooks.Poststart.Script},
		{JailID: jailID, Phase: jailModels.JailHookPhasePreStop, Enabled: hooks.Prestop.Enabled, Script: hooks.Prestop.Script},
		{JailID: jailID, Phase: jailModels.JailHookPhaseStop, Enabled: hooks.Stop.Enabled, Script: hooks.Stop.Script},
		{JailID: jailID, Phase: jailModels.JailHookPhasePostStop, Enabled: hooks.Poststop.Enabled, Script: hooks.Poststop.Script},
	}
}

func (s *Service) ModifyExecutionTimeout(ctID uint, execTimeout int) error {
	if err := validateJailExecTimeout(execTimeout); err != nil {
		return err
	}
	return s.mutateJailOption(ctID, func(jail *jailModels.Jail) error {
		configPath, currentConfig, err := s.loadJailOptionConfig(ctID)
		if err != nil {
			return err
		}
		currentConfig, err = canonicalizeJailAdditionalOptions(currentConfig, jail)
		if err != nil {
			return err
		}
		nextConfig, err := reconcileJailExecTimeoutConfig(currentConfig, execTimeout)
		if err != nil {
			return err
		}
		prior := jail.ExecTimeout
		return s.persistJailOptionMutation(jail, jailOptionPersistence{
			paths: []string{configPath},
			writeFiles: func() error {
				if err := utils.AtomicWriteFile(configPath, []byte(nextConfig), 0o644); err != nil {
					return fmt.Errorf("failed_to_write_jail_config: %w", err)
				}
				return nil
			},
			updateDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"exec_timeout": execTimeout})
			},
			restoreDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"exec_timeout": prior})
			},
		})
	})
}

func (s *Service) ModifyBootOrder(ctID uint, startAtBoot bool, bootOrder int) error {
	if bootOrder < 0 {
		return fmt.Errorf("start_order_must_be_greater_than_or_equal_to_0")
	}
	return s.mutateJailOption(ctID, func(jail *jailModels.Jail) error {
		priorStartAtBoot := jail.StartAtBoot
		priorBootOrder := jail.StartOrder
		return s.persistJailOptionMutation(jail, jailOptionPersistence{
			updateDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{
					"start_at_boot": startAtBoot,
					"start_order":   bootOrder,
				})
			},
			restoreDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{
					"start_at_boot": priorStartAtBoot,
					"start_order":   priorBootOrder,
				})
			},
		})
	})
}

func (s *Service) ModifyWakeOnLan(ctID uint, enabled bool) error {
	return s.mutateJailOption(ctID, func(jail *jailModels.Jail) error {
		prior := jail.WoL
		return s.persistJailOptionMutation(jail, jailOptionPersistence{
			updateDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"wo_l": enabled})
			},
			restoreDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"wo_l": prior})
			},
		})
	})
}

func (s *Service) ModifyFstab(ctID uint, fstab string) error {
	return s.mutateJailOption(ctID, func(jail *jailModels.Jail) error {
		configPath, currentConfig, err := s.loadJailOptionConfig(ctID)
		if err != nil {
			return err
		}
		currentConfig, err = canonicalizeJailAdditionalOptions(currentConfig, jail)
		if err != nil {
			return err
		}
		jailDir := filepath.Dir(configPath)
		fstabPath := filepath.Join(jailDir, "fstab")
		nextConfig, err := reconcileJailFstabConfig(currentConfig, fstabPath, fstab != "")
		if err != nil {
			return err
		}
		prior := jail.Fstab
		return s.persistJailOptionMutation(jail, jailOptionPersistence{
			paths: []string{configPath, fstabPath},
			writeFiles: func() error {
				if fstab == "" {
					if err := utils.DeleteFileIfExists(fstabPath); err != nil {
						return fmt.Errorf("failed_to_delete_fstab_file: %w", err)
					}
				} else if err := utils.AtomicWriteFile(fstabPath, []byte(fstab), 0o644); err != nil {
					return fmt.Errorf("failed_to_write_fstab_file: %w", err)
				}
				if err := utils.AtomicWriteFile(configPath, []byte(nextConfig), 0o644); err != nil {
					return fmt.Errorf("failed_to_write_jail_config: %w", err)
				}
				return nil
			},
			updateDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"fstab": fstab})
			},
			restoreDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"fstab": prior})
			},
		})
	})
}

func (s *Service) ModifyResolvConf(ctID uint, resolvConf string) error {
	return s.mutateJailOption(ctID, func(jail *jailModels.Jail) error {
		_, _, err := s.loadJailOptionConfig(ctID)
		if err != nil {
			return err
		}
		mountPoint, err := s.GetJailBaseMountPoint(ctID)
		if err != nil {
			return fmt.Errorf("failed_to_get_jail_mount_point: %w", err)
		}
		resolvPath := filepath.Join(mountPoint, "etc", "resolv.conf")
		prior := jail.ResolvConf
		return s.persistJailOptionMutation(jail, jailOptionPersistence{
			paths: []string{resolvPath},
			writeFiles: func() error {
				if resolvConf == "" {
					if err := utils.DeleteFileIfExists(resolvPath); err != nil {
						return fmt.Errorf("failed_to_delete_resolv_conf_file: %w", err)
					}
					return nil
				}
				if err := os.MkdirAll(filepath.Dir(resolvPath), 0o755); err != nil {
					return fmt.Errorf("failed_to_prepare_resolv_conf_path: %w", err)
				}
				if err := utils.AtomicWriteFile(resolvPath, []byte(resolvConf), 0o644); err != nil {
					return fmt.Errorf("failed_to_write_resolv_conf_file: %w", err)
				}
				return nil
			},
			updateDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"resolv_conf": resolvConf})
			},
			restoreDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"resolv_conf": prior})
			},
		})
	})
}

func (s *Service) ModifyDevfsRuleset(ctID uint, rules string) error {
	if config.IsDevFSDisabled() {
		return fmt.Errorf("devfs_management_disabled")
	}
	return s.mutateJailOption(ctID, func(jail *jailModels.Jail) error {
		configPath, currentConfig, err := s.loadJailOptionConfig(ctID)
		if err != nil {
			return err
		}
		currentConfig, err = canonicalizeJailAdditionalOptions(currentConfig, jail)
		if err != nil {
			return err
		}
		nextConfig, err := reconcileJailDevFSConfig(currentConfig, ctID, jail.AllowedOptions, rules)
		if err != nil {
			return err
		}
		ops := s.jailOptionHostOps()
		devFSPath := ops.DevFSRulesPath()
		currentDevFS, err := os.ReadFile(devFSPath)
		if err != nil {
			return fmt.Errorf("failed_to_read_devfs_rules: %w", err)
		}
		nextDevFS := reconcileDevFSRulesFile(string(currentDevFS), ctID, rules)
		devFSChanged := nextDevFS != string(currentDevFS)
		prior := jail.DevFSRuleset
		return s.persistJailOptionMutation(jail, jailOptionPersistence{
			paths: []string{configPath, devFSPath},
			writeFiles: func() error {
				if devFSChanged {
					if err := utils.AtomicWriteFile(devFSPath, []byte(nextDevFS), 0o644); err != nil {
						return fmt.Errorf("failed_to_write_devfs_rules: %w", err)
					}
				}
				if err := utils.AtomicWriteFile(configPath, []byte(nextConfig), 0o644); err != nil {
					return fmt.Errorf("failed_to_write_jail_config: %w", err)
				}
				return nil
			},
			updateDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"dev_fs_ruleset": rules})
			},
			restoreDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"dev_fs_ruleset": prior})
			},
			finalize: func() error {
				if !devFSChanged {
					return nil
				}
				return ops.ReloadDevFS()
			},
			restoreFinalized: func() error {
				if !devFSChanged {
					return nil
				}
				return ops.ReloadDevFS()
			},
		})
	})
}

func (s *Service) ModifyAdditionalOptions(ctID uint, options string) error {
	if options != "" && containsJailOptionMarker(options) {
		return fmt.Errorf("reserved_jail_option_marker")
	}
	return s.mutateJailOption(ctID, func(jail *jailModels.Jail) error {
		configPath, currentConfig, err := s.loadJailOptionConfig(ctID)
		if err != nil {
			return err
		}
		nextConfig, err := reconcileJailAdditionalOptionsBlock(currentConfig, jail.AdditionalOptions, options)
		if err != nil {
			return err
		}
		prior := jail.AdditionalOptions
		return s.persistJailOptionMutation(jail, jailOptionPersistence{
			paths: []string{configPath},
			writeFiles: func() error {
				if err := utils.AtomicWriteFile(configPath, []byte(nextConfig), 0o644); err != nil {
					return fmt.Errorf("failed_to_write_jail_config: %w", err)
				}
				return nil
			},
			updateDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"additional_options": options})
			},
			restoreDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{"additional_options": prior})
			},
		})
	})
}

func (s *Service) ModifyAllowedOptions(ctID uint, options []string) error {
	normalized, err := normalizeJailAllowedOptions(options)
	if err != nil {
		return err
	}
	return s.mutateJailOption(ctID, func(jail *jailModels.Jail) error {
		configPath, currentConfig, err := s.loadJailOptionConfig(ctID)
		if err != nil {
			return err
		}
		currentConfig, err = canonicalizeJailAdditionalOptions(currentConfig, jail)
		if err != nil {
			return err
		}
		nextConfig, err := reconcileJailAllowedOptionsConfig(currentConfig, ctID, jail.DevFSRuleset, normalized)
		if err != nil {
			return err
		}
		prior := append([]string{}, jail.AllowedOptions...)
		return s.persistJailOptionMutation(jail, jailOptionPersistence{
			paths: []string{configPath},
			writeFiles: func() error {
				if err := utils.AtomicWriteFile(configPath, []byte(nextConfig), 0o644); err != nil {
					return fmt.Errorf("failed_to_write_jail_config: %w", err)
				}
				return nil
			},
			updateDatabase: func() error {
				return updateJailAllowedOptions(s.DB, ctID, normalized)
			},
			restoreDatabase: func() error {
				return updateJailAllowedOptions(s.DB, ctID, prior)
			},
		})
	})
}

func (s *Service) ModifyMetadata(ctID uint, meta, env string) error {
	if err := validateJailMetadata(meta, env); err != nil {
		return err
	}
	return s.mutateJailOption(ctID, func(jail *jailModels.Jail) error {
		configPath, currentConfig, err := s.loadJailOptionConfig(ctID)
		if err != nil {
			return err
		}
		currentConfig, err = canonicalizeJailAdditionalOptions(currentConfig, jail)
		if err != nil {
			return err
		}
		nextConfig, err := reconcileJailMetadataConfig(currentConfig, meta, env)
		if err != nil {
			return err
		}
		priorMeta := jail.MetadataMeta
		priorEnv := jail.MetadataEnv
		return s.persistJailOptionMutation(jail, jailOptionPersistence{
			paths: []string{configPath},
			writeFiles: func() error {
				if err := utils.AtomicWriteFile(configPath, []byte(nextConfig), 0o644); err != nil {
					return fmt.Errorf("failed_to_write_jail_config: %w", err)
				}
				return nil
			},
			updateDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{
					"metadata_meta": meta,
					"metadata_env":  env,
				})
			},
			restoreDatabase: func() error {
				return updateJailOptionColumns(s.DB, ctID, map[string]any{
					"metadata_meta": priorMeta,
					"metadata_env":  priorEnv,
				})
			},
		})
	})
}

type jailHookFileWrite struct {
	path    string
	content string
}

func (s *Service) ModifyLifecycleHooks(ctID uint, hooks jailServiceInterfaces.Hooks) error {
	if err := validateLifecycleHooks(hooks); err != nil {
		return err
	}
	return s.mutateJailOption(ctID, func(jail *jailModels.Jail) error {
		hooks = normalizeLifecycleHookPayload(jail.Type, hooks)
		configPath, currentConfig, err := s.loadJailOptionConfig(ctID)
		if err != nil {
			return err
		}
		currentConfig, err = canonicalizeJailAdditionalOptions(currentConfig, jail)
		if err != nil {
			return err
		}

		jailsPath, err := config.GetJailsPath()
		if err != nil {
			return fmt.Errorf("failed_to_get_jails_path: %w", err)
		}
		jailDir := filepath.Join(jailsPath, strconv.FormatUint(uint64(ctID), 10))
		hostScriptsDir := filepath.Join(jailDir, "scripts")
		mountPoint, err := s.GetJailBaseMountPoint(ctID)
		if err != nil {
			return fmt.Errorf("failed_to_get_jail_mount_point: %w", err)
		}
		inJailScriptsDir := filepath.Join(mountPoint, "usr", "local", "sylve", "scripts")

		targets := []hookEditTarget{
			{phase: jailModels.JailHookPhasePreStart, execKey: "exec.prestart", execPath: filepath.Join(hostScriptsDir, "pre-start.sh"), hostPath: filepath.Join(hostScriptsDir, "pre-start.sh"), hookPayload: hooks.Prestart},
			{phase: jailModels.JailHookPhaseStart, execKey: "exec.start", execPath: jailStartHookExecPath, hostPath: filepath.Join(hostScriptsDir, "start.sh"), inJailPath: filepath.Join(inJailScriptsDir, "start.sh"), hookPayload: hooks.Start},
			{phase: jailModels.JailHookPhasePostStart, execKey: "exec.poststart", execPath: filepath.Join(hostScriptsDir, "post-start.sh"), hostPath: filepath.Join(hostScriptsDir, "post-start.sh"), hookPayload: hooks.Poststart},
			{phase: jailModels.JailHookPhasePreStop, execKey: "exec.prestop", execPath: filepath.Join(hostScriptsDir, "pre-stop.sh"), hostPath: filepath.Join(hostScriptsDir, "pre-stop.sh"), hookPayload: hooks.Prestop},
			{phase: jailModels.JailHookPhaseStop, execKey: "exec.stop", execPath: jailStopHookExecPath, hostPath: filepath.Join(hostScriptsDir, "stop.sh"), inJailPath: filepath.Join(inJailScriptsDir, "stop.sh"), hookPayload: hooks.Stop},
			{phase: jailModels.JailHookPhasePostStop, execKey: "exec.poststop", execPath: filepath.Join(hostScriptsDir, "post-stop.sh"), hostPath: filepath.Join(hostScriptsDir, "post-stop.sh"), hookPayload: hooks.Poststop},
		}

		writes := make([]jailHookFileWrite, 0, len(targets)+2)
		shouldWire := make(map[jailModels.JailHookPhase]bool, len(targets))
		paths := []string{configPath}
		for _, target := range targets {
			current, readErr := os.ReadFile(target.hostPath)
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				return fmt.Errorf("failed_to_read_hook_script(%s): %w", target.phase, readErr)
			}
			if errors.Is(readErr, os.ErrNotExist) {
				current = []byte("#!/bin/sh\n")
			}
			next, err := s.reconcileUserManagedHook(string(current), target.hookPayload)
			if err != nil {
				return fmt.Errorf("%s: %w", target.phase, err)
			}
			writes = append(writes, jailHookFileWrite{path: target.hostPath, content: next})
			paths = append(paths, target.hostPath)
			if target.inJailPath != "" {
				writes = append(writes, jailHookFileWrite{path: target.inJailPath, content: next})
				paths = append(paths, target.inJailPath)
			}
			shouldWire[target.phase] = s.hasHookBody(next)
		}

		nextConfig, err := reconcileJailLifecycleConfig(currentConfig, jail.Type, targets, shouldWire)
		if err != nil {
			return err
		}
		priorHooks := make([]jailModels.JailHooks, len(jail.JailHooks))
		copy(priorHooks, jail.JailHooks)
		desiredHooks := lifecycleHookRows(jail.ID, hooks)

		return s.persistJailOptionMutation(jail, jailOptionPersistence{
			paths: paths,
			writeFiles: func() error {
				if err := os.MkdirAll(hostScriptsDir, 0o755); err != nil {
					return fmt.Errorf("failed_to_create_host_scripts_dir: %w", err)
				}
				if err := os.MkdirAll(inJailScriptsDir, 0o755); err != nil {
					return fmt.Errorf("failed_to_create_in_jail_scripts_dir: %w", err)
				}
				for _, write := range writes {
					if err := utils.AtomicWriteFile(write.path, []byte(write.content), 0o755); err != nil {
						return fmt.Errorf("failed_to_write_hook_script(%s): %w", write.path, err)
					}
				}
				if err := utils.AtomicWriteFile(configPath, []byte(nextConfig), 0o644); err != nil {
					return fmt.Errorf("failed_to_write_jail_config: %w", err)
				}
				return nil
			},
			updateDatabase: func() error {
				return replaceJailHookRows(s.DB, jail.ID, desiredHooks)
			},
			restoreDatabase: func() error {
				return replaceJailHookRows(s.DB, jail.ID, priorHooks)
			},
		})
	})
}
