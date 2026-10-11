// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
)

const maxKeytabBytes = 1024 * 1024

type keytabEntry struct {
	principal string
	kvno      uint32
	enctype   uint16
	raw       []byte
}

// MIT's version-2 keytab container is also used by Heimdal. Parsing the
// container lets us inspect and merge it without printing any key material.
func parseKeytab(data []byte) ([]keytabEntry, error) {
	if len(data) < 2 || len(data) > maxKeytabBytes || data[0] != 5 || data[1] != 2 {
		return nil, invalid("expected a version-2 Kerberos keytab (maximum 1 MiB)")
	}
	r := bytes.NewReader(data[2:])
	entries := []keytabEntry{}
	for r.Len() > 0 {
		var size int32
		if err := binary.Read(r, binary.BigEndian, &size); err != nil {
			return nil, invalid("truncated keytab entry")
		}
		length := int64(size)
		if length < 0 {
			length = -length
		}
		if length > int64(r.Len()) {
			return nil, invalid("truncated keytab entry")
		}
		if size <= 0 {
			_, _ = r.Seek(length, io.SeekCurrent)
			continue
		}
		raw := make([]byte, int(size))
		_, _ = io.ReadFull(r, raw)
		e, err := parseKeytabEntry(raw)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func parseKeytabEntry(raw []byte) (keytabEntry, error) {
	r := bytes.NewReader(raw)
	read := func(v any) error { return binary.Read(r, binary.BigEndian, v) }
	text := func() (string, error) {
		var n uint16
		if err := read(&n); err != nil || n == 0 || int(n) > r.Len() {
			return "", invalid("invalid keytab principal")
		}
		b := make([]byte, int(n))
		_, _ = io.ReadFull(r, b)
		if !utf8.Valid(b) || strings.IndexFunc(string(b), unicode.IsControl) >= 0 || strings.ContainsAny(string(b), "\\@") || strings.HasPrefix(string(b), "-") {
			return "", invalid("unsupported keytab principal characters")
		}
		return string(b), nil
	}
	var components uint16
	if err := read(&components); err != nil || components == 0 || components > 32 {
		return keytabEntry{}, invalid("invalid keytab principal")
	}
	realm, err := text()
	if err != nil {
		return keytabEntry{}, err
	}
	parts := make([]string, int(components))
	for i := range parts {
		parts[i], err = text()
		if err != nil || strings.Contains(parts[i], "/") {
			return keytabEntry{}, invalid("invalid keytab principal component")
		}
	}
	var nameType, timestamp uint32
	var kvno8 uint8
	var enctype, keyLength uint16
	if read(&nameType) != nil || read(&timestamp) != nil || read(&kvno8) != nil || read(&enctype) != nil || read(&keyLength) != nil || keyLength == 0 || int(keyLength) > r.Len() {
		return keytabEntry{}, invalid("invalid keytab key entry")
	}
	_, _ = r.Seek(int64(keyLength), io.SeekCurrent)
	kvno := uint32(kvno8)
	// Records may have padding or Heimdal flags after the optional 32-bit
	// KVNO. Keep that data intact when merging, as Kerberos readers do.
	if r.Len() >= 4 {
		var value uint32
		_ = read(&value)
		if value != 0 {
			kvno = value
		}
	}
	return keytabEntry{principal: strings.Join(parts, "/") + "@" + realm, kvno: kvno, enctype: enctype, raw: raw}, nil
}

func keytabPrincipals(entries []keytabEntry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.principal
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func mergeKeytabs(files ...[]byte) ([]byte, error) {
	entries := []keytabEntry{}
	indices := map[string]int{}
	for _, file := range files {
		parsed, err := parseKeytab(file)
		if err != nil {
			return nil, err
		}
		for _, entry := range parsed {
			key := fmt.Sprintf("%s:%d:%d", entry.principal, entry.kvno, entry.enctype)
			if i, ok := indices[key]; ok {
				entries[i] = entry
			} else {
				indices[key] = len(entries)
				entries = append(entries, entry)
			}
		}
	}
	var out bytes.Buffer
	out.Write([]byte{5, 2})
	for _, entry := range entries {
		_ = binary.Write(&out, binary.BigEndian, int32(len(entry.raw)))
		out.Write(entry.raw)
	}
	if out.Len() > maxKeytabBytes {
		return nil, invalid("combined system keytab exceeds 1 MiB")
	}
	return out.Bytes(), nil
}

func (s *Service) keytabCipher(create bool) (cipher.AEAD, error) {
	dir, err := s.runtimeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "keytab.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) && create {
		var count int64
		if err := s.DB.Model(&directoryModels.KerberosKeytab{}).Count(&count).Error; err != nil {
			return nil, err
		}
		var passwords int64
		if err := s.DB.Model(&directoryModels.ActiveDirectory{}).Where("length(join_password_ciphertext) > 0").Count(&passwords).Error; err != nil {
			return nil, err
		}
		if count > 0 || passwords > 0 {
			return nil, fmt.Errorf("Kerberos encryption key is missing; restore directory-services/keytab.key with the database backup")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if openErr != nil {
			return nil, openErr
		}
		_, err = file.Write(key)
		if err == nil {
			err = file.Sync()
		}
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		return nil, fmt.Errorf("read Kerberos encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid Kerberos encryption key")
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Kerberos encryption key must be a regular file")
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Service) encryptKeytab(data []byte) ([]byte, error) {
	return s.encryptSecret(data, "sylve-kerberos-keytab-v1")
}

func (s *Service) decryptKeytab(data []byte) ([]byte, error) {
	return s.decryptSecret(data, "sylve-kerberos-keytab-v1")
}

func (s *Service) encryptSecret(data []byte, purpose string) ([]byte, error) {
	aead, err := s.keytabCipher(true)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, data, []byte(purpose)), nil
}

func (s *Service) decryptSecret(data []byte, purpose string) ([]byte, error) {
	aead, err := s.keytabCipher(false)
	if err != nil {
		return nil, err
	}
	if len(data) < aead.NonceSize() {
		return nil, fmt.Errorf("invalid encrypted directory-services secret")
	}
	return aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(purpose))
}
