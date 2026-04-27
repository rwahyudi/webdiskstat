# webdiskstat

`webdiskstat` converts JSON from `gdu -o-` or `ncdu -o-` into a self-contained HTML disk usage report with a WinDirStat-style directory list and treemap.

## Requirements

- Python 3.10 or newer
- `gdu` or `ncdu`

## Features

- Converts `gdu -o-` JSON into a single self-contained static HTML report by default.
- Supports `ncdu -o-` JSON with `--input-type ncdu`.
- Reads JSON from stdin, a saved JSON file, or a `.gz` compressed JSON file.
- Writes to an HTML file or stdout with `-o -`.
- Embeds scan data as a gzip-compressed compact string-table payload.
- Optionally encrypts embedded scan data with `--password` using PBKDF2-SHA256 and ChaCha20-Poly1305.
- Encrypted reports prompt for a password in the browser before loading scan data.
- Encrypted reports use Web Crypto when available and include a JavaScript fallback for `file://` and other non-HTTPS schemes.
- Shows an encrypted or unencrypted data indicator in the footer.
- Displays a WinDirStat-style directory list with name, item count, file count, size, modified date, and percentage columns.
- Sorts directory rows by name, item count, file count, size, or modified date.
- Uses comma grouping for file and item counts.
- Shows an interactive treemap for the current directory.
- Colors directory tiles distinctly and file tiles by extension.
- Shows a home view with a smaller treemap and a biggest-files list.
- Lets the biggest-files list show 10 to 50 entries and scroll when needed.
- Supports double-click navigation into directories and from biggest-file rows to containing directories.
- Shows details for the selected entry, including size, percentage, type, extension, item count, file count, and modified time when available.
- Supports breadcrumb navigation, parent navigation, bookmarkable URL hashes, and browser back/forward navigation.
- Supports keyboard navigation with arrow keys, Home, End, Enter, Backspace, and Escape.
- Lets users resize the main split pane and the home-view treemap/list split.
- Includes a dark theme, a light theme, and persistent theme/pane-size preferences when browser storage is available.
- Includes an in-report Help dialog for features, mouse actions, keyboard shortcuts, and navigation.
- Shows the generated date and time in the browser title and footer.
- Works as a static report after generation without Python or `gdu`.

## Screenshot

![webdiskstat report showing the directory list, treemap, details panel, and data security footer](docs/assets/webdiskstat-screenshot.png)

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

Generate a report from `ncdu`:

```sh
ncdu -o- /path/to/scan | ./webdiskstat.py --input-type ncdu -o diskstats.html
```

You can also save the scanner JSON first:

```sh
gdu -o report.json /path/to/scan
./webdiskstat.py report.json -o diskstats.html
```

For a saved `ncdu` export, pass the input type explicitly:

```sh
ncdu -o report.json /path/to/scan
./webdiskstat.py --input-type ncdu report.json -o diskstats.html
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
usage: webdiskstat.py [-h] [--input-type {gdu,ncdu}] [-o OUTPUT] [--password PASSWORD] [input]
```

- `input`: scanner JSON file, `.gz` file, or `-` for stdin. Defaults to stdin.
- `--input-type`: input JSON format, either `gdu` or `ncdu`. Defaults to `gdu`.
- `-o, --output`: output HTML path. Defaults to `webdiskstat.html`.
- `--password PASSWORD`: encrypt the embedded scan data. Defaults to unencrypted.

Running the script without piped input or an input file prints the usage instructions.

Encrypt the embedded report data:

```sh
./webdiskstat.py report.json -o diskstats.html --password 'choose-a-strong-password'
```

## Interface

- The left panel lists the current directory entries, including modified time when the scan data provides it.
- Columns are sortable by name, item count, file count, size, and modified date.
- File and item counts use comma grouping.
- The treemap shows the current directory, including directories and files directly inside that directory.
- Directory tiles use distinct shaded colors. File tiles are colored by extension.
- The divider between the directory list and right panel can be dragged to resize the right panel.
- The home view shows a smaller treemap and a framed biggest-files list.
- The biggest-files list can show 10 to 50 entries and scrolls when the list is taller than the pane.
- The home view divider can be dragged to resize the treemap and biggest-files list.
- Double-click a listed file to jump to the directory containing that file.
- Details show size, percentage, type, extension, item count, file count, and modified time when the scan data provides it.
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

The output is a static HTML file. After generation, it does not need Python, `gdu`, or `ncdu` to view the report.

The scan data is embedded as a gzip-compressed compact string-table payload and expanded by the browser when the report loads.
Viewing generated reports requires a browser with the standard `DecompressionStream` API.
Encrypted reports prompt for the password before loading the scan data. They use the standard Web Crypto API when available and include a slower JavaScript fallback for `file://` and other non-HTTPS schemes.
Unencrypted reports disclose the scan metadata embedded in the HTML. Encrypted reports still depend on password strength, and command-line passwords may be visible in shell history or process lists.

This script was vibe-coded.
