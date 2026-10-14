package xconn

import (
	"golang.org/x/sys/windows/registry"
)

// MachineID returns a unique, stable identifier for the current host.
func MachineID() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`,
		registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer key.Close()

	value, _, err := key.GetStringValue("MachineGuid")
	if err != nil {
		return "", err
	}

	return value, nil
}
