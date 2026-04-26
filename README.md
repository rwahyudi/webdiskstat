# webdiskstat

`webdiskstat` converts JSON from `gdu -o-` into a self-contained HTML disk usage report with a WinDirStat-style directory list and treemap.

## Requirements

- Python 3.10 or newer
- `gdu`

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

## Options

```text
usage: webdiskstat.py [input] [-o OUTPUT]
```

- `input`: gdu JSON file, `.gz` file, or `-` for stdin. Defaults to stdin.
- `-o, --output`: output HTML path. Defaults to `webdiskstat.html`.

Running the script without piped input or an input file prints the usage instructions.

## Interface

- The left panel lists the current directory entries, including modified time when `gdu` provides it.
- Columns are sortable by name, item count, and size.
- File and item counts use comma grouping.
- The treemap shows the current directory, including directories and files directly inside that directory.
- Directory tiles use distinct shaded colors. File tiles are colored by extension.
- The home view shows a smaller treemap and a framed top 10 biggest files list.
- Double-click a top 10 file to jump to the directory containing that file.
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
