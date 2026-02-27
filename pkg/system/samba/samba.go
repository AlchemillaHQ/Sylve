package samba

import (
	"fmt"
	"strings"

	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func SambaUserExists(name string) (bool, error) {
	out, err := utils.RunCommand("/usr/local/bin/pdbedit", "-L", name)
	if err != nil {
		low := strings.ToLower(out)
		if strings.Contains(low, "no such user") ||
			strings.Contains(low, "nt_status_no_such_user") ||
			strings.Contains(low, "username not found!") {
			return false, nil
		}
		return false, fmt.Errorf("pdbedit lookup failed: %v: %s", err, out)
	}

	return true, nil
}

func CreateSambaUser(name, password string) error {
	exists, err := system.UnixUserExists(name)
	if err != nil {
		return fmt.Errorf("failed to check if user exists: %v", err)
	}

	if !exists {
		return fmt.Errorf("user %s does not exist in the system", name)
	}

	input := fmt.Sprintf("%[1]s\n%[1]s\n", password)
	out, err := utils.RunCommandWithInput("/usr/local/bin/smbpasswd", input, "-s", "-a", name)

	if err != nil {
		return fmt.Errorf("smbpasswd -a %s failed: %v: %s", name, err, out)
	}
	return nil
}

func EditSambaUser(name, newPassword string) error {
	exists, err := system.UnixUserExists(name)
	if err != nil {
		return fmt.Errorf("failed to check if user exists: %v", err)
	}

	if !exists {
		return fmt.Errorf("user %s does not exist in the system", name)
	}

	input := fmt.Sprintf("%[1]s\n%[1]s\n", newPassword)
	out, err := utils.RunCommandWithInput("/usr/local/bin/smbpasswd", input, "-s", name)
	if err != nil {
		return fmt.Errorf("smbpasswd change %s failed: %v: %s", name, err, out)
	}
	return nil
}

func DeleteSambaUser(name string) error {
	out, err := utils.RunCommand("/usr/local/bin/smbpasswd", "-x", name)
	if err != nil {
		return fmt.Errorf("smbpasswd -x %s failed: %v: %s", name, err, out)
	}
	return nil
}
