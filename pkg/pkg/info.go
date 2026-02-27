package pkg

import "github.com/alchemillahq/sylve/pkg/utils"

func IsPackageInstalled(packageName string) bool {
	_, err := utils.RunCommand("/usr/sbin/pkg", "info", packageName)

	if err == nil {
		return true
	}

	return false
}
