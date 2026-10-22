package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sozercan/cmux-to-rex/internal/cmux"
	"github.com/sozercan/cmux-to-rex/internal/rex"
)

const fixture = `{
  "version": 1,
  "windows": [{
    "tabManager": {
      "workspaceGroups": [{"id": "g1", "name": "orka"}, {"id": "g2", "name": "misc"}],
      "workspaces": [
        {
          "customTitle": "scout",
          "currentDirectory": "/home/u",
          "groupId": "g1",
          "layout": {"type": "split", "split": {
            "orientation": "vertical", "dividerPosition": 0.3,
            "first": {"type": "pane", "pane": {"panelIds": ["a", "b"], "selectedPanelId": "b"}},
            "second": {"type": "pane", "pane": {"panelIds": ["c", "web"], "selectedPanelId": "c"}}
          }},
          "panels": [
            {"id": "a", "type": "terminal", "title": "~", "directory": "/home/u"},
            {"id": "b", "type": "terminal", "title": "build", "customTitle": "Build", "directory": "/home/u/p"},
            {"id": "c", "type": "terminal", "title": "agent", "directory": "/home/u/m",
             "terminal": {"resumeBinding": {"kind": "claude", "command": "claude --resume x"}}},
            {"id": "web", "type": "browser", "title": "docs"}
          ]
        },
        {
          "customTitle": "scout",
          "groupId": "g2",
          "layout": {"type": "pane", "pane": {"panelIds": ["d"]}},
          "panels": [{"id": "d", "type": "terminal", "title": "", "terminal": {"workingDirectory": "/srv/app"}}]
        },
        {
          "customTitle": "only browser",
          "groupId": "g2",
          "layout": {"type": "pane", "pane": {"panelIds": ["e"]}},
          "panels": [{"id": "e", "type": "browser", "title": "x"}]
        }
      ]
    }
  }]
}`

func decode(t *testing.T) *cmux.Session {
	t.Helper()
	s, err := cmux.Decode(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestConvertSplitAndTabs(t *testing.T) {
	plan := Convert(decode(t), Options{})
	if len(plan.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(plan.Sessions))
	}
	s := plan.Sessions[0]
	if s.Label != "scout" || len(s.InitialWindows) != 2 {
		t.Fatalf("session = %q with %d windows, want scout with 2", s.Label, len(s.InitialWindows))
	}

	main := s.InitialWindows[0]
	split := main.Layout.Split
	if main.Label != "Build" || split == nil {
		t.Fatalf("first window = %+v, want split labelled Build", main)
	}
	if split.Direction != "vertical" || split.Ratio != 0.3 {
		t.Errorf("split = %s %.2f, want vertical 0.30", split.Direction, split.Ratio)
	}
	if b := split.Before.Block; b.Label != "Build" || b.Options.Cwd != "/home/u/p" || b.Flavor != rex.ShellFlavor {
		t.Errorf("before = %+v %+v", b, b.Options)
	}
	if b := split.After.Block; b.Label != "agent" || b.Options.InitialInput != "" {
		t.Errorf("after = %+v %+v; resume must be opt-in", b, b.Options)
	}

	extra := s.InitialWindows[1]
	if extra.Label != "~" || extra.Layout.Block == nil || extra.Layout.Block.Options.Cwd != "/home/u" {
		t.Errorf("extra window = %+v", extra)
	}

	if got := plan.Sessions[1]; got.Label != "scout (2)" || got.InitialWindows[0].Layout.Block.Options.Cwd != "/srv/app" {
		t.Errorf("second session = %+v", got)
	}

	warnings := strings.Join(plan.Warnings, "\n")
	for _, want := range []string{`skipped browser tab "docs"`, "only browser: skipped, no terminal tabs"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings %q missing %q", warnings, want)
		}
	}
}

func TestConvertOptions(t *testing.T) {
	plan := Convert(decode(t), Options{Groups: []string{"orka"}, GroupPrefix: true, ResumeAgents: true})
	if len(plan.Sessions) != 1 || plan.Sessions[0].Label != "orka/scout" {
		t.Fatalf("sessions = %+v", plan.Sessions)
	}
	after := plan.Sessions[0].InitialWindows[0].Layout.Split.After.Block
	if after.Options.InitialInput != "claude --resume x\n" {
		t.Errorf("initial input = %q", after.Options.InitialInput)
	}
}

func TestPlanMatchesRexSchema(t *testing.T) {
	plan := Convert(decode(t), Options{})
	data, err := json.Marshal(plan.Sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"initial_windows"`, `"window_label"`, `"direction":"vertical"`, `"before"`, `"after"`, `"cwd"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%s missing %s", data, want)
		}
	}
	if strings.Contains(string(data), "initial_input") {
		t.Errorf("empty initial_input should be omitted: %s", data)
	}
}

func TestDecodeRejectsUnknownVersion(t *testing.T) {
	if _, err := cmux.Decode(strings.NewReader(`{"version": 2}`)); err == nil {
		t.Fatal("want error for version 2")
	}
}

func TestUniqueLabelTruncates(t *testing.T) {
	used := map[string]int{}
	long := strings.Repeat("x", 200)
	first := uniqueLabel(truncate(long), used)
	second := uniqueLabel(truncate(long), used)
	if len(first) != rex.MaxLabel || len(second) != rex.MaxLabel || first == second {
		t.Errorf("labels %d/%d chars, equal=%v", len(first), len(second), first == second)
	}
}
