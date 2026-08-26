// SPDX-License-Identifier: BSD-2-Clause

package remoteexec

import (
	"fmt"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxOperationTokenLen = 128

type SSHDestination struct{ value string }

func ParseSSHDestination(raw string) (SSHDestination, error) {
	value, ok := safeAtom(raw)
	if !ok {
		return SSHDestination{}, fmt.Errorf("invalid_ssh_destination")
	}
	user, host, hasUser := strings.Cut(value, "@")
	if hasUser {
		if strings.Contains(host, "@") || !validSSHUser(user) {
			return SSHDestination{}, fmt.Errorf("invalid_ssh_destination")
		}
	} else {
		host = user
		user = ""
	}
	if host == "" || host[0] == '-' {
		return SSHDestination{}, fmt.Errorf("invalid_ssh_destination")
	}

	bracketed := strings.HasPrefix(host, "[")
	if bracketed {
		if !strings.HasSuffix(host, "]") {
			return SSHDestination{}, fmt.Errorf("invalid_ssh_destination")
		}
		host = host[1 : len(host)-1]
	}
	if address, err := netip.ParseAddr(host); err == nil {
		if address.Zone() != "" || (bracketed && !address.Is6()) {
			return SSHDestination{}, fmt.Errorf("invalid_ssh_destination")
		}
		host = address.String()
	} else {
		if bracketed || strings.Contains(host, ":") || !validDNSHost(host) {
			return SSHDestination{}, fmt.Errorf("invalid_ssh_destination")
		}
		host = strings.ToLower(host)
	}
	if user != "" {
		host = user + "@" + host
	}
	return SSHDestination{value: host}, nil
}

func (destination SSHDestination) String() string { return destination.value }

func (destination SSHDestination) ZeltaString() string {
	user, host, hasUser := strings.Cut(destination.value, "@")
	if !hasUser {
		host = user
		user = ""
	}
	if address, err := netip.ParseAddr(host); err == nil && address.Is6() {
		host = "[" + host + "]"
	}
	if user != "" {
		return user + "@" + host
	}
	return host
}

func validSSHUser(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index := range len(value) {
		char := value[index]
		if asciiAlphaNumeric(char) || char == '_' || (index > 0 && (char == '.' || char == '-')) {
			continue
		}
		return false
	}
	return true
}

func validDNSHost(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	if strings.HasSuffix(value, ".") {
		value = strings.TrimSuffix(value, ".")
	}
	if value == "" || numericDotsOnly(value) {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := range len(label) {
			if !asciiAlphaNumeric(label[index]) && label[index] != '-' {
				return false
			}
		}
	}
	return true
}

func numericDotsOnly(value string) bool {
	for index := range len(value) {
		if (value[index] < '0' || value[index] > '9') && value[index] != '.' {
			return false
		}
	}
	return true
}

type ZFSDataset struct{ value string }

func ParseZFSDataset(raw string) (ZFSDataset, error) {
	value, ok := safeAtom(raw)
	if !ok || strings.ContainsAny(value, "@#") {
		return ZFSDataset{}, fmt.Errorf("invalid_zfs_dataset")
	}
	parts := strings.Split(value, "/")
	if parts[0] == "" || !asciiAlpha(parts[0][0]) {
		return ZFSDataset{}, fmt.Errorf("invalid_zfs_dataset")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || !validZFSComponent(part) {
			return ZFSDataset{}, fmt.Errorf("invalid_zfs_dataset")
		}
	}
	return ZFSDataset{value: value}, nil
}

func (dataset ZFSDataset) String() string { return dataset.value }

func (dataset ZFSDataset) Pool() string {
	pool, _, _ := strings.Cut(dataset.value, "/")
	return pool
}

func (dataset ZFSDataset) Within(root ZFSDataset) bool {
	return dataset.value == root.value || strings.HasPrefix(dataset.value, root.value+"/")
}

func JoinZFSDataset(root ZFSDataset, suffix string) (ZFSDataset, error) {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return root, nil
	}
	return ParseZFSDataset(root.value + "/" + suffix)
}

type ZFSSnapshotName struct{ value string }

func ParseZFSSnapshotName(raw string) (ZFSSnapshotName, error) {
	value, ok := safeAtom(raw)
	if !ok {
		return ZFSSnapshotName{}, fmt.Errorf("invalid_zfs_snapshot")
	}
	value = strings.TrimPrefix(value, "@")
	if value == "" || !validZFSComponent(value) {
		return ZFSSnapshotName{}, fmt.Errorf("invalid_zfs_snapshot")
	}
	return ZFSSnapshotName{value: value}, nil
}

func (snapshot ZFSSnapshotName) String() string { return snapshot.value }
func (snapshot ZFSSnapshotName) WithAt() string {
	if snapshot.value == "" {
		return ""
	}
	return "@" + snapshot.value
}

type ZFSSnapshot struct {
	dataset ZFSDataset
	name    ZFSSnapshotName
}

func ParseZFSSnapshot(raw string) (ZFSSnapshot, error) {
	value, ok := safeAtom(raw)
	if !ok || strings.Count(value, "@") != 1 {
		return ZFSSnapshot{}, fmt.Errorf("invalid_zfs_snapshot")
	}
	datasetRaw, nameRaw, _ := strings.Cut(value, "@")
	dataset, err := ParseZFSDataset(datasetRaw)
	if err != nil {
		return ZFSSnapshot{}, err
	}
	name, err := ParseZFSSnapshotName(nameRaw)
	if err != nil {
		return ZFSSnapshot{}, err
	}
	return ZFSSnapshot{dataset: dataset, name: name}, nil
}

func NewZFSSnapshot(dataset ZFSDataset, name ZFSSnapshotName) (ZFSSnapshot, error) {
	return ParseZFSSnapshot(dataset.String() + name.WithAt())
}

func (snapshot ZFSSnapshot) String() string {
	return snapshot.dataset.String() + snapshot.name.WithAt()
}
func (snapshot ZFSSnapshot) Dataset() ZFSDataset   { return snapshot.dataset }
func (snapshot ZFSSnapshot) Name() ZFSSnapshotName { return snapshot.name }

type ZFSPropertyName struct{ value string }

func ParseZFSPropertyName(raw string) (ZFSPropertyName, error) {
	value, ok := safeAtom(raw)
	if !ok || !asciiLower(value[0]) {
		return ZFSPropertyName{}, fmt.Errorf("invalid_zfs_property_name")
	}
	for index := range len(value) {
		char := value[index]
		if asciiLower(char) || (char >= '0' && char <= '9') ||
			char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return ZFSPropertyName{}, fmt.Errorf("invalid_zfs_property_name")
	}
	return ZFSPropertyName{value: value}, nil
}

func (property ZFSPropertyName) String() string { return property.value }

type ZFSPropertyValue struct{ value string }

func ParseZFSPropertyValue(raw string) (ZFSPropertyValue, error) {
	if strings.IndexByte(raw, 0) >= 0 || !utf8.ValidString(raw) {
		return ZFSPropertyValue{}, fmt.Errorf("invalid_zfs_property_value")
	}
	return ZFSPropertyValue{value: raw}, nil
}

func (value ZFSPropertyValue) String() string { return value.value }

type OperationToken struct{ value string }

func ParseOperationToken(raw string) (OperationToken, error) {
	value, ok := safeAtom(raw)
	if !ok || len(value) > maxOperationTokenLen || !asciiAlphaNumeric(value[0]) {
		return OperationToken{}, fmt.Errorf("invalid_operation_token")
	}
	for index := range len(value) {
		char := value[index]
		if asciiAlphaNumeric(char) || char == '.' || char == '_' || char == '-' || char == ':' {
			continue
		}
		return OperationToken{}, fmt.Errorf("invalid_operation_token")
	}
	return OperationToken{value: value}, nil
}

func (token OperationToken) String() string { return token.value }

func safeAtom(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return "", false
		}
	}
	return value, true
}

func validZFSComponent(value string) bool {
	for index := range len(value) {
		char := value[index]
		if asciiAlphaNumeric(char) || char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return value != ""
}

func asciiAlpha(value byte) bool {
	return asciiLower(value) || (value >= 'A' && value <= 'Z')
}

func asciiLower(value byte) bool { return value >= 'a' && value <= 'z' }

func asciiAlphaNumeric(value byte) bool {
	return asciiAlpha(value) || (value >= '0' && value <= '9')
}
