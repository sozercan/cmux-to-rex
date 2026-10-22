// Command cmux-to-rex exports the cmux sidebar layout and recreates it in Rex.
//
// It only reads cmux and only adds Rex sessions: existing sessions are never
// changed, and a session whose label already exists is skipped.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sozercan/cmux-to-rex/internal/cmux"
	"github.com/sozercan/cmux-to-rex/internal/convert"
	"github.com/sozercan/cmux-to-rex/internal/rex"
)

const usage = `cmux-to-rex exports the cmux sidebar layout and recreates it in Rex.

Usage:
  cmux-to-rex export  [-o FILE]                  write cmux's saved session to FILE
  cmux-to-rex convert [-i FILE] [-o FILE] [...]  turn a cmux export into a Rex plan
  cmux-to-rex apply   [-i FILE] [--dry-run]      create the plan's sessions in Rex

Example:
  cmux-to-rex export -o cmux.json
  cmux-to-rex convert -i cmux.json -o plan.json
  cmux-to-rex apply -i plan.json --dry-run
  cmux-to-rex apply -i plan.json

Nothing is destructive: export never overwrites a file, and apply only creates
new Rex sessions, skipping any whose label already exists.

Run "cmux-to-rex COMMAND -h" for a command's options.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "export":
		err = runExport(args)
	case "convert":
		err = runConvert(args)
	case "apply":
		err = runApply(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cmux-to-rex:", err)
		os.Exit(1)
	}
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	out := fs.String("o", "cmux-session-"+time.Now().Format("20060102-150405")+".json", "file to write; must not exist")
	cmuxBin := fs.String("cmux", "cmux", "cmux CLI")
	fs.Parse(args)

	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s already exists; choose another -o", *out)
	}
	if err := cmux.Export(*cmuxBin, *out); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "exported cmux session to", *out)
	return nil
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func runConvert(args []string) error {
	fs := flag.NewFlagSet("convert", flag.ExitOnError)
	in := fs.String("i", "-", "cmux export to read, or - for stdin")
	out := fs.String("o", "-", "plan to write, or - for stdout; an existing file is not replaced")
	var opts convert.Options
	fs.Var((*stringList)(&opts.Groups), "group", "only convert workspaces in this sidebar group (repeatable)")
	fs.BoolVar(&opts.GroupPrefix, "group-prefix", false, `label sessions "group/title" to keep sidebar groups`)
	fs.BoolVar(&opts.ByGroup, "by-group", false, "one session per sidebar group, each workspace a tab (Rex allows 64 sessions)")
	fs.BoolVar(&opts.ResumeAgents, "resume-agents", false, "type each tab's saved agent resume command into its shell")
	fs.Parse(args)

	r, closeIn, err := openInput(*in)
	if err != nil {
		return err
	}
	defer closeIn()
	s, err := cmux.Decode(r)
	if err != nil {
		return err
	}

	plan := convert.Convert(s, opts)
	for _, w := range plan.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if *out == "-" {
		_, err = os.Stdout.Write(data)
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d sessions to %s\n", len(plan.Sessions), *out)
	return f.Close()
}

func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	in := fs.String("i", "-", "plan to read, or - for stdin")
	dryRun := fs.Bool("dry-run", false, "print what would be created without touching Rex")
	rexBin := fs.String("rex", "", "rex CLI (default: $REX_BIN, $PATH, then the Rex app bundle)")
	fs.Parse(args)

	r, closeIn, err := openInput(*in)
	if err != nil {
		return err
	}
	defer closeIn()
	var plan convert.Plan
	if err := json.NewDecoder(r).Decode(&plan); err != nil {
		return fmt.Errorf("decode plan: %w", err)
	}

	var client rex.Client
	existing := map[string]bool{}
	bin, err := rex.FindBin(*rexBin)
	switch {
	case err == nil:
		client.Bin = bin
		if existing, err = client.SessionLabels(); err != nil {
			if !*dryRun {
				return err
			}
			fmt.Fprintln(os.Stderr, "warning: cannot list Rex sessions:", err)
		}
	case !*dryRun:
		return err
	}

	var created, skipped, failed int
	for _, s := range plan.Sessions {
		summary := fmt.Sprintf("%q (%d windows, %d terminals)", s.Label, len(s.InitialWindows), countBlocks(s))
		switch {
		case existing[s.Label]:
			skipped++
			fmt.Printf("skip    %s: a Rex session with this label exists\n", summary)
		case *dryRun:
			created++
			fmt.Printf("create  %s\n", summary)
		default:
			id, err := client.Create(s)
			if err != nil {
				failed++
				fmt.Printf("failed  %s: %v\n", summary, err)
				continue
			}
			created++
			existing[s.Label] = true
			fmt.Printf("created %s: %s\n", summary, id)
		}
	}
	verb := "created"
	if *dryRun {
		verb = "would create"
	}
	fmt.Printf("\n%s %d, skipped %d, failed %d\n", verb, created, skipped, failed)
	if failed > 0 {
		return errors.New("some sessions were not created")
	}
	return nil
}

func countBlocks(s rex.CreateSession) int {
	var count func(rex.Node) int
	count = func(n rex.Node) int {
		if n.Split != nil {
			return count(n.Split.Before) + count(n.Split.After)
		}
		return 1
	}
	total := 0
	for _, w := range s.InitialWindows {
		total += count(w.Layout)
	}
	return total
}

func openInput(path string) (io.Reader, func(), error) {
	if path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}
