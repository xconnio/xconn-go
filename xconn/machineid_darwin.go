package xconn

import (
	"errors"
	"os/exec"
	"strings"
)

func machineID() (string, error) {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		parts := strings.Split(line, "\"")
		if len(parts) >= 4 {
			return parts[3], nil
		}
	}

	return "", errors.New("IOPlatformUUID not found")
}
