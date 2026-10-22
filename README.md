# cmux-to-rex

Export the cmux sidebar layout and recreate it as Rex sessions.

```sh
go build -o bin/cmux-to-rex .

bin/cmux-to-rex export -o cmux.json               # cmux restore-session --export
bin/cmux-to-rex convert -i cmux.json -o plan.json # cmux export -> Rex session.create requests
bin/cmux-to-rex apply -i plan.json --dry-run      # show what would be created
bin/cmux-to-rex apply -i plan.json
```

## Mapping

| cmux | Rex |
| --- | --- |
| Sidebar workspace | Session (created in sidebar order) |
| Split tree | First window, same directions and ratios, showing each pane's selected tab |
| Other tabs in a pane | One extra window each (Rex panes have no tabs) |
| Tab title / directory | Block label / shell `cwd` |

Only terminal tabs are converted; browser and other tabs are reported as warnings.

`convert` options:

- `--group NAME` (repeatable): only convert workspaces in these sidebar groups.
- `--group-prefix`: label sessions `group/title`, since Rex has no sidebar groups.
- `--resume-agents`: type each tab's saved cmux agent resume command (for example
  `claude --resume <id>`) into its new shell. Off by default. Agent environment
  variables are never copied.

## Safety

- `export` never overwrites a file, and `convert -o` refuses an existing file.
- `apply` only calls Rex `session.create`, which is atomic per session. It never
  modifies or closes existing sessions and skips any plan session whose label
  already exists, so rerunning it is safe.
- The rex CLI is found via `--rex`, `$REX_BIN`, `$PATH`, then the copy bundled in
  the Rex app.
