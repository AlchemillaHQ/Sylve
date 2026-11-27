// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/sharerules"
	sambaSystem "github.com/alchemillahq/sylve/pkg/system/samba"
)

const (
	sambaStatusPath = "/usr/local/bin/smbstatus"
	sambaSmbdPath   = "/usr/local/sbin/smbd"
)

var sambaListUsers = sambaSystem.ListSambaUsers

type SambaPrincipalUser struct {
	ID           uint   `json:"id"`
	Username     string `json:"username"`
	SambaAccount *bool  `json:"sambaAccount"`
}

type SambaPrincipalGroup struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type SambaPrincipals struct {
	Users  []SambaPrincipalUser  `json:"users"`
	Groups []SambaPrincipalGroup `json:"groups"`
}

type ShareReference struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ShareDataset struct {
	GUID       string          `json:"guid"`
	Name       string          `json:"name"`
	Mountpoint string          `json:"mountpoint"`
	Mounted    bool            `json:"mounted"`
	SharedBy   *ShareReference `json:"sharedBy,omitempty"`
	Block      string          `json:"block,omitempty"`
}

type ShareState struct {
	DatasetName string
	Mountpoint  string
	State       string
	Block       string
}

type SambaSession struct {
	ID            string `json:"id"`
	PID           string `json:"pid"`
	Username      string `json:"username"`
	Group         string `json:"group"`
	RemoteMachine string `json:"remoteMachine"`
	Hostname      string `json:"hostname"`
	Dialect       string `json:"dialect"`
	Encryption    string `json:"encryption"`
	Signing       string `json:"signing"`
}

type SambaConnection struct {
	Share       string `json:"share"`
	SessionID   string `json:"sessionId"`
	PID         string `json:"pid"`
	Machine     string `json:"machine"`
	ConnectedAt string `json:"connectedAt"`
	Encryption  string `json:"encryption"`
	Signing     string `json:"signing"`
}

type SambaStatus struct {
	ServiceEnabled bool              `json:"serviceEnabled"`
	Running        bool              `json:"running"`
	Version        string            `json:"version"`
	Sessions       []SambaSession    `json:"sessions"`
	Connections    []SambaConnection `json:"connections"`
	OpenFiles      int               `json:"openFiles"`
	Error          string            `json:"error,omitempty"`
}

type flexibleString string

func (f *flexibleString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*f = flexibleString(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*f = flexibleString(number.String())
	return nil
}

type smbstatusProtection struct {
	Cipher flexibleString `json:"cipher"`
	Degree flexibleString `json:"degree"`
}

func (p smbstatusProtection) String() string {
	if p.Cipher == "" || p.Cipher == "-" {
		return string(p.Degree)
	}
	return strings.TrimSpace(string(p.Cipher) + " " + string(p.Degree))
}

type smbstatusOutput struct {
	Version  string `json:"version"`
	Sessions map[string]struct {
		SessionID flexibleString `json:"session_id"`
		ServerID  struct {
			PID flexibleString `json:"pid"`
		} `json:"server_id"`
		Username      string              `json:"username"`
		Groupname     string              `json:"groupname"`
		RemoteMachine string              `json:"remote_machine"`
		Hostname      string              `json:"hostname"`
		Dialect       string              `json:"session_dialect"`
		Encryption    smbstatusProtection `json:"encryption"`
		Signing       smbstatusProtection `json:"signing"`
	} `json:"sessions"`
	Tcons map[string]struct {
		Service   string         `json:"service"`
		SessionID flexibleString `json:"session_id"`
		ServerID  struct {
			PID flexibleString `json:"pid"`
		} `json:"server_id"`
		Machine     string              `json:"machine"`
		ConnectedAt string              `json:"connected_at"`
		Encryption  smbstatusProtection `json:"encryption"`
		Signing     smbstatusProtection `json:"signing"`
	} `json:"tcons"`
	OpenFiles map[string]json.RawMessage `json:"open_files"`
}

func parseSmbstatus(output string) (SambaStatus, error) {
	var parsed smbstatusOutput
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return SambaStatus{}, fmt.Errorf("unreadable smbstatus output: %w", err)
	}
	status := SambaStatus{Version: parsed.Version, Sessions: []SambaSession{}, Connections: []SambaConnection{}, OpenFiles: len(parsed.OpenFiles)}
	for _, session := range parsed.Sessions {
		status.Sessions = append(status.Sessions, SambaSession{
			ID:            string(session.SessionID),
			PID:           string(session.ServerID.PID),
			Username:      session.Username,
			Group:         session.Groupname,
			RemoteMachine: session.RemoteMachine,
			Hostname:      session.Hostname,
			Dialect:       session.Dialect,
			Encryption:    session.Encryption.String(),
			Signing:       session.Signing.String(),
		})
	}
	for _, tcon := range parsed.Tcons {
		status.Connections = append(status.Connections, SambaConnection{
			Share:       tcon.Service,
			SessionID:   string(tcon.SessionID),
			PID:         string(tcon.ServerID.PID),
			Machine:     tcon.Machine,
			ConnectedAt: tcon.ConnectedAt,
			Encryption:  tcon.Encryption.String(),
			Signing:     tcon.Signing.String(),
		})
	}
	sort.Slice(status.Sessions, func(i, j int) bool { return status.Sessions[i].ID < status.Sessions[j].ID })
	sort.Slice(status.Connections, func(i, j int) bool {
		if status.Connections[i].Share != status.Connections[j].Share {
			return status.Connections[i].Share < status.Connections[j].Share
		}
		return status.Connections[i].Machine < status.Connections[j].Machine
	})
	return status, nil
}

func (s *Service) Status() (SambaStatus, error) {
	enabled, err := s.sambaServiceEnabled()
	if err != nil {
		return SambaStatus{}, err
	}
	status := SambaStatus{ServiceEnabled: enabled, Sessions: []SambaSession{}, Connections: []SambaConnection{}}
	status.Running = sambaServiceRunning()
	if status.Running {
		output, err := sambaRunCommand(sambaStatusPath, "--json")
		if err == nil {
			parsed, parseErr := parseSmbstatus(output)
			if parseErr == nil {
				parsed.ServiceEnabled, parsed.Running = status.ServiceEnabled, true
				return parsed, nil
			}
			err = parseErr
		}
		status.Error = err.Error()
	}
	if output, err := sambaRunCommand(sambaSmbdPath, "-V"); err == nil {
		status.Version = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(output), "Version"))
	}
	return status, nil
}

func (s *Service) Principals() (SambaPrincipals, error) {
	var users []models.User
	if err := s.DB.Where("source = ?", "pam").Order("username").Find(&users).Error; err != nil {
		return SambaPrincipals{}, fmt.Errorf("failed_to_list_users: %w", err)
	}
	var groups []models.Group
	if err := s.DB.Order("name").Find(&groups).Error; err != nil {
		return SambaPrincipals{}, fmt.Errorf("failed_to_list_groups: %w", err)
	}
	accounts, accountsErr := sambaListUsers()
	result := SambaPrincipals{Users: make([]SambaPrincipalUser, 0, len(users)), Groups: make([]SambaPrincipalGroup, 0, len(groups))}
	for _, user := range users {
		principal := SambaPrincipalUser{ID: user.ID, Username: user.Username}
		if accountsErr == nil {
			hasAccount := slices.Contains(accounts, user.Username)
			principal.SambaAccount = &hasAccount
		}
		result.Users = append(result.Users, principal)
	}
	for _, group := range groups {
		result.Groups = append(result.Groups, SambaPrincipalGroup{ID: group.ID, Name: group.Name})
	}
	return result, nil
}

func (s *Service) ShareDatasets(ctx context.Context) ([]ShareDataset, error) {
	datasets, err := s.filesystems(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_datasets: %w", err)
	}
	shares, err := s.GetShares()
	if err != nil {
		return nil, err
	}
	sharedBy := make(map[string]*ShareReference, len(shares))
	for _, share := range shares {
		sharedBy[share.Dataset] = &ShareReference{ID: share.ID, Name: share.Name}
	}
	roots := sharerules.ProtectedRoots(datasets, sharerules.DataPath())
	result := make([]ShareDataset, 0, len(datasets))
	for _, dataset := range datasets {
		if dataset == nil {
			continue
		}
		result = append(result, ShareDataset{
			GUID:       dataset.GUID,
			Name:       dataset.Name,
			Mountpoint: dataset.Mountpoint,
			Mounted:    isSambaDatasetMounted(dataset),
			SharedBy:   sharedBy[dataset.GUID],
			Block:      sharerules.DatasetBlock(dataset, roots),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (s *Service) ShareStates(ctx context.Context, shares []sambaModels.SambaShare) (map[int]ShareState, error) {
	datasets, err := s.filesystems(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_datasets: %w", err)
	}
	byGUID := make(map[string]*gzfs.Dataset, len(datasets))
	for _, dataset := range datasets {
		if dataset != nil {
			byGUID[dataset.GUID] = dataset
		}
	}
	roots := sharerules.ProtectedRoots(datasets, sharerules.DataPath())
	states := make(map[int]ShareState, len(shares))
	for _, share := range shares {
		dataset := byGUID[share.Dataset]
		state := ShareState{State: sharerules.State(share.Enabled, dataset, isSambaDatasetMounted(dataset))}
		if dataset != nil {
			state.DatasetName, state.Mountpoint = dataset.Name, dataset.Mountpoint
			state.Block = sharerules.DatasetBlock(dataset, roots)
		}
		states[share.ID] = state
	}
	return states, nil
}
