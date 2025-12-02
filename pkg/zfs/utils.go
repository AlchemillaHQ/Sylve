package zfs

import (
	"bytes"
	"io"
	"regexp"
	"strings"
)

func (z *zfs) run(in io.Reader, out io.Writer, cmd string, args ...string) ([][]string, error) {
	var stdout, stderr bytes.Buffer

	if z.sudo {
		args = append([]string{cmd}, args...)
		cmd = "sudo"
	}

	cmdOut := out

	if cmdOut == nil {
		cmdOut = &stdout
	}

	joinedArgs := strings.Join(args, " ")

	if err := z.exec.Run(in, cmdOut, &stderr, cmd, args...); err != nil {
		return nil, &Error{
			Err:    err,
			Debug:  strings.Join([]string{cmd, joinedArgs}, " "),
			Stderr: stderr.String(),
		}
	}

	if out != nil {
		return nil, nil
	}

	lines := strings.Split(stdout.String(), "\n")
	lines = lines[0 : len(lines)-1]

	output := make([][]string, len(lines))

	for i, l := range lines {
		output[i] = strings.Fields(l)
	}

	return output, nil
}

func (s *zfs) runJSON(cmd string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer

	if s.sudo {
		args = append([]string{cmd}, args...)
		cmd = "sudo"
	}

	joinedArgs := strings.Join(args, " ")

	if err := s.exec.Run(nil, &stdout, &stderr, cmd, args...); err != nil {
		return nil, &Error{
			Err:    err,
			Debug:  strings.Join([]string{cmd, joinedArgs}, " "),
			Stderr: stderr.String(),
		}
	}

	return stdout.Bytes(), nil
}

// https://docs.oracle.com/cd/E26505_01/html/E37384/gbcpt.html
func IsValidPoolName(name string) bool {
	// Must start with a letter and contain only alphanumeric characters, '_', '-', or '.'
	validNamePattern := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
	if !validNamePattern.MatchString(name) {
		return false
	}

	if len(name) >= 2 && name[0] == 'c' && name[1] >= '0' && name[1] <= '9' {
		return false
	}

	reservedNames := map[string]bool{
		"log":    true,
		"mirror": true,
		"raidz":  true,
		"raidz1": true,
		"raidz2": true,
		"raidz3": true,
		"spare":  true,
	}

	lowerName := strings.ToLower(name)
	if reservedNames[lowerName] {
		return false
	}

	for reserved := range reservedNames {
		if strings.HasPrefix(lowerName, reserved) {
			return false
		}
	}

	if strings.Contains(name, "%") {
		return false
	}

	return true
}
