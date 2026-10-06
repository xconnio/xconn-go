package xconn

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

// Credentials are the device's cloud identity, issued when it is attached to the cloud.
type Credentials struct {
	Realm      string `json:"realm"`
	AuthID     string `json:"authid"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"` // #nosec
}

// EnsureCredentials returns the device's cloud credentials from path, waiting for the file to
// appear (i.e. for the device to be attached) or for ctx to be done.
func EnsureCredentials(ctx context.Context, path string) (*Credentials, error) {
	if _, err := os.Stat(path); err != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return nil, fmt.Errorf("failed to create watcher: %w", err)
		}
		defer watcher.Close()

		if err := watcher.Add(filepath.Dir(path)); err != nil {
			return nil, fmt.Errorf("failed to add watcher: %w", err)
		}

		log.Println("Waiting for credentials file...")

	wait:
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case event, ok := <-watcher.Events:
				if !ok {
					return nil, fmt.Errorf("credentials watcher closed")
				}
				if event.Name == path && event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
					log.Println("Device successfully attached to cloud")
					break wait
				}
			}
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials: %w", err)
	}

	return &creds, nil
}
