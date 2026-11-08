// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	storagezfs "github.com/alchemillahq/sylve/pkg/storage/zfs"
	"github.com/alchemillahq/sylve/pkg/utils"
)

// keptSecretsFile holds an install's secrets next to its data, so installing
// again over data that was kept reuses them: a database only opens with the
// password it was created with.
const keptSecretsFile = ".sylve-secrets.json"

// dataDirs returns the mountpoints of those of the datasets that exist.
func (s *Service) dataDirs(ctx context.Context, datasets ...string) []string {
	var dirs []string
	for _, name := range datasets {
		dataset, err := s.GZFS.ZFS.Get(ctx, name, false)
		if err != nil || dataset == nil {
			continue
		}
		if dir, err := storagezfs.FilesystemMountpoint(dataset); err == nil {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func readKeptSecrets(dirs []string) map[string]string {
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, keptSecretsFile))
		if err != nil {
			continue
		}
		var secrets map[string]string
		if json.Unmarshal(data, &secrets) == nil && len(secrets) > 0 {
			return secrets
		}
	}
	return nil
}

func writeKeptSecrets(dirs []string, secrets map[string]string) error {
	if len(secrets) == 0 {
		return nil
	}
	data, err := json.Marshal(secrets)
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if err := utils.AtomicWriteFile(filepath.Join(dir, keptSecretsFile), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// restoreSecrets puts kept secrets back in place of the ones this install
// made up, wherever those went, as a password also sits inside a database URL.
// Only values long enough not to turn up by chance are replaced, which made-up
// ones always are. It reports whether any was put back.
func restoreSecrets(creates []containerServiceInterfaces.CreateRequest, kept map[string]string) bool {
	var replacements []string
	for _, create := range creates {
		for name, made := range create.Secrets {
			if old, ok := kept[name]; ok && old != made && len(made) >= 16 {
				replacements = append(replacements, made, old)
			}
		}
	}
	if len(replacements) == 0 {
		return false
	}
	replacer := strings.NewReplacer(replacements...)
	for i := range creates {
		for j := range creates[i].Env {
			creates[i].Env[j].Value = replacer.Replace(creates[i].Env[j].Value)
		}
		for name, made := range creates[i].Secrets {
			creates[i].Secrets[name] = replacer.Replace(made)
		}
	}
	return true
}
