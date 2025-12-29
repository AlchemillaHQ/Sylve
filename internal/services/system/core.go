package system

import "github.com/alchemillahq/sylve/pkg/utils"

func (s *Service) RebootSystem() error {
	_, err := utils.RunCommand(
		"shutdown",
		"-r",
		"now",
		"Reboot initiated by Sylve",
	)

	return err
}
