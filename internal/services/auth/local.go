package auth

import (
	"fmt"
	"sylve/internal/db/models"
	"sylve/pkg/system"
	"sylve/pkg/system/samba"
	"sylve/pkg/utils"
	"time"
)

func (s *Service) ListUsers() ([]models.User, error) {
	var users []models.User
	if err := s.DB.Find(&users).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_users: %w", err)
	}
	return users, nil
}

func (s *Service) GetUserByID(id uint) (*models.User, error) {
	var user models.User
	if err := s.DB.First(&user, id).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_user_by_id: %w", err)
	}
	return &user, nil
}

func (s *Service) CreateUser(user *models.User) error {
	if user.Email != "" && !utils.IsValidEmail(user.Email) {
		return fmt.Errorf("invalid_email_format: %s", user.Email)
	}

	if user.Username == "" || len(user.Username) < 3 || len(user.Username) > 128 {
		return fmt.Errorf("invalid_username_length: %s", user.Username)
	}

	if user.Password == "" || len(user.Password) < 8 || len(user.Password) > 128 {
		return fmt.Errorf("invalid_password_length: %s", user.Password)
	}

	if !utils.IsValidUsername(user.Username) {
		return fmt.Errorf("invalid_username_format: %s", user.Username)
	}

	pwCopy := user.Password

	hashed, err := utils.HashPassword(user.Password)
	if err != nil {
		return fmt.Errorf("failed_to_hash_password: %w", err)
	}

	user.Password = hashed

	exists, err := system.UnixUserExists(user.Username)
	if err != nil {
		return fmt.Errorf("failed_to_check_unix_user: %w", err)
	}

	if exists {
		return fmt.Errorf("user_already_exists: %s", user.Username)
	}

	if err := system.CreateUnixUser(user.Username, "", ""); err != nil {
		return fmt.Errorf("failed_to_create_unix_user: %w", err)
	}

	if err := samba.CreateSambaUser(user.Username, pwCopy); err != nil {
		return fmt.Errorf("failed_to_create_samba_user: %w", err)
	}

	if err := s.DB.Create(user).Error; err != nil {
		return fmt.Errorf("failed_to_create_user: %w", err)
	}

	return nil
}

func (s *Service) EditUser(user *models.User) error {
	if err := s.DB.Save(user).Error; err != nil {
		return fmt.Errorf("failed_to_edit_user: %w", err)
	}
	return nil
}

func (s *Service) DeleteUser(userID uint) error {
	user, err := s.GetUserByID(userID)
	if err != nil {
		return fmt.Errorf("failed_to_get_user: %w", err)
	}

	if user.Username == "" {
		return fmt.Errorf("user_not_found: %d", userID)
	}

	if err := samba.DeleteSambaUser(user.Username); err != nil {
		return fmt.Errorf("failed_to_delete_samba_user: %w", err)
	}

	if err := system.DeleteUnixUser(user.Username, true); err != nil {
		return fmt.Errorf("failed_to_delete_unix_user: %w", err)
	}

	if err := s.DB.Where("user_id = ?", userID).Delete(&models.Token{}).Error; err != nil {
		return fmt.Errorf("failed_to_delete_user_tokens: %w", err)
	}

	if err := s.DB.Delete(user).Error; err != nil {
		return fmt.Errorf("failed_to_delete_user: %w", err)
	}

	return nil
}

func (s *Service) UpdateLastUsageTime(userID uint) error {
	now := time.Now()

	// Try to update only if last_login_time < now - 30s
	result := s.DB.
		Model(&models.User{}).
		Where("id = ? AND last_login_time < ?", userID, now.Add(-30*time.Second)).
		UpdateColumn("last_login_time", now)

	if result.Error != nil {
		return fmt.Errorf("failed_to_update_last_usage_time: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var count int64
		if err := s.DB.
			Model(&models.User{}).
			Where("id = ?", userID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_verify_user_existence: %w", err)
		}

		if count == 0 {
			return nil
		}
	}

	return nil
}
