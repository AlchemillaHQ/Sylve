// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"errors"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"gorm.io/gorm"
)

// ErrNoteNotFound indicates that the requested note does not exist.
var ErrNoteNotFound = errors.New("note_not_found")

func (s *Service) GetNoteByID(id int) (infoModels.Note, error) {
	var note infoModels.Note
	err := s.DB.First(&note, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return infoModels.Note{}, ErrNoteNotFound
		}
		return infoModels.Note{}, err
	}
	return note, nil
}

func (s *Service) GetNotes() ([]infoModels.Note, error) {
	var notes []infoModels.Note
	err := s.DB.Find(&notes).Error
	return notes, err
}

func (s *Service) AddNote(title, note string) (infoModels.Note, error) {
	n := infoModels.Note{Title: title, Content: note}
	err := s.DB.Create(&n).Error

	return n, err
}

func (s *Service) DeleteNoteByID(id int) error {
	result := s.DB.Delete(&infoModels.Note{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNoteNotFound
	}
	return nil
}

func (s *Service) BulkDeleteNotes(ids []int) error {
	tx := s.DB.Begin()
	if err := tx.Error; err != nil {
		return err
	}

	result := tx.Delete(&infoModels.Note{}, ids)
	if result.Error != nil {
		tx.Rollback()
		return result.Error
	}

	if err := tx.Commit().Error; err != nil {
		return err
	}

	return nil
}

func (s *Service) UpdateNoteByID(id int, title, note string) error {
	result := s.DB.Model(&infoModels.Note{}).
		Where("id = ?", id).
		Updates(infoModels.Note{Title: title, Content: note})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNoteNotFound
	}

	return nil
}
