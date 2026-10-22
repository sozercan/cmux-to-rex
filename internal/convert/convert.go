// Package convert turns a cmux sidebar layout into Rex session.create requests.
//
// Mapping:
//   - each cmux workspace (sidebar entry) becomes a Rex session, in sidebar order,
//     or with ByGroup, a window in its sidebar group's session;
//   - a workspace's split tree becomes the first Rex window, showing each pane's
//     selected tab;
//   - every other tab in a pane becomes its own Rex window, because Rex panes
//     have no tabs of their own.
//
// Only terminal panels are converted. Agent environments are never copied.
package convert

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sozercan/cmux-to-rex/internal/cmux"
	"github.com/sozercan/cmux-to-rex/internal/rex"
)

// Options control the conversion.
type Options struct {
	// Groups limits conversion to workspaces in these sidebar groups.
	Groups []string
	// GroupPrefix prefixes each session label with its sidebar group, as "group/title".
	GroupPrefix bool
	// ByGroup makes one session per sidebar group, with each workspace as a window.
	ByGroup bool
	// ResumeAgents types each terminal's saved agent resume command into its new shell.
	ResumeAgents bool
}

// Plan is the converted layout: one session.create request per workspace.
type Plan struct {
	Sessions []rex.CreateSession `json:"sessions"`
	Warnings []string            `json:"warnings,omitempty"`
}

// Convert builds a plan from an exported cmux session.
func Convert(s *cmux.Session, opts Options) Plan {
	var plan Plan
	wanted := map[string]bool{}
	for _, g := range opts.Groups {
		wanted[g] = true
	}
	used := map[string]int{}
	groupSession := map[string]int{}
	groupParts := map[string]int{}

	for _, win := range s.Windows {
		groups := map[string]string{}
		for _, g := range win.TabManager.WorkspaceGroups {
			groups[g.ID] = g.Name
		}
		for _, ws := range win.TabManager.Workspaces {
			group := ""
			if ws.GroupID != nil {
				group = groups[*ws.GroupID]
			}
			if len(wanted) > 0 && !wanted[group] {
				continue
			}
			title := workspaceTitle(ws)
			c := workspaceConverter{ws: ws, opts: opts, panels: map[string]cmux.Panel{}}
			for _, p := range ws.Panels {
				c.panels[p.ID] = p
			}
			windows := c.windows()
			for _, w := range c.warnings {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: %s", title, w))
			}
			if len(windows) == 0 {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: skipped, no terminal tabs", title))
				continue
			}
			if opts.ByGroup {
				if group == "" {
					group = UngroupedLabel
				}
				windows[0].Label = truncate(title)
				for i := 1; i < len(windows); i++ {
					windows[i].Label = truncate(title + " · " + windows[i].Label)
				}
				// Start another session for the group when this workspace's
				// windows would push it past Rex's window limit.
				i, ok := groupSession[group]
				if !ok || len(plan.Sessions[i].InitialWindows)+len(windows) > rex.MaxWindows {
					groupParts[group]++
					label := group
					if groupParts[group] > 1 {
						label = fmt.Sprintf("%s %d", group, groupParts[group])
					}
					i = len(plan.Sessions)
					groupSession[group] = i
					plan.Sessions = append(plan.Sessions, rex.CreateSession{Label: uniqueLabel(truncate(label), used)})
				}
				plan.Sessions[i].InitialWindows = append(plan.Sessions[i].InitialWindows, windows...)
				continue
			}
			label := title
			if opts.GroupPrefix && group != "" {
				label = group + "/" + title
			}
			plan.Sessions = append(plan.Sessions, rex.CreateSession{
				Label:          uniqueLabel(truncate(label), used),
				InitialWindows: windows,
			})
		}
	}
	plan.Sessions = splitOversized(plan.Sessions, used)
	return plan
}

// splitOversized breaks any session with more windows than Rex allows into
// numbered sessions, keeping window order.
func splitOversized(sessions []rex.CreateSession, used map[string]int) []rex.CreateSession {
	var out []rex.CreateSession
	for _, s := range sessions {
		for part := 1; len(s.InitialWindows) > 0; part++ {
			n := min(len(s.InitialWindows), rex.MaxWindows)
			label := s.Label
			if part > 1 {
				label = uniqueLabel(truncate(fmt.Sprintf("%s %d", s.Label, part)), used)
			}
			out = append(out, rex.CreateSession{Label: label, InitialWindows: s.InitialWindows[:n]})
			s.InitialWindows = s.InitialWindows[n:]
		}
	}
	return out
}

// UngroupedLabel names the session that holds workspaces outside any sidebar group.
const UngroupedLabel = "ungrouped"

type workspaceConverter struct {
	ws       cmux.Workspace
	opts     Options
	panels   map[string]cmux.Panel
	extra    []string // panel IDs that become their own windows
	warnings []string
}

func (c *workspaceConverter) windows() []rex.Window {
	var windows []rex.Window
	if c.ws.Layout != nil {
		if node, label, ok := c.node(c.ws.Layout); ok {
			windows = append(windows, rex.Window{Label: truncate(label), Layout: node})
		}
	}
	for _, id := range c.extra {
		p := c.panels[id]
		windows = append(windows, rex.Window{Label: truncate(panelTitle(p)), Layout: rex.Node{Block: c.block(p)}})
	}
	return windows
}

// node converts a layout subtree. It returns the label of the subtree's first
// pane and false when the subtree has no terminal panels.
func (c *workspaceConverter) node(l *cmux.Layout) (rex.Node, string, bool) {
	switch {
	case l.Pane != nil:
		return c.pane(l.Pane)
	case l.Split != nil:
		first, firstLabel, okFirst := c.childNode(l.Split.First)
		second, secondLabel, okSecond := c.childNode(l.Split.Second)
		switch {
		case okFirst && okSecond:
			return rex.Node{Split: &rex.Split{
				Direction: direction(l.Split.Orientation),
				Ratio:     ratio(l.Split.DividerPosition),
				Before:    first,
				After:     second,
			}}, firstLabel, true
		case okFirst:
			return first, firstLabel, true
		case okSecond:
			return second, secondLabel, true
		}
	}
	return rex.Node{}, "", false
}

func (c *workspaceConverter) childNode(l *cmux.Layout) (rex.Node, string, bool) {
	if l == nil {
		return rex.Node{}, "", false
	}
	return c.node(l)
}

// pane shows the pane's selected terminal tab and queues its other tabs as
// separate windows.
func (c *workspaceConverter) pane(p *cmux.Pane) (rex.Node, string, bool) {
	var terminals []cmux.Panel
	for _, id := range p.PanelIDs {
		panel, ok := c.panels[id]
		if !ok {
			c.warnings = append(c.warnings, fmt.Sprintf("panel %s missing from export", id))
			continue
		}
		if panel.Type != "terminal" {
			c.warnings = append(c.warnings, fmt.Sprintf("skipped %s tab %q", panel.Type, panelTitle(panel)))
			continue
		}
		terminals = append(terminals, panel)
	}
	if len(terminals) == 0 {
		return rex.Node{}, "", false
	}
	shown := terminals[0]
	if p.SelectedPanelID != nil {
		for _, t := range terminals {
			if t.ID == *p.SelectedPanelID {
				shown = t
			}
		}
	}
	for _, t := range terminals {
		if t.ID != shown.ID {
			c.extra = append(c.extra, t.ID)
		}
	}
	return rex.Node{Block: c.block(shown)}, panelTitle(shown), true
}

func (c *workspaceConverter) block(p cmux.Panel) *rex.Block {
	opts := &rex.ShellOptions{Cwd: panelDir(p, c.ws)}
	if p.Terminal != nil {
		if p.Terminal.IsRemoteTerminal {
			c.warnings = append(c.warnings, fmt.Sprintf("tab %q was remote; opening a local shell", panelTitle(p)))
		}
		if c.opts.ResumeAgents && p.Terminal.ResumeBinding != nil && p.Terminal.ResumeBinding.Command != "" {
			opts.InitialInput = p.Terminal.ResumeBinding.Command + "\n"
		}
	}
	return &rex.Block{Flavor: rex.ShellFlavor, Label: truncate(panelTitle(p)), Options: opts}
}

func panelDir(p cmux.Panel, ws cmux.Workspace) string {
	switch {
	case p.Directory != "":
		return p.Directory
	case p.Terminal != nil && p.Terminal.WorkingDirectory != "":
		return p.Terminal.WorkingDirectory
	}
	return ws.CurrentDirectory
}

func panelTitle(p cmux.Panel) string {
	if p.CustomTitle != nil && strings.TrimSpace(*p.CustomTitle) != "" {
		return *p.CustomTitle
	}
	if strings.TrimSpace(p.Title) != "" {
		return p.Title
	}
	if p.Directory != "" {
		return filepath.Base(p.Directory)
	}
	return "Terminal"
}

func workspaceTitle(ws cmux.Workspace) string {
	if ws.CustomTitle != nil && strings.TrimSpace(*ws.CustomTitle) != "" {
		return *ws.CustomTitle
	}
	if len(ws.Panels) > 0 {
		return panelTitle(ws.Panels[0])
	}
	if ws.CurrentDirectory != "" {
		return filepath.Base(ws.CurrentDirectory)
	}
	return "workspace"
}

// direction maps cmux orientation to Rex split direction. Both use
// "horizontal" for left-to-right and "vertical" for top-to-bottom.
func direction(orientation string) string {
	if orientation == "vertical" {
		return "vertical"
	}
	return "horizontal"
}

// ratio keeps the divider strictly inside (0, 1) as Rex requires.
func ratio(pos float64) float64 {
	if pos <= 0 || pos >= 1 {
		return 0.5
	}
	return pos
}

func truncate(s string) string {
	if utf8.RuneCountInString(s) <= rex.MaxLabel {
		return s
	}
	return string([]rune(s)[:rex.MaxLabel])
}

func uniqueLabel(label string, used map[string]int) string {
	used[label]++
	if used[label] == 1 {
		return label
	}
	for {
		suffix := fmt.Sprintf(" (%d)", used[label])
		candidate := label
		if utf8.RuneCountInString(candidate)+len(suffix) > rex.MaxLabel {
			candidate = string([]rune(candidate)[:rex.MaxLabel-len(suffix)])
		}
		candidate += suffix
		if used[candidate] == 0 {
			used[candidate] = 1
			return candidate
		}
		used[label]++
	}
}
