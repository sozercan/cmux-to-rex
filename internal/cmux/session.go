// Package cmux reads the session file written by `cmux restore-session --export`.
package cmux

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
)

// SupportedVersion is the session snapshot version this tool understands.
const SupportedVersion = 1

// Session is the exported cmux session. Only the fields needed to rebuild the
// sidebar layout are decoded; agent environments and notifications are ignored.
type Session struct {
	Version int      `json:"version"`
	Windows []Window `json:"windows"`
}

type Window struct {
	TabManager TabManager `json:"tabManager"`
}

type TabManager struct {
	Workspaces      []Workspace `json:"workspaces"`
	WorkspaceGroups []Group     `json:"workspaceGroups"`
}

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Workspace struct {
	CustomTitle      *string `json:"customTitle"`
	CurrentDirectory string  `json:"currentDirectory"`
	GroupID          *string `json:"groupId"`
	Layout           *Layout `json:"layout"`
	Panels           []Panel `json:"panels"`
}

// Layout is a node of the workspace split tree: either a pane or a split.
type Layout struct {
	Type  string `json:"type"`
	Pane  *Pane  `json:"pane"`
	Split *Split `json:"split"`
}

type Pane struct {
	PanelIDs        []string `json:"panelIds"`
	SelectedPanelID *string  `json:"selectedPanelId"`
}

// Split orientation is "horizontal" (left to right) or "vertical" (top to bottom).
type Split struct {
	Orientation     string  `json:"orientation"`
	DividerPosition float64 `json:"dividerPosition"`
	First           *Layout `json:"first"`
	Second          *Layout `json:"second"`
}

type Panel struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	CustomTitle *string   `json:"customTitle"`
	Directory   string    `json:"directory"`
	Terminal    *Terminal `json:"terminal"`
}

type Terminal struct {
	WorkingDirectory string         `json:"workingDirectory"`
	IsRemoteTerminal bool           `json:"isRemoteTerminal"`
	ResumeBinding    *ResumeBinding `json:"resumeBinding"`
}

// ResumeBinding is the command cmux would run to resume an agent in a terminal.
type ResumeBinding struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Command string `json:"command"`
}

// Decode reads an exported session.
func Decode(r io.Reader) (*Session, error) {
	var s Session
	if err := json.NewDecoder(r).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode cmux session: %w", err)
	}
	if s.Version != SupportedVersion {
		return nil, fmt.Errorf("unsupported cmux session version %d (want %d)", s.Version, SupportedVersion)
	}
	return &s, nil
}

// Export asks the running cmux to write its saved session to path. It never
// passes --force, so cmux refuses to replace an existing file.
func Export(cmuxBin, path string) error {
	out, err := exec.Command(cmuxBin, "restore-session", "--export", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cmux restore-session --export: %w: %s", err, out)
	}
	return nil
}
