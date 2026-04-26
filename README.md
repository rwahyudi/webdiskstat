# webdiskstat

`webdiskstat` converts JSON from `gdu -o-` into a self-contained HTML disk usage report with a WinDirStat-style directory list and treemap.

## Requirements

- Python 3.10 or newer
- `gdu`

## Download and Install

Clone the repository:

```sh
git clone https://github.com/rwahyudi/webdiskstat.git
cd webdiskstat
```

Run it from the cloned directory:

```sh
gdu -o- /path/to/scan | ./webdiskstat.py -o webdiskstat.html
```

Optional: make it available from your shell path:

```sh
install -Dm755 webdiskstat.py ~/.local/bin/webdiskstat
```

Then run:

```sh
gdu -o- /path/to/scan | webdiskstat -o webdiskstat.html
```

## Quick Start

Generate a report directly from `gdu`:

```sh
gdu -o- /path/to/scan | ./webdiskstat.py -o diskstats.html
```

Open `diskstats.html` in a browser.

You can also save the `gdu` JSON first:

```sh
gdu -o report.json /path/to/scan
./webdiskstat.py report.json -o diskstats.html
```

Read compressed JSON:

```sh
zcat report.json.gz | ./webdiskstat.py -o diskstats.html
```

## Example

Open the included sample report in the repository: [example/report.html](example/report.html).

Preview it in a browser through HTMLPreview: [webdiskstat example report](https://htmlpreview.github.io/?https://github.com/rwahyudi/webdiskstat/blob/main/example/report.html).

## Options

```text
usage: webdiskstat.py [input] [-o OUTPUT]
```

- `input`: gdu JSON file, `.gz` file, or `-` for stdin. Defaults to stdin.
- `-o, --output`: output HTML path. Defaults to `webdiskstat.html`.

Running the script without piped input or an input file prints the usage instructions.

## Interface

- The left panel lists the current directory entries, including modified time when `gdu` provides it.
- Columns are sortable by name, item count, file count, size, and modified date.
- File and item counts use comma grouping.
- The treemap shows the current directory, including directories and files directly inside that directory.
- Directory tiles use distinct shaded colors. File tiles are colored by extension.
- The divider between the directory list and right panel can be dragged to resize the right panel.
- The home view shows a smaller treemap and a framed biggest-files list.
- The biggest-files list can show 10 to 50 entries and paginates 10 rows at a time.
- The home view divider can be dragged to resize the treemap and biggest-files list.
- Double-click a listed file to jump to the directory containing that file.
- Details show size, percentage, type, extension, item count, file count, and modified time when `gdu` provides it.
- Generated date and time are shown in the browser title and footer.
- The Help button in the toolbar explains features, mouse actions, keyboard shortcuts, and navigation.
- The toolbar theme switch toggles between the default dark theme and a light theme.

## Keyboard and Navigation

- `Arrow Up` / `Arrow Down`: move selection in the directory list.
- `Home` / `End`: jump to first or last item.
- `Enter` or `Arrow Right`: enter the selected directory.
- `Backspace` or `Arrow Left`: go up one directory.
- The URL hash changes as you navigate, so directory views are bookmarkable.

## Notes

The output is a static HTML file. After generation, it does not need Python or `gdu` to view the report.

The scan data is embedded in a compact string-table format and expanded by the browser when the report loads.

This script was vibe-coded.
