package pkg

import "github.com/alchemillahq/sylve/pkg/utils"

var runCommand = utils.RunCommand

func IsPackageInstalled(packageName string) bool {
	_, err := runCommand("/usr/sbin/pkg", "info", packageName)
	return err == nil
}
