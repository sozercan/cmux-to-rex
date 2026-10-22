// Package rex describes Rex session layouts and creates them through the rex CLI.
package rex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ShellFlavor is the block flavor for a terminal running the user's shell.
const ShellFlavor = "com.superlogical.terminal.shell"

// MaxLabel is the longest label Rex accepts for sessions and windows.
const MaxLabel = 128

// MaxWindows is the most windows Rex allows in one session.
const MaxWindows = 64

// CreateSession is the argument object for the session.create API method.
type CreateSession struct {
	Label          string   `json:"label,omitempty"`
	InitialWindows []Window `json:"initial_windows"`
}

type Window struct {
	Label  string `json:"window_label,omitempty"`
	Layout Node   `json:"layout"`
}

// Node holds exactly one of Block or Split.
type Node struct {
	Block *Block `json:"block,omitempty"`
	Split *Split `json:"split,omitempty"`
}

type Block struct {
	Flavor  string        `json:"flavor"`
	Label   string        `json:"label,omitempty"`
	Options *ShellOptions `json:"options,omitempty"`
}

type ShellOptions struct {
	Cwd          string `json:"cwd,omitempty"`
	InitialInput string `json:"initial_input,omitempty"`
}

// Split direction is "horizontal" (before on the left) or "vertical" (before on top).
// Ratio is the fraction of space Before gets.
type Split struct {
	Direction string  `json:"direction"`
	Ratio     float64 `json:"ratio,omitempty"`
	Before    Node    `json:"before"`
	After     Node    `json:"after"`
}

// Client runs the rex CLI.
type Client struct {
	Bin string
}

// FindBin locates the rex CLI: an explicit path, $REX_BIN, $PATH, then the
// copy bundled with the Rex app.
func FindBin(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv("REX_BIN"); env != "" {
		return env, nil
	}
	if p, err := exec.LookPath("rex"); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, "Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("rex CLI not found; pass --rex or set REX_BIN")
}

// SessionLabels returns the labels of the sessions that already exist.
func (c Client) SessionLabels() (map[string]bool, error) {
	out, err := c.run(nil, "ls", "--json")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Sessions []struct {
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("decode rex ls: %w", err)
	}
	labels := make(map[string]bool, len(resp.Sessions))
	for _, s := range resp.Sessions {
		labels[s.Label] = true
	}
	return labels, nil
}

// Create creates one session. session.create is atomic: if any window fails,
// Rex creates nothing.
func (c Client) Create(req CreateSession) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	out, err := c.run(body, "api", "call", "session.create", "-")
	if err != nil {
		return "", err
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("decode session.create result: %w", err)
	}
	return resp.SessionID, nil
}

func (c Client) run(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command(c.Bin, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("rex %v: %w: %s", args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return out, nil
}
