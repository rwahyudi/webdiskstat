#!/usr/bin/env python3
"""
Convert JSON exported by `gdu -o-` or `ncdu -o-` into a self-contained
WinDirStat-like HTML report.

Examples:
  gdu -o- /home | ./webdiskstat.py -o report.html
  ncdu -o- /home | ./webdiskstat.py --input-type ncdu -o report.html
  zcat report.json.gz | ./webdiskstat.py -o report.html
"""

from __future__ import annotations

import argparse
import base64
from datetime import datetime
import gzip
import hashlib
import html
import json
import mimetypes
import posixpath
import re
import secrets
import sys
from pathlib import Path
from typing import Any


APP_TITLE = "webdiskstat"
REPORT_SIZE_PLACEHOLDER = "__WEBDISKSTAT_REPORT_SIZE__"
ENCRYPTION_AAD = b"webdiskstat-report-data-v1"
ENCRYPTION_ALGORITHM = "ChaCha20-Poly1305"
PBKDF2_ITERATIONS = 310_000

CHILD_KEYS = (
    "items",
    "Items",
    "children",
    "Children",
    "entries",
    "Entries",
    "files",
    "Files",
    "dirs",
    "Dirs",
    "nodes",
    "Nodes",
)

NAME_KEYS = ("name", "Name", "path", "Path", "fullPath", "FullPath")
PATH_KEYS = ("path", "Path", "fullPath", "FullPath")
SIZE_KEYS = (
    "usage",
    "Usage",
    "size",
    "Size",
    "diskUsage",
    "DiskUsage",
    "disk_usage",
    "dsize",
    "Dsize",
    "asize",
    "Asize",
    "blocks",
    "Blocks",
    "apparentSize",
    "ApparentSize",
    "apparent_size",
    "total",
    "Total",
)
DIR_KEYS = ("isDir", "IsDir", "dir", "Dir", "directory", "Directory")
MTIME_KEYS = ("mtime", "Mtime", "modTime", "ModTime", "modified", "Modified")
FLAG_KEYS = ("flag", "Flag", "flags", "Flags")


class NoInputError(ValueError):
    """Raised when stdin was selected but no JSON was provided."""


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Build a static WinDirStat-like web report from gdu or ncdu JSON.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Examples:\n"
            "  gdu -o- / | %(prog)s -o webdiskstat.html\n"
            "  ncdu -o- / | %(prog)s --input-type ncdu -o webdiskstat.html\n"
            "  zcat report.json.gz | %(prog)s -o report.html\n"
        ),
    )
    parser.add_argument(
        "input",
        nargs="?",
        default="-",
        help="Input JSON file, .gz file, or '-' for stdin. Defaults to stdin.",
    )
    parser.add_argument(
        "--input-type",
        choices=("gdu", "ncdu"),
        default="gdu",
        help="Input JSON format. Defaults to gdu.",
    )
    parser.add_argument(
        "-o",
        "--output",
        default="webdiskstat.html",
        help="HTML output path, or '-' for stdout. Defaults to webdiskstat.html.",
    )
    parser.add_argument(
        "--password",
        default=None,
        help="Encrypt embedded report data with this password. Defaults to unencrypted.",
    )
    args = parser.parse_args()

    if args.password == "":
        parser.error("--password must not be empty")

    if args.input == "-" and sys.stdin.isatty():
        print_no_input_help(parser)
        return 2

    try:
        raw = read_json(args.input)
        root = normalize_export(raw, args.input_type)
        report = render_report(root, args.password)
    except NoInputError:
        print_no_input_help(parser)
        return 2
    except Exception as exc:
        print(f"webdiskstat: {exc}", file=sys.stderr)
        return 1

    output_path = write_report(report, args.output)
    if args.output == "-":
        return 0

    print(f"Wrote {output_path}", file=sys.stderr)
    return 0


def read_json(source: str) -> Any:
    if source == "-":
        text = sys.stdin.buffer.read()
        if not text.strip():
            raise NoInputError("stdin is empty")
        if text.startswith(b"\x1f\x8b"):
            text = gzip.decompress(text)
        return json.loads(text)

    path = Path(source)
    if not path.exists():
        raise FileNotFoundError(f"{source!r} does not exist")

    if path.suffix == ".gz":
        with gzip.open(path, "rt", encoding="utf-8") as handle:
            return json.load(handle)

    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def print_no_input_help(parser: argparse.ArgumentParser) -> None:
    print("No input provided. Pipe gdu or ncdu JSON into the script or pass a saved JSON file.", file=sys.stderr)
    print(file=sys.stderr)
    parser.print_help(file=sys.stderr)


def write_report(report: str, output: str) -> Path:
    if output == "-":
        sys.stdout.write(report)
        return Path("<stdout>")

    path = Path(output).expanduser().resolve()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(report, encoding="utf-8")
    return path


def normalize_export(raw: Any, input_type: str = "gdu") -> dict[str, Any]:
    if input_type not in ("gdu", "ncdu"):
        raise ValueError(f"unsupported input type: {input_type}")

    if is_export_array(raw):
        if input_type == "ncdu":
            validate_ncdu_export(raw)
        raw = raw[3]
    elif input_type == "ncdu":
        raise ValueError("expected ncdu JSON export array from `ncdu -o-`")

    if isinstance(raw, list):
        if is_sequence_node(raw):
            root = normalize_node(raw, "root", "", input_type)
            add_totals(root)
            return root

        children = [normalize_node(item, f"item-{index}", "", input_type) for index, item in enumerate(raw)]
        root = {
            "name": "root",
            "path": "root",
            "size": sum(child["size"] for child in children),
            "type": "dir",
            "ext": "",
            "children": children,
        }
        add_totals(root)
        return root

    if isinstance(raw, dict):
        node = unwrap_possible_root(raw)
        root = normalize_node(node, "root", "", input_type)
        add_totals(root)
        return root

    raise ValueError("expected a JSON object or array")


def is_export_array(raw: Any) -> bool:
    return (
        isinstance(raw, list)
        and len(raw) >= 4
        and isinstance(raw[0], int)
        and isinstance(raw[1], int)
        and isinstance(raw[2], dict)
        and isinstance(raw[3], (dict, list))
    )


def validate_ncdu_export(raw: list[Any]) -> None:
    if raw[0] != 1:
        raise ValueError(f"unsupported ncdu export major version: {raw[0]}")
    if not isinstance(raw[1], int):
        raise ValueError("invalid ncdu export minor version")
    if not isinstance(raw[3], list) or not is_sequence_node(raw[3]):
        raise ValueError("invalid ncdu export directory tree")


def unwrap_possible_root(raw: dict[str, Any]) -> Any:
    if looks_like_node(raw):
        return raw

    for key in ("root", "Root", "data", "Data", "scan", "Scan", "tree", "Tree"):
        value = raw.get(key)
        if isinstance(value, (dict, list)):
            return value

    dict_values = [value for value in raw.values() if isinstance(value, dict)]
    if len(dict_values) == 1:
        return dict_values[0]

    return raw


def looks_like_node(value: dict[str, Any]) -> bool:
    return any(key in value for key in NAME_KEYS + SIZE_KEYS + CHILD_KEYS)


def is_sequence_node(raw: Any) -> bool:
    return isinstance(raw, list) and bool(raw) and isinstance(raw[0], dict) and looks_like_node(raw[0])


def normalize_node(
    raw: Any,
    fallback_name: str,
    parent_path: str,
    input_type: str = "gdu",
) -> dict[str, Any]:
    if isinstance(raw, dict):
        mapped = normalize_mapping_node(raw, fallback_name, parent_path, input_type)
        return mapped

    if isinstance(raw, list):
        if is_sequence_node(raw):
            return normalize_sequence_node(raw, fallback_name, parent_path, input_type)

        children = [normalize_node(item, fallback_name, parent_path, input_type) for item in raw]
        return {
            "name": fallback_name,
            "path": make_path(parent_path, fallback_name),
            "size": sum(child["size"] for child in children),
            "type": "dir",
            "ext": "",
            "children": children,
        }

    size = numberish(raw)
    return {
        "name": fallback_name,
        "path": make_path(parent_path, fallback_name),
        "size": size,
        "type": "file",
        "ext": extension_for(fallback_name),
        "children": [],
    }


def normalize_sequence_node(
    raw: list[Any],
    fallback_name: str,
    parent_path: str,
    input_type: str = "gdu",
) -> dict[str, Any]:
    info = raw[0]
    name_value = first_string(info, NAME_KEYS)
    path_value = first_string(info, PATH_KEYS)
    if path_value and (not name_value or name_value == path_value):
        name = display_name_from_path(path_value)
    else:
        name = name_value or fallback_name
    path = path_value or make_path(parent_path, name)

    children = [
        normalize_node(child, f"item-{index}", path, input_type)
        for index, child in enumerate(raw[1:])
    ]
    size = first_number(info, SIZE_KEYS)
    child_size = sum(child["size"] for child in children)
    if input_type == "ncdu" and children:
        size += child_size
    elif size <= 0:
        size = child_size

    node = {
        "name": name,
        "path": path,
        "size": size,
        "type": "dir" if children or first_bool(info, DIR_KEYS) else "file",
        "ext": "",
        "children": sorted(children, key=lambda child: child["size"], reverse=True),
    }

    mtime = first_scalar(info, MTIME_KEYS)
    if mtime not in ("", None):
        node["mtime"] = str(mtime)

    flag = first_string(info, FLAG_KEYS)
    if flag:
        node["flag"] = flag

    return node


def normalize_mapping_node(
    raw: dict[str, Any],
    fallback_name: str,
    parent_path: str,
    input_type: str = "gdu",
) -> dict[str, Any]:
    children_raw = extract_children(raw)
    path_value = first_string(raw, PATH_KEYS)
    name_value = first_string(raw, NAME_KEYS)

    if path_value and (not name_value or name_value == path_value):
        name = display_name_from_path(path_value)
    else:
        name = name_value or fallback_name

    path = path_value or make_path(parent_path, name)
    if parent_path and path == name:
        path = make_path(parent_path, name)

    children = []
    if isinstance(children_raw, dict):
        for child_name, child_value in children_raw.items():
            children.append(normalize_node(child_value, str(child_name), path, input_type))
    elif isinstance(children_raw, list):
        for index, child in enumerate(children_raw):
            children.append(normalize_node(child, f"item-{index}", path, input_type))
    elif children_raw is None:
        children = extract_mapping_children(raw, path, input_type)

    size = first_number(raw, SIZE_KEYS)
    if size <= 0 and children:
        size = sum(child["size"] for child in children)

    is_dir = bool(children) or first_bool(raw, DIR_KEYS)
    node_type = "dir" if is_dir else "file"

    node = {
        "name": name,
        "path": path,
        "size": size,
        "type": node_type,
        "ext": "" if is_dir else extension_for(name),
        "children": sorted(children, key=lambda child: child["size"], reverse=True),
    }

    mtime = first_scalar(raw, MTIME_KEYS)
    if mtime not in ("", None):
        node["mtime"] = str(mtime)

    flag = first_string(raw, FLAG_KEYS)
    if flag:
        node["flag"] = flag

    mime = mimetypes.guess_type(name)[0]
    if mime:
        node["mime"] = mime

    return node


def extract_children(raw: dict[str, Any]) -> Any | None:
    for key in CHILD_KEYS:
        value = raw.get(key)
        if isinstance(value, (list, dict)):
            return value

    for key, value in raw.items():
        if key in NAME_KEYS + SIZE_KEYS + PATH_KEYS + DIR_KEYS + MTIME_KEYS + FLAG_KEYS:
            continue
        if isinstance(value, list) and value and all(isinstance(item, dict) for item in value):
            return value

    return None


def extract_mapping_children(
    raw: dict[str, Any],
    parent_path: str,
    input_type: str = "gdu",
) -> list[dict[str, Any]]:
    if looks_like_node(raw):
        return []

    children = []
    for key, value in raw.items():
        if isinstance(value, (dict, list, int, float)):
            children.append(normalize_node(value, str(key), parent_path, input_type))
    return children


def add_totals(root: dict[str, Any]) -> None:
    next_id = 0

    def visit(node: dict[str, Any], depth: int) -> tuple[int, int]:
        nonlocal next_id
        node["id"] = next_id
        next_id += 1
        node["depth"] = depth

        total_count = 1
        file_count = 0 if node["type"] == "dir" else 1
        for child in node.get("children", []):
            child_count, child_files = visit(child, depth + 1)
            total_count += child_count
            file_count += child_files
        node["items"] = total_count - 1
        node["files"] = file_count
        return total_count, file_count

    visit(root, 0)


def first_string(raw: dict[str, Any], keys: tuple[str, ...]) -> str:
    for key in keys:
        value = raw.get(key)
        if isinstance(value, str) and value:
            return value
    return ""


def first_scalar(raw: dict[str, Any], keys: tuple[str, ...]) -> Any:
    for key in keys:
        value = raw.get(key)
        if isinstance(value, (str, int, float)) and value != "":
            return value
    return ""


def first_number(raw: dict[str, Any], keys: tuple[str, ...]) -> int:
    for key in keys:
        value = raw.get(key)
        number = numberish(value)
        if number > 0:
            return number
    return 0


def first_bool(raw: dict[str, Any], keys: tuple[str, ...]) -> bool:
    for key in keys:
        value = raw.get(key)
        if isinstance(value, bool):
            return value
    return False


def numberish(value: Any) -> int:
    if isinstance(value, bool):
        return 0
    if isinstance(value, (int, float)):
        return max(0, int(value))
    if isinstance(value, str):
        cleaned = value.strip().replace(",", "")
        if re.fullmatch(r"\d+(\.\d+)?", cleaned):
            return max(0, int(float(cleaned)))
    return 0


def display_name_from_path(path: str) -> str:
    trimmed = path.rstrip("/")
    if not trimmed:
        return path or "root"
    return posixpath.basename(trimmed) or trimmed


def make_path(parent: str, name: str) -> str:
    if not parent:
        return name
    if parent == "/":
        return "/" + name.strip("/")
    return parent.rstrip("/") + "/" + name.strip("/")


def extension_for(name: str) -> str:
    base = name.rsplit("/", 1)[-1]
    if "." not in base or base.startswith(".") and base.count(".") == 1:
        return "[no extension]"
    ext = base.rsplit(".", 1)[-1].lower()
    return "." + ext if ext else "[no extension]"


def serialize_report_data(root: dict[str, Any]) -> list[Any]:
    strings: list[str] = []
    string_indexes: dict[str, int] = {}

    def intern(value: Any) -> int:
        if value in ("", None):
            return -1
        text = str(value)
        index = string_indexes.get(text)
        if index is not None:
            return index
        index = len(strings)
        strings.append(text)
        string_indexes[text] = index
        return index

    def pack(node: dict[str, Any], parent_path: str) -> list[Any]:
        name = str(node.get("name") or "")
        path = str(node.get("path") or make_path(parent_path, name))
        expected_path = make_path(parent_path, name)
        node_type = 1 if node.get("type") == "dir" else 0
        children = [pack(child, path) for child in node.get("children", [])]
        return [
            intern(name),
            -1 if path == expected_path else intern(path),
            int(node.get("size") or 0),
            node_type,
            intern(node.get("ext") or ""),
            intern(node.get("mtime") or ""),
            intern(node.get("mime") or ""),
            intern(node.get("flag") or ""),
            children,
        ]

    return [strings, pack(root, "")]


def script_json(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).replace("</", "<\\/")


def compressed_script_json_bytes(value: Any) -> bytes:
    raw = script_json(value).encode("utf-8")
    return gzip.compress(raw, compresslevel=9, mtime=0)


def report_data_payload(root: dict[str, Any], password: str | None) -> str:
    compressed = compressed_script_json_bytes(serialize_report_data(root))
    if password is None:
        return script_json({
            "encrypted": False,
            "payload": base64.b64encode(compressed).decode("ascii"),
        })

    encrypted = encrypt_report_data(compressed, password)
    return script_json(encrypted)


def encrypt_report_data(plaintext: bytes, password: str) -> dict[str, Any]:
    salt = secrets.token_bytes(16)
    nonce = secrets.token_bytes(12)
    key = hashlib.pbkdf2_hmac(
        "sha256",
        password.encode("utf-8"),
        salt,
        PBKDF2_ITERATIONS,
        dklen=32,
    )
    ciphertext, tag = chacha20_poly1305_encrypt(key, nonce, plaintext, ENCRYPTION_AAD)
    return {
        "encrypted": True,
        "algorithm": ENCRYPTION_ALGORITHM,
        "kdf": "PBKDF2-SHA256",
        "iterations": PBKDF2_ITERATIONS,
        "salt": base64.b64encode(salt).decode("ascii"),
        "nonce": base64.b64encode(nonce).decode("ascii"),
        "aad": ENCRYPTION_AAD.decode("ascii"),
        "payload": base64.b64encode(ciphertext).decode("ascii"),
        "tag": base64.b64encode(tag).decode("ascii"),
    }


def chacha20_poly1305_encrypt(
    key: bytes,
    nonce: bytes,
    plaintext: bytes,
    aad: bytes,
) -> tuple[bytes, bytes]:
    if len(key) != 32:
        raise ValueError("encryption key must be 32 bytes")
    if len(nonce) != 12:
        raise ValueError("encryption nonce must be 12 bytes")

    poly_key = chacha20_block(key, nonce, 0)[:32]
    ciphertext = chacha20_xor(key, nonce, 1, plaintext)
    tag = poly1305_mac(poly1305_aead_data(aad, ciphertext), poly_key)
    return ciphertext, tag


def chacha20_xor(key: bytes, nonce: bytes, counter: int, data: bytes) -> bytes:
    output = bytearray(len(data))
    for offset in range(0, len(data), 64):
        block = chacha20_block(key, nonce, counter)
        chunk = data[offset:offset + 64]
        for index, value in enumerate(chunk):
            output[offset + index] = value ^ block[index]
        counter = (counter + 1) & 0xFFFFFFFF
        if counter == 0 and offset + 64 < len(data):
            raise ValueError("report data is too large to encrypt with one nonce")
    return bytes(output)


def chacha20_block(key: bytes, nonce: bytes, counter: int) -> bytes:
    def word(data: bytes, offset: int) -> int:
        return int.from_bytes(data[offset:offset + 4], "little")

    state = [
        0x61707865,
        0x3320646E,
        0x79622D32,
        0x6B206574,
        *[word(key, offset) for offset in range(0, 32, 4)],
        counter & 0xFFFFFFFF,
        *[word(nonce, offset) for offset in range(0, 12, 4)],
    ]
    working = state[:]

    for _ in range(10):
        quarter_round(working, 0, 4, 8, 12)
        quarter_round(working, 1, 5, 9, 13)
        quarter_round(working, 2, 6, 10, 14)
        quarter_round(working, 3, 7, 11, 15)
        quarter_round(working, 0, 5, 10, 15)
        quarter_round(working, 1, 6, 11, 12)
        quarter_round(working, 2, 7, 8, 13)
        quarter_round(working, 3, 4, 9, 14)

    return b"".join(
        ((working[index] + state[index]) & 0xFFFFFFFF).to_bytes(4, "little")
        for index in range(16)
    )


def quarter_round(state: list[int], a: int, b: int, c: int, d: int) -> None:
    state[a] = (state[a] + state[b]) & 0xFFFFFFFF
    state[d] = rotate_left(state[d] ^ state[a], 16)
    state[c] = (state[c] + state[d]) & 0xFFFFFFFF
    state[b] = rotate_left(state[b] ^ state[c], 12)
    state[a] = (state[a] + state[b]) & 0xFFFFFFFF
    state[d] = rotate_left(state[d] ^ state[a], 8)
    state[c] = (state[c] + state[d]) & 0xFFFFFFFF
    state[b] = rotate_left(state[b] ^ state[c], 7)


def rotate_left(value: int, bits: int) -> int:
    return ((value << bits) & 0xFFFFFFFF) | (value >> (32 - bits))


def poly1305_aead_data(aad: bytes, ciphertext: bytes) -> bytes:
    def padding(length: int) -> bytes:
        return b"\x00" * ((16 - length % 16) % 16)

    return b"".join((
        aad,
        padding(len(aad)),
        ciphertext,
        padding(len(ciphertext)),
        len(aad).to_bytes(8, "little"),
        len(ciphertext).to_bytes(8, "little"),
    ))


def poly1305_mac(message: bytes, key: bytes) -> bytes:
    if len(key) != 32:
        raise ValueError("Poly1305 key must be 32 bytes")

    r = bytearray(key[:16])
    r[3] &= 15
    r[7] &= 15
    r[11] &= 15
    r[15] &= 15
    r[4] &= 252
    r[8] &= 252
    r[12] &= 252

    r_value = int.from_bytes(r, "little")
    s_value = int.from_bytes(key[16:], "little")
    modulus = (1 << 130) - 5
    accumulator = 0

    for offset in range(0, len(message), 16):
        block = message[offset:offset + 16]
        number = int.from_bytes(block + b"\x01", "little")
        accumulator = ((accumulator + number) * r_value) % modulus

    tag = (accumulator + s_value) % (1 << 128)
    return tag.to_bytes(16, "little")


def minify_css(css: str) -> str:
    css = re.sub(r"/\*.*?\*/", "", css, flags=re.S)
    css = re.sub(r"\s+", " ", css)
    css = re.sub(r"\s*([{}:;,>])\s*", r"\1", css)
    css = re.sub(r";}", "}", css)
    return css.strip()


def optimize_report_html(report: str) -> str:
    blocks: list[str] = []

    def protect(pattern: str, text: str, transform=lambda value: value) -> str:
        def replace(match: re.Match[str]) -> str:
            token = f"@@WEBDISKSTAT_BLOCK_{len(blocks)}@@"
            blocks.append(transform(match.group(0)))
            return token

        return re.sub(pattern, replace, text, flags=re.S | re.I)

    def optimize_style(block: str) -> str:
        match = re.fullmatch(r"(<style>)(.*?)(</style>)", block, flags=re.S | re.I)
        if not match:
            return block
        return match.group(1) + minify_css(match.group(2)) + match.group(3)

    report = protect(r"<script>.*?</script>", report)
    report = protect(r"<style>.*?</style>", report, optimize_style)
    report = re.sub(r">\s+<", "><", report)
    report = "\n".join(line.strip() for line in report.splitlines() if line.strip())

    for index, block in enumerate(blocks):
        report = report.replace(f"@@WEBDISKSTAT_BLOCK_{index}@@", block)
    return report + "\n"


def format_report_file_size(size: int) -> str:
    units = ("bytes", "KiB", "MiB", "GiB")
    value = float(size)
    unit = units[0]
    for unit in units:
        if value < 1024 or unit == units[-1]:
            break
        value /= 1024

    if unit == "bytes":
        amount = f"{size:,}"
    elif value >= 100:
        amount = f"{value:,.0f}"
    elif value >= 10:
        amount = f"{value:,.1f}"
    else:
        amount = f"{value:,.2f}"
    return f"HTML file: {amount} {unit}"


def fill_report_size(report: str) -> str:
    size_label = format_report_file_size(len(report.encode("utf-8")))
    for _ in range(10):
        candidate = report.replace(REPORT_SIZE_PLACEHOLDER, size_label)
        next_label = format_report_file_size(len(candidate.encode("utf-8")))
        if next_label == size_label:
            return candidate
        size_label = next_label
    return report.replace(REPORT_SIZE_PLACEHOLDER, size_label)


def render_report(root: dict[str, Any], password: str | None = None) -> str:
    data = report_data_payload(root, password)
    encrypted_report = password is not None
    generated_at = datetime.now().astimezone()
    generated_iso = generated_at.isoformat(timespec="seconds")
    generated_display = generated_at.strftime("%Y-%m-%d %H:%M:%S %Z")
    escaped_title = html.escape(f"{APP_TITLE} - Generated {generated_display}")
    security_class = "encrypted" if encrypted_report else "plain"
    security_label = "Data encrypted" if encrypted_report else "Data not encrypted"
    security_title = (
        "Embedded scan data is encrypted and requires the report password."
        if encrypted_report
        else "Embedded scan data is not encrypted."
    )
    security_icon = (
        '<path d="M7 11V8a5 5 0 0 1 10 0v3"/>'
        if encrypted_report
        else '<path d="M8 11V8a4 4 0 0 1 7.6-1.8"/>'
    )
    footer_status = (
        f'<span class="report-security {security_class}" title="{html.escape(security_title)}">'
        '<svg class="security-icon" viewBox="0 0 24 24" aria-hidden="true">'
        f'{security_icon}'
        '<rect x="5" y="11" width="14" height="9" rx="2"/>'
        '<path d="M12 15v2"/>'
        '</svg>'
        f'<span>{html.escape(security_label)}</span>'
        '</span>'
    )

    report = f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{escaped_title}</title>
<script>
try {{
  document.documentElement.dataset.theme = localStorage.getItem("webdiskstat-theme") === "light" ? "light" : "dark";
}} catch (error) {{
  document.documentElement.dataset.theme = "dark";
}}
</script>
<style>
:root {{
  color-scheme: dark;
  --bg: #14171c;
  --panel: #20252c;
  --panel-2: #2a3038;
  --panel-3: #171b21;
  --control: #1b2027;
  --line: #36404b;
  --subtle-line: #2a323c;
  --ink: #edf2f7;
  --muted: #9da9b6;
  --accent: #7bd7ff;
  --accent-2: #65e4c4;
  --warn: #f59e0b;
  --row-hover: #262d36;
  --row-active: #203747;
  --row-active-line: #38bdf8;
  --list-bar-top: rgba(123, 215, 255, 0.18);
  --list-bar: rgba(123, 215, 255, 0.14);
  --list-bar-bottom: rgba(123, 215, 255, 0.08);
  --list-bar-edge: rgba(123, 215, 255, 0.38);
  --tile-outline: #dbeafe;
  --shadow: 0 12px 28px rgba(0, 0, 0, 0.26);
  --shadow-soft: 0 1px 0 rgba(255, 255, 255, 0.04) inset;
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}}
html[data-theme="light"] {{
  color-scheme: light;
  --bg: #f4f7fb;
  --panel: #ffffff;
  --panel-2: #eef3f8;
  --panel-3: #e8edf3;
  --control: #f8fafc;
  --line: #cbd5e1;
  --subtle-line: #e2e8f0;
  --ink: #172033;
  --muted: #64748b;
  --accent: #0369a1;
  --accent-2: #0f766e;
  --row-hover: #eef6ff;
  --row-active: #dbeafe;
  --row-active-line: #0284c7;
  --list-bar-top: rgba(3, 105, 161, 0.12);
  --list-bar: rgba(3, 105, 161, 0.09);
  --list-bar-bottom: rgba(3, 105, 161, 0.04);
  --list-bar-edge: rgba(3, 105, 161, 0.26);
  --tile-outline: #0f172a;
  --shadow: 0 12px 28px rgba(15, 23, 42, 0.14);
  --shadow-soft: 0 1px 0 rgba(255, 255, 255, 0.75) inset;
}}
* {{ box-sizing: border-box; }}
html, body {{ height: 100%; }}
body {{
  margin: 0;
  background: linear-gradient(180deg, #171b21 0%, var(--bg) 46%, #101317 100%);
  color: var(--ink);
  overflow: hidden;
}}
body.resizing-home-pane {{
  cursor: row-resize;
  user-select: none;
}}
body.resizing-home-pane * {{
  cursor: row-resize !important;
}}
body.resizing-main-pane {{
  cursor: col-resize;
  user-select: none;
}}
body.resizing-main-pane * {{
  cursor: col-resize !important;
}}
button, input, select {{
  font: inherit;
}}
button:focus-visible, .row:focus-visible, .tile:focus-visible, .top-file-row:focus-visible, .main-resizer:focus-visible, .home-resizer:focus-visible {{
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}}
kbd {{
  display: inline-block;
  min-width: 1.75em;
  padding: 2px 6px;
  border: 1px solid #455262;
  border-bottom-color: #2a323c;
  border-radius: 5px;
  background: #151a20;
  color: #f8fafc;
  font: 11px/1.35 ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
  text-align: center;
  box-shadow: inset 0 1px 0 rgba(255,255,255,0.05);
}}
* {{
  scrollbar-color: #596575 #171b21;
  scrollbar-width: thin;
}}
*::-webkit-scrollbar {{
  width: 10px;
  height: 10px;
}}
*::-webkit-scrollbar-track {{
  background: #171b21;
}}
*::-webkit-scrollbar-thumb {{
  background: #596575;
  border: 2px solid #171b21;
  border-radius: 999px;
}}
*::-webkit-scrollbar-thumb:hover {{
  background: #728094;
}}
.app {{
  height: 100vh;
  display: grid;
  grid-template-rows: auto 1fr auto;
}}
.toolbar {{
  min-height: 58px;
  padding: 10px 16px;
  display: grid;
  grid-template-columns: minmax(180px, 1fr) auto;
  align-items: center;
  gap: 12px;
  border-bottom: 1px solid var(--line);
  background: linear-gradient(180deg, #242a32 0%, var(--panel) 100%);
  box-shadow: 0 10px 24px rgba(0, 0, 0, 0.18), var(--shadow-soft);
  z-index: 2;
}}
.crumbs {{
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  overflow: hidden;
  white-space: nowrap;
}}
.crumb {{
  border: 0;
  background: transparent;
  color: var(--accent);
  border-radius: 5px;
  padding: 5px 6px;
  cursor: pointer;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 260px;
}}
.crumb.root {{
  width: 30px;
  height: 30px;
  display: inline-grid;
  place-items: center;
  flex: 0 0 auto;
  border: 1px solid color-mix(in srgb, var(--accent) 35%, transparent);
  background: color-mix(in srgb, var(--accent) 10%, transparent);
}}
.crumb:hover {{
  background: color-mix(in srgb, var(--accent) 12%, transparent);
}}
.sep {{ color: var(--muted); }}
.generated {{
  color: var(--muted);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}}
.footer-meta {{
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  min-width: 0;
}}
.report-size {{
  color: var(--muted);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}}
.report-security {{
  min-height: 22px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 8px;
  border: 1px solid var(--line);
  border-radius: 999px;
  background: color-mix(in srgb, var(--panel-2) 78%, black 22%);
  color: var(--muted);
  font-size: 12px;
  font-weight: 650;
  line-height: 1;
  white-space: nowrap;
}}
.report-security.encrypted {{
  border-color: color-mix(in srgb, var(--accent-2) 60%, var(--line));
  background: color-mix(in srgb, var(--accent-2) 14%, transparent);
  color: #99f6e4;
}}
.report-security.plain {{
  border-color: color-mix(in srgb, var(--warn) 50%, var(--line));
  background: color-mix(in srgb, var(--warn) 10%, transparent);
  color: #fbbf24;
}}
.security-icon {{
  width: 14px;
  height: 14px;
  flex: 0 0 auto;
  stroke: currentColor;
  fill: none;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}}
.tools {{
  display: flex;
  align-items: center;
  gap: 8px;
}}
.theme-toggle {{
  position: relative;
  display: inline-flex;
  align-items: center;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
}}
.theme-input {{
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  opacity: 0;
  pointer-events: none;
}}
.theme-switch {{
  width: 64px;
  height: 34px;
  position: relative;
  display: inline-grid;
  grid-template-columns: 1fr 1fr;
  align-items: center;
  padding: 0 4px;
  border: 1px solid #3a4654;
  border-radius: 999px;
  background: linear-gradient(180deg, #222933 0%, #171d25 100%);
  color: #94a3b8;
  box-shadow:
    inset 0 1px 0 rgba(255,255,255,0.06),
    inset 0 -1px 0 rgba(0,0,0,0.22),
    0 1px 2px rgba(0,0,0,0.16);
  transition: border-color 160ms ease, background 160ms ease, box-shadow 160ms ease;
}}
.theme-icon {{
  width: 15px;
  height: 15px;
  justify-self: center;
  stroke: currentColor;
  fill: none;
  stroke-width: 2.15;
  stroke-linecap: round;
  stroke-linejoin: round;
  opacity: 0.58;
  transform: scale(0.94);
  transition: color 160ms ease, opacity 160ms ease, transform 160ms ease;
  z-index: 2;
}}
.theme-knob {{
  position: absolute;
  left: 3px;
  top: 3px;
  width: 28px;
  height: 28px;
  border-radius: 999px;
  border: 1px solid rgba(255,255,255,0.10);
  background: linear-gradient(180deg, #334155 0%, #202936 100%);
  box-shadow:
    0 2px 5px rgba(0,0,0,0.34),
    inset 0 1px 0 rgba(255,255,255,0.10);
  transition: transform 180ms cubic-bezier(.2, .8, .2, 1), background 160ms ease, border-color 160ms ease, box-shadow 160ms ease;
  z-index: 1;
}}
.theme-input:checked + .theme-switch .theme-knob {{
  transform: translateX(30px);
}}
.theme-input:not(:checked) + .theme-switch .moon-icon,
.theme-input:checked + .theme-switch .sun-icon {{
  color: #f8fafc;
  opacity: 0.96;
  transform: scale(1);
}}
.theme-input:focus-visible + .theme-switch {{
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}}
.theme-toggle:hover .theme-switch {{
  border-color: #526173;
  background: linear-gradient(180deg, #26303b 0%, #1b222b 100%);
  box-shadow:
    inset 0 1px 0 rgba(255,255,255,0.08),
    inset 0 -1px 0 rgba(0,0,0,0.20),
    0 2px 5px rgba(0,0,0,0.18);
}}
.icon-btn, .select {{
  border: 1px solid var(--line);
  border-radius: 7px;
  min-height: 36px;
  background: var(--control);
  color: var(--ink);
  box-shadow: var(--shadow-soft);
}}
.icon-btn {{
  width: 38px;
  display: inline-grid;
  place-items: center;
  cursor: pointer;
}}
.icon-btn:hover, .select:hover {{
  border-color: #64748b;
  background: #232a33;
}}
.icon {{
  width: 18px;
  height: 18px;
  display: block;
  stroke: currentColor;
  fill: none;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}}
.select {{
  padding: 0 28px 0 10px;
}}
.main {{
  --sidebar-size: 38vw;
  min-height: 0;
  display: grid;
  grid-template-columns: minmax(280px, var(--sidebar-size)) 10px minmax(360px, 1fr);
  background: var(--panel-3);
}}
.sidebar {{
  min-width: 0;
  min-height: 0;
  border-right: 1px solid var(--line);
  background: linear-gradient(180deg, #20252c 0%, #1c2128 100%);
  display: grid;
  grid-template-rows: auto 1fr;
}}
.main-resizer {{
  min-width: 10px;
  min-height: 0;
  border: 0;
  background: linear-gradient(90deg, transparent 0%, var(--line) 50%, transparent 100%);
  cursor: col-resize;
  display: flex;
  align-items: center;
  justify-content: center;
  touch-action: none;
}}
.main-resizer::before {{
  content: "";
  width: 4px;
  height: 72px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--line) 76%, var(--ink) 24%);
  box-shadow: 0 1px 0 rgba(255,255,255,0.08);
  transition: background 120ms ease, height 120ms ease;
}}
.main-resizer:hover::before,
.main-resizer.dragging::before,
.main-resizer:focus-visible::before {{
  height: 96px;
  background: var(--accent);
}}
.summary {{
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1px;
  background: var(--line);
  border-bottom: 1px solid var(--line);
}}
.metric {{
  background: linear-gradient(180deg, #242a32 0%, var(--panel) 100%);
  padding: 12px;
  min-width: 0;
}}
.label {{
  display: block;
  color: var(--muted);
  font-size: 11px;
  line-height: 1.2;
  text-transform: uppercase;
  letter-spacing: 0;
}}
.value {{
  display: block;
  margin-top: 4px;
  font-size: 15px;
  font-weight: 650;
  color: #f8fafc;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}}
.tree {{
  --tree-columns: minmax(150px, 1fr) 52px 52px 82px 126px 46px;
  --tree-min-width: 600px;
  min-height: 0;
  overflow: auto;
}}
.tree-body {{
  position: relative;
  min-width: var(--tree-min-width);
}}
.tree-body .row {{
  position: absolute;
  left: 0;
  right: 0;
  height: 36px;
  min-height: 36px;
}}
.tree-header {{
  min-height: 32px;
  display: grid;
  grid-template-columns: var(--tree-columns);
  align-items: center;
  gap: 8px;
  min-width: var(--tree-min-width);
  padding: 0 10px 0 12px;
  border-bottom: 1px solid var(--line);
  background: #1b2027;
  color: var(--muted);
  font-size: 11px;
  font-weight: 650;
  text-transform: uppercase;
  position: sticky;
  top: 0;
  z-index: 5;
  box-shadow: 0 1px 0 rgba(255,255,255,0.03), 0 8px 16px rgba(0,0,0,0.12);
}}
.tree-name-head {{
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 7px;
}}
.tree-name-head .tree-sort {{
  flex: 1 1 auto;
}}
.tree-parent-btn {{
  width: 24px;
  height: 24px;
  flex: 0 0 auto;
  border: 1px solid color-mix(in srgb, var(--line) 78%, transparent);
  border-radius: 6px;
  background: color-mix(in srgb, var(--control) 72%, transparent);
  color: var(--muted);
  display: inline-grid;
  place-items: center;
  padding: 0;
  cursor: pointer;
}}
.tree-parent-btn:hover:not(:disabled) {{
  border-color: color-mix(in srgb, var(--accent) 48%, var(--line));
  color: var(--ink);
  background: color-mix(in srgb, var(--accent) 10%, var(--control));
}}
.tree-parent-btn:disabled {{
  opacity: 0.42;
  cursor: default;
}}
.tree-parent-btn .icon {{
  width: 15px;
  height: 15px;
}}
.tree-columns-btn {{
  width: 26px;
  height: 24px;
  flex: 0 0 auto;
  border: 1px solid color-mix(in srgb, var(--line) 78%, transparent);
  border-radius: 6px;
  background: color-mix(in srgb, var(--control) 72%, transparent);
  color: var(--muted);
  display: inline-grid;
  place-items: center;
  padding: 0;
  cursor: pointer;
}}
.tree-columns-btn:hover,
.tree-columns-btn[aria-expanded="true"] {{
  border-color: color-mix(in srgb, var(--accent) 48%, var(--line));
  color: var(--ink);
  background: color-mix(in srgb, var(--accent) 10%, var(--control));
}}
.tree-columns-btn .icon {{
  width: 15px;
  height: 15px;
}}
.tree-columns-menu {{
  position: absolute;
  top: calc(100% + 6px);
  right: 10px;
  z-index: 20;
  min-width: 190px;
  padding: 9px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: #171d25;
  color: var(--ink);
  box-shadow: var(--shadow);
  text-transform: none;
}}
.tree-columns-menu[hidden] {{
  display: none;
}}
.tree-columns-title {{
  margin: 0 0 7px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 750;
  text-transform: uppercase;
  letter-spacing: .02em;
}}
.tree-column-option {{
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 28px;
  color: var(--ink);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}}
.tree-column-option input {{
  width: 14px;
  height: 14px;
  accent-color: var(--accent);
}}
.tree-sort {{
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  text-transform: inherit;
  padding: 0;
  text-align: inherit;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}}
.tree-sort:hover {{
  color: var(--ink);
}}
.tree-sort.sort-active {{
  color: var(--accent);
}}
.tree-sort.numeric, .tree-label.numeric {{
  text-align: right;
}}
.tree-label {{
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}}
.row {{
  min-height: 36px;
  display: grid;
  grid-template-columns: var(--tree-columns);
  align-items: center;
  gap: 8px;
  min-width: var(--tree-min-width);
  padding: 0 10px 0 12px;
  border-bottom: 1px solid var(--subtle-line);
  cursor: pointer;
  position: relative;
  transition: background 120ms ease, color 120ms ease;
}}
.row::before {{
  content: "";
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: var(--bar, 0%);
  background: linear-gradient(180deg, var(--list-bar-top) 0%, var(--list-bar) 48%, var(--list-bar-bottom) 100%);
  box-shadow: inset -2px 0 0 var(--list-bar-edge);
  pointer-events: none;
}}
.row:hover {{ background: var(--row-hover); }}
.row.dir {{
  font-weight: 620;
}}
.row.file {{
  color: #cbd5e1;
}}
.row.active {{
  background: var(--row-active);
  outline: 1px solid var(--row-active-line);
  outline-offset: -1px;
}}
.row.active::after {{
  content: "";
  position: absolute;
  left: 0;
  top: 6px;
  bottom: 6px;
  width: 3px;
  border-radius: 0 999px 999px 0;
  background: var(--accent);
}}
.row-name, .row-count, .row-size, .row-modified, .row-pct {{
  position: relative;
  z-index: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}}
.row-name {{
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}}
.row-kind {{
  min-width: 32px;
  padding: 2px 4px;
  border: 1px solid #475569;
  border-radius: 4px;
  background: #202733;
  color: #cbd5e1;
  font-size: 10px;
  font-weight: 750;
  line-height: 1.2;
  text-align: center;
  flex: 0 0 auto;
}}
.row.dir .row-kind {{
  border-color: color-mix(in srgb, var(--accent-2) 65%, #123b3a);
  background: #123835;
  color: #99f6e4;
}}
.swatch {{
  width: 10px;
  height: 10px;
  border-radius: 2px;
  flex: 0 0 auto;
  background: var(--row-color, #2563eb);
  box-shadow: inset 0 0 0 1px rgba(0,0,0,0.16);
}}
.row.dir .swatch {{
  width: 13px;
  height: 9px;
  margin-top: 3px;
  border-radius: 2px;
  position: relative;
}}
.row.dir .swatch::before {{
  content: "";
  position: absolute;
  left: 1px;
  top: -4px;
  width: 7px;
  height: 4px;
  border-radius: 2px 2px 0 0;
  background: inherit;
  box-shadow: inset 0 0 0 1px rgba(0,0,0,0.12);
}}
.row.file .swatch {{
  border-radius: 50%;
}}
.row-count, .row-size, .row-modified, .row-pct {{
  color: var(--muted);
  font-variant-numeric: tabular-nums;
  text-align: right;
  font-size: 12px;
}}
.row-modified {{
  text-align: left;
}}
.content {{
  --home-treemap-size: 36%;
  min-width: 0;
  min-height: 0;
  display: grid;
  grid-template-rows: 1fr auto;
}}
.content.home {{
  grid-template-rows: minmax(180px, var(--home-treemap-size)) 12px minmax(300px, 1fr);
}}
.treemap-frame {{
  min-width: 0;
  min-height: 0;
  margin: 14px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
  box-shadow: var(--shadow), var(--shadow-soft);
  overflow: hidden;
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
}}
.treemap-head {{
  min-width: 0;
  min-height: 36px;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  padding: 5px 8px;
  border-bottom: 1px solid var(--line);
  background: #1b2027;
}}
.treemap-cap-label {{
  color: var(--muted);
  font-size: 11px;
  font-weight: 750;
  text-transform: uppercase;
  letter-spacing: .02em;
  white-space: nowrap;
}}
.treemap-cap-select {{
  width: 78px;
  min-height: 26px;
  border: 1px solid var(--line);
  border-radius: 6px;
  background: var(--control);
  color: var(--ink);
  font-size: 12px;
  font-weight: 650;
}}
.treemap {{
  width: 100%;
  height: 100%;
  min-height: 0;
  position: relative;
  overflow: hidden;
  background: #15171a;
}}
.tile {{
  position: absolute;
  border: 1px solid rgba(255,255,255,0.36);
  overflow: hidden;
  cursor: pointer;
  color: rgba(255,255,255,0.95);
  background: var(--tile-color);
  background:
    linear-gradient(
      135deg,
      color-mix(in srgb, var(--tile-color, #2563eb) 88%, white 12%) 0%,
      var(--tile-color, #2563eb) 48%,
      color-mix(in srgb, var(--tile-color, #2563eb) 68%, black 32%) 100%
    );
  box-shadow: inset 0 0 0 1px rgba(0,0,0,0.14);
}}
.tile.dir {{
  border: 2px solid rgba(15, 23, 42, 0.42);
  background: var(--tile-color);
  background:
    linear-gradient(
      135deg,
      color-mix(in srgb, var(--tile-color, #0f766e) 82%, white 18%) 0%,
      var(--tile-color, #0f766e) 45%,
      color-mix(in srgb, var(--tile-color, #0f766e) 60%, black 40%) 100%
    );
  box-shadow:
    inset 0 0 0 2px rgba(255,255,255,0.58),
    inset 0 0 0 999px rgba(255,255,255,0.10);
}}
.tile.file {{
  border-color: rgba(255,255,255,0.42);
}}
.tile-kind {{
  position: absolute;
  right: 5px;
  bottom: 5px;
  z-index: 1;
  padding: 2px 4px;
  border-radius: 4px;
  background: rgba(15, 23, 42, 0.72);
  color: #fff;
  font-size: 10px;
  font-weight: 750;
  line-height: 1;
  letter-spacing: 0;
}}
.tile.dir .tile-label {{
  font-weight: 750;
  padding-right: 38px;
}}
.tile.has-children > .tile-kind {{
  top: 5px;
  bottom: auto;
}}
.tile.has-children > .tile-label {{
  position: relative;
  z-index: 2;
  -webkit-line-clamp: 1;
}}
.tile-children {{
  position: absolute;
  overflow: hidden;
  border-radius: 5px;
}}
.tile.nested {{
  border-width: 1px;
}}
.tile.nested .tile-kind {{
  display: none;
}}
.tile.nested .tile-label {{
  padding: 3px 4px;
  font-size: 10px;
  -webkit-line-clamp: 1;
}}
.tile:hover {{
  outline: 2px solid var(--tile-outline);
  outline-offset: -2px;
  z-index: 3;
}}
.tile.active {{
  outline: 3px solid var(--tile-outline);
  outline-offset: -3px;
  z-index: 4;
}}
.tile-label {{
  padding: 5px 6px;
  font-size: clamp(10px, 1.6vmin, 12px);
  line-height: 1.2;
  text-shadow: 0 1px 1px rgba(0,0,0,0.3);
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  word-break: break-word;
}}
.home-resizer {{
  min-height: 12px;
  margin: -4px 14px 4px;
  border: 0;
  border-radius: 999px;
  background: transparent;
  cursor: row-resize;
  display: flex;
  align-items: center;
  justify-content: center;
  touch-action: none;
}}
.home-resizer[hidden] {{
  display: none;
}}
.home-resizer::before {{
  content: "";
  width: 76px;
  height: 4px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--line) 76%, var(--ink) 24%);
  box-shadow: 0 1px 0 rgba(255,255,255,0.08);
  transition: background 120ms ease, width 120ms ease;
}}
.home-resizer:hover::before,
.home-resizer.dragging::before,
.home-resizer:focus-visible::before {{
  width: 96px;
  background: var(--accent);
}}
.top-files {{
  min-height: 0;
  overflow: hidden;
  margin: 0 14px 14px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: linear-gradient(180deg, #222831 0%, var(--panel) 100%);
  box-shadow: var(--shadow), inset 0 1px 0 rgba(255,255,255,0.03);
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
}}
.top-files[hidden] {{
  display: none;
}}
.top-file-row {{
  display: grid;
  grid-template-columns: minmax(0, 1fr) 120px;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
}}
.top-files-head {{
  min-height: 43px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto 120px;
  align-items: center;
  gap: 12px;
  padding: 8px 14px;
  border-bottom: 1px solid var(--subtle-line);
  background: #1b2027;
  color: var(--muted);
  font-size: 11px;
  font-weight: 750;
  text-transform: uppercase;
}}
.top-file-title {{
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}}
.top-file-controls {{
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  min-width: 0;
  text-transform: none;
  flex-wrap: wrap;
}}
.top-file-limit {{
  width: 72px;
  min-height: 28px;
  border: 1px solid var(--line);
  border-radius: 6px;
  background: var(--control);
  color: var(--ink);
  font-size: 12px;
  font-weight: 650;
}}
.top-files-body {{
  min-height: 0;
  overflow: auto;
}}
.top-file-row {{
  min-height: 54px;
  border-bottom: 1px solid var(--subtle-line);
  cursor: pointer;
  font-size: 13px;
  transition: background 120ms ease;
}}
.top-file-row:last-child {{
  border-bottom: 0;
}}
.top-file-row:hover {{
  background: var(--row-hover);
}}
.top-file-row.active {{
  background: var(--row-active);
  outline: 1px solid var(--row-active-line);
  outline-offset: -1px;
}}
.top-file-main {{
  min-width: 0;
}}
.top-file-name, .top-file-path {{
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}}
.top-file-name {{
  font-weight: 650;
}}
.top-file-path {{
  margin-top: 3px;
  color: var(--muted);
  font-size: 11px;
}}
.top-file-size {{
  color: var(--muted);
  font-variant-numeric: tabular-nums;
  text-align: right;
  white-space: nowrap;
  font-size: 12px;
}}
.details {{
  min-height: 82px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 12px;
  align-items: center;
  padding: 12px 16px;
  border-top: 1px solid var(--line);
  background: linear-gradient(180deg, #222831 0%, var(--panel) 100%);
  box-shadow: var(--shadow-soft);
}}
.details[hidden] {{
  display: none;
}}
.detail-main {{
  min-width: 0;
}}
.detail-name {{
  font-weight: 700;
  color: #f8fafc;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}}
.detail-path {{
  margin-top: 4px;
  color: var(--muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}}
.detail-stats {{
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}}
.pill {{
  padding: 5px 8px;
  border: 1px solid #384452;
  border-radius: 999px;
  background: color-mix(in srgb, var(--panel-2) 88%, black 12%);
  color: #dbeafe;
  font-size: 12px;
  white-space: nowrap;
}}
.tooltip {{
  position: fixed;
  max-width: min(420px, calc(100vw - 24px));
  z-index: 20;
  padding: 8px 10px;
  border: 1px solid #3b4654;
  border-radius: 7px;
  background: rgba(15, 23, 42, 0.94);
  color: #fff;
  font-size: 12px;
  line-height: 1.35;
  pointer-events: none;
  transform: translate(12px, 12px);
  display: none;
  box-shadow: var(--shadow);
}}
.tooltip strong {{
  display: block;
  margin-bottom: 3px;
  overflow-wrap: anywhere;
}}
.help-page {{
  position: fixed;
  inset: 0;
  z-index: 30;
  display: grid;
  place-items: center;
  padding: 18px;
  background: rgba(8, 12, 17, 0.76);
  backdrop-filter: blur(8px);
}}
.help-page[hidden] {{
  display: none;
}}
.help-dialog {{
  width: min(1040px, calc(100vw - 36px));
  max-height: min(760px, calc(100vh - 36px));
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  border: 1px solid var(--line);
  border-radius: 10px;
  background: linear-gradient(180deg, #222831 0%, #1b2027 100%);
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.48), var(--shadow-soft);
  overflow: hidden;
}}
.help-head {{
  min-height: 58px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 14px 12px 18px;
  border-bottom: 1px solid var(--line);
  background: #20262e;
}}
.help-title {{
  margin: 0;
  font-size: 18px;
  line-height: 1.25;
}}
.help-content {{
  min-height: 0;
  overflow: auto;
  padding: 18px;
}}
.help-grid {{
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}}
.help-section {{
  border: 1px solid var(--subtle-line);
  border-radius: 8px;
  background: rgba(20, 23, 28, 0.55);
  padding: 14px;
}}
.help-section h3 {{
  margin: 0 0 9px;
  color: #f8fafc;
  font-size: 14px;
}}
.help-section p, .help-section li {{
  color: #c9d3df;
  font-size: 13px;
  line-height: 1.48;
}}
.help-section p {{
  margin: 0;
}}
.help-section ul {{
  margin: 0;
  padding-left: 18px;
}}
.shortcut-list {{
  display: grid;
  gap: 7px;
}}
.shortcut-list div {{
  display: grid;
  grid-template-columns: 132px minmax(0, 1fr);
  align-items: baseline;
  gap: 10px;
  color: #c9d3df;
  font-size: 13px;
}}
.empty {{
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: var(--muted);
}}
.footer {{
  min-height: 30px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 6px 14px;
  border-top: 1px solid var(--line);
  background: #181d23;
}}
html[data-theme="light"] body {{
  background: linear-gradient(180deg, #f8fafc 0%, var(--bg) 48%, #e8edf3 100%);
}}
html[data-theme="light"] .theme-switch {{
  border-color: #cbd5e1;
  background: linear-gradient(180deg, #f8fafc 0%, #e8eef6 100%);
  color: #64748b;
  box-shadow:
    inset 0 1px 0 rgba(255,255,255,0.86),
    inset 0 -1px 0 rgba(15,23,42,0.05),
    0 1px 2px rgba(15,23,42,0.08);
}}
html[data-theme="light"] .theme-knob {{
  border-color: rgba(96, 165, 250, 0.38);
  background: linear-gradient(180deg, #ffffff 0%, #eef7ff 100%);
  box-shadow:
    0 2px 6px rgba(15, 23, 42, 0.16),
    inset 0 1px 0 rgba(255,255,255,0.98);
}}
html[data-theme="light"] .theme-input:checked + .theme-switch .theme-knob {{
  background: linear-gradient(180deg, #eff6ff 0%, #dbeafe 100%);
  border-color: rgba(14, 165, 233, 0.48);
}}
html[data-theme="light"] .theme-input:checked + .theme-switch .sun-icon {{
  color: #0284c7;
}}
html[data-theme="light"] .theme-toggle:hover .theme-switch {{
  border-color: #b6c5d8;
  background: linear-gradient(180deg, #ffffff 0%, #eaf2fb 100%);
}}
html[data-theme="light"] kbd {{
  border-color: #cbd5e1;
  border-bottom-color: #94a3b8;
  background: #f8fafc;
  color: #0f172a;
}}
html[data-theme="light"] * {{
  scrollbar-color: #94a3b8 #e2e8f0;
}}
html[data-theme="light"] *::-webkit-scrollbar-track {{
  background: #e2e8f0;
}}
html[data-theme="light"] *::-webkit-scrollbar-thumb {{
  background: #94a3b8;
  border-color: #e2e8f0;
}}
html[data-theme="light"] *::-webkit-scrollbar-thumb:hover {{
  background: #64748b;
}}
html[data-theme="light"] .toolbar,
html[data-theme="light"] .metric,
html[data-theme="light"] .details,
html[data-theme="light"] .top-files {{
  background: linear-gradient(180deg, #ffffff 0%, #f8fafc 100%);
}}
html[data-theme="light"] .sidebar {{
  background: linear-gradient(180deg, #ffffff 0%, #f1f5f9 100%);
}}
html[data-theme="light"] .icon-btn:hover,
html[data-theme="light"] .select:hover {{
  background: #eaf2fb;
}}
html[data-theme="light"] .tree-header,
html[data-theme="light"] .top-files-head,
html[data-theme="light"] .help-head {{
  background: #f1f5f9;
}}
html[data-theme="light"] .tree-parent-btn {{
  border-color: #cbd5e1;
  background: #f8fafc;
  color: #64748b;
}}
html[data-theme="light"] .tree-parent-btn:hover:not(:disabled) {{
  border-color: #93c5fd;
  color: #0f172a;
  background: #eaf2fb;
}}
html[data-theme="light"] .tree-columns-btn {{
  border-color: #cbd5e1;
  background: #f8fafc;
  color: #64748b;
}}
html[data-theme="light"] .tree-columns-btn:hover,
html[data-theme="light"] .tree-columns-btn[aria-expanded="true"] {{
  border-color: #93c5fd;
  color: #0f172a;
  background: #eaf2fb;
}}
html[data-theme="light"] .tree-columns-menu {{
  background: #ffffff;
  border-color: #cbd5e1;
}}
html[data-theme="light"] .row.file {{
  color: #334155;
}}
html[data-theme="light"] .row::before {{
  background: linear-gradient(180deg, var(--list-bar-top) 0%, var(--list-bar) 48%, var(--list-bar-bottom) 100%);
  box-shadow: inset -2px 0 0 var(--list-bar-edge);
}}
html[data-theme="light"] .row-kind {{
  border-color: #cbd5e1;
  background: #f8fafc;
  color: #475569;
}}
html[data-theme="light"] .row.dir .row-kind {{
  border-color: #5eead4;
  background: #ccfbf1;
  color: #0f766e;
}}
html[data-theme="light"] .value,
html[data-theme="light"] .detail-name,
html[data-theme="light"] .help-section h3 {{
  color: #0f172a;
}}
html[data-theme="light"] .treemap {{
  background: #e2e8f0;
}}
html[data-theme="light"] .treemap-head {{
  background: #ffffff;
}}
html[data-theme="light"] .tile {{
  border-color: rgba(15, 23, 42, 0.22);
}}
html[data-theme="light"] .tile.dir {{
  border-color: rgba(15, 23, 42, 0.30);
}}
html[data-theme="light"] .pill {{
  border-color: #cbd5e1;
  background: #f8fafc;
  color: #1e3a8a;
}}
html[data-theme="light"] .tooltip {{
  border-color: #cbd5e1;
  background: rgba(255, 255, 255, 0.96);
  color: #0f172a;
}}
html[data-theme="light"] .help-page {{
  background: rgba(226, 232, 240, 0.78);
}}
html[data-theme="light"] .help-dialog {{
  background: linear-gradient(180deg, #ffffff 0%, #f8fafc 100%);
}}
html[data-theme="light"] .help-section {{
  background: rgba(248, 250, 252, 0.82);
}}
html[data-theme="light"] .help-section p,
html[data-theme="light"] .help-section li,
html[data-theme="light"] .shortcut-list div {{
  color: #334155;
}}
html[data-theme="light"] .footer {{
  background: #f8fafc;
}}
html[data-theme="light"] .report-security.encrypted {{
  color: #0f766e;
  background: #ccfbf1;
}}
html[data-theme="light"] .report-security.plain {{
  color: #92400e;
  background: #fef3c7;
}}
@media (max-width: 820px) {{
  body {{ overflow: auto; }}
  .app {{ min-height: 100vh; height: auto; }}
  .toolbar {{
    grid-template-columns: 1fr;
  }}
  .tools {{
    justify-content: stretch;
  }}
  .main {{
    grid-template-columns: 1fr;
    grid-template-rows: minmax(260px, 38vh) minmax(360px, 62vh);
  }}
  .main-resizer {{
    display: none;
  }}
  .sidebar {{
    border-right: 0;
    border-bottom: 1px solid var(--line);
  }}
  .content {{
    min-height: 520px;
  }}
  .content.home {{
    --home-treemap-size: 34vh;
    grid-template-rows: minmax(210px, var(--home-treemap-size)) 12px minmax(320px, 1fr);
  }}
  .details {{
    grid-template-columns: 1fr;
  }}
  .detail-stats {{
    justify-content: flex-start;
  }}
  .help-grid {{
    grid-template-columns: 1fr;
  }}
  .shortcut-list div {{
    grid-template-columns: 1fr;
  }}
}}
</style>
</head>
<body>
<div class="app">
  <header class="toolbar">
    <nav id="crumbs" class="crumbs" aria-label="Path"></nav>
    <div class="tools">
      <label class="theme-toggle" title="Toggle light theme">
        <input id="themeToggle" class="theme-input" type="checkbox" role="switch" aria-label="Light theme">
        <span class="theme-switch" aria-hidden="true">
          <svg class="theme-icon moon-icon" viewBox="0 0 24 24"><path d="M20 14.7A7.5 7.5 0 0 1 9.3 4a8 8 0 1 0 10.7 10.7Z"/></svg>
          <svg class="theme-icon sun-icon" viewBox="0 0 24 24"><path d="M12 3v2"/><path d="M12 19v2"/><path d="m4.22 4.22 1.42 1.42"/><path d="m18.36 18.36 1.42 1.42"/><path d="M3 12h2"/><path d="M19 12h2"/><path d="m4.22 19.78 1.42-1.42"/><path d="m18.36 5.64 1.42-1.42"/><circle cx="12" cy="12" r="4"/></svg>
          <span class="theme-knob"></span>
        </span>
      </label>
      <button id="helpButton" class="icon-btn" title="Help (?)" aria-label="Help">
        <svg class="icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M9.5 9a2.8 2.8 0 0 1 5 1.7c0 2-2.5 2.2-2.5 4.3"/><path d="M12 18h.01"/></svg>
      </button>
    </div>
  </header>
  <main id="main" class="main">
    <aside id="sidebar" class="sidebar">
      <section class="summary">
        <div class="metric"><span class="label">Selected</span><span id="selectedSize" class="value"></span></div>
        <div class="metric"><span class="label">Items</span><span id="selectedItems" class="value"></span></div>
        <div class="metric"><span class="label">Files</span><span id="selectedFiles" class="value"></span></div>
      </section>
      <section id="tree" class="tree" aria-label="Directory listing"></section>
    </aside>
    <div id="mainResizer" class="main-resizer" role="separator" aria-orientation="vertical" aria-label="Resize right panel" tabindex="0"></div>
    <section id="content" class="content">
      <div id="treemapFrame" class="treemap-frame">
        <div class="treemap-head">
          <label class="treemap-cap-label" for="treemapTileCap">Max Tiles Depth</label>
          <select id="treemapTileCap" class="treemap-cap-select" aria-label="Maximum treemap tiles depth">
            <option value="5">5</option>
            <option value="10">10</option>
            <option value="20">20</option>
            <option value="50">50</option>
            <option value="100">100</option>
            <option value="500">500</option>
          </select>
        </div>
        <section id="treemap" class="treemap" aria-label="Treemap"></section>
      </div>
      <div id="homeResizer" class="home-resizer" role="separator" aria-orientation="horizontal" aria-label="Resize home panes" tabindex="0" hidden></div>
      <section id="topFiles" class="top-files" aria-label="Biggest files" hidden>
        <div class="top-files-head">
          <span id="topFilesTitle" class="top-file-title">List of biggest file</span>
          <div class="top-file-controls">
            <select id="topFilesLimit" class="top-file-limit" aria-label="Number of biggest files">
              <option value="10">10</option>
              <option value="20">20</option>
              <option value="30">30</option>
              <option value="40">40</option>
              <option value="50">50</option>
            </select>
          </div>
          <span class="top-file-size">Size</span>
        </div>
        <div id="topFilesBody" class="top-files-body"></div>
      </section>
      <section id="details" class="details">
        <div class="detail-main">
          <div id="detailName" class="detail-name"></div>
          <div id="detailPath" class="detail-path"></div>
        </div>
        <div id="detailStats" class="detail-stats"></div>
      </section>
    </section>
  </main>
  <footer class="footer">
    {footer_status}
    <span class="footer-meta">
      <span class="report-size" title="Final generated HTML file size">{REPORT_SIZE_PLACEHOLDER}</span>
      <time class="generated" datetime="{html.escape(generated_iso)}">Generated {html.escape(generated_display)}</time>
    </span>
  </footer>
</div>
<section id="helpPage" class="help-page" role="dialog" aria-modal="true" aria-labelledby="helpTitle" hidden>
  <div class="help-dialog">
    <header class="help-head">
      <h2 id="helpTitle" class="help-title">Help</h2>
      <button id="helpCloseButton" class="icon-btn" title="Close help" aria-label="Close help">
        <svg class="icon" viewBox="0 0 24 24"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>
      </button>
    </header>
    <div class="help-content">
      <div class="help-grid">
        <section class="help-section">
          <h3>What This Report Shows</h3>
          <p>webdiskstat turns a <code>gdu</code> or <code>ncdu</code> scan into a static disk usage report. The left panel lists entries in the current directory. The treemap shows the same directory visually by size, including nested contents inside larger directory tiles when space allows.</p>
        </section>
        <section class="help-section">
          <h3>Directory List</h3>
          <ul>
            <li>Double-click a directory to enter it.</li>
            <li>Use the column headers to sort by name, items, files, size, or modified date.</li>
            <li>Use the column settings button next to the Name header to show or hide optional columns.</li>
            <li>Modified time appears when it was included in the scan data.</li>
          </ul>
        </section>
        <section class="help-section">
          <h3>Treemap</h3>
          <ul>
            <li>Top-level rectangles are files or directories in the current directory.</li>
            <li>Larger directory tiles show subdirectories and files inside them when space allows.</li>
            <li>The treemap defaults to a maximum of 10 visible tiles, and the Max Tiles Depth menu can raise or lower that cap.</li>
            <li>Hover a tile for path and size. Double-click a directory tile to enter it.</li>
          </ul>
        </section>
        <section class="help-section">
          <h3>Home View</h3>
          <ul>
            <li>Choose 10 to 50 biggest files from the list menu. Results are paged 10 at a time.</li>
            <li>Drag the divider between the treemap and biggest-files list to resize the home panes.</li>
            <li>Double-click a listed file to jump to the directory containing it.</li>
          </ul>
        </section>
        <section class="help-section">
          <h3>Navigation</h3>
          <ul>
            <li>Use the breadcrumb path at the top to jump to a parent directory.</li>
            <li>Drag the divider between the directory list and right panel to resize the right panel.</li>
            <li>Use the parent button next to the Name column header to go up one directory.</li>
            <li>Use the home icon in the breadcrumb path to return to the scan root.</li>
          </ul>
        </section>
        <section class="help-section">
          <h3>Keyboard</h3>
          <div class="shortcut-list">
            <div><span><kbd>Up</kbd> / <kbd>Down</kbd></span><span>Move selection in the list.</span></div>
            <div><span><kbd>Page Up</kbd> / <kbd>Page Down</kbd></span><span>Move selection by one visible page.</span></div>
            <div><span><kbd>Home</kbd> / <kbd>End</kbd></span><span>Jump to the first or last item.</span></div>
            <div><span><kbd>n</kbd> / <kbd>s</kbd> / <kbd>C</kbd> / <kbd>M</kbd>/<kbd>m</kbd></span><span>Sort by name, size, file count, or modified time.</span></div>
            <div><span><kbd>Enter</kbd> / <kbd>Right</kbd></span><span>Enter the selected directory.</span></div>
            <div><span><kbd>Backspace</kbd> / <kbd>Left</kbd></span><span>Go up one directory.</span></div>
            <div><span><kbd>?</kbd></span><span>Open this help page.</span></div>
          </div>
        </section>
      </div>
    </div>
  </div>
</section>
<div id="tooltip" class="tooltip"></div>
<script>
const REPORT_DATA_PAYLOAD = {data};
let DATA = null;

function unpackReportData(payload) {{
  const strings = payload[0] || [];
  const packedRoot = payload[1];

  function valueAt(index) {{
    return index >= 0 ? strings[index] : "";
  }}

  function joinNodePath(parentPath, name) {{
    if (!parentPath) return name;
    if (parentPath === "/") return "/" + name.replace(/^\\/+/, "");
    return parentPath.replace(/\\/+$/, "") + "/" + name.replace(/^\\/+/, "");
  }}

  function decodeNode(packed, parentPath) {{
    const name = valueAt(packed[0]);
    const path = packed[1] >= 0 ? valueAt(packed[1]) : joinNodePath(parentPath, name);
    const type = packed[3] ? "dir" : "file";
    const node = {{
      name,
      path,
      size: packed[2] || 0,
      type,
      ext: valueAt(packed[4]) || (type === "dir" ? "" : "[no extension]"),
      children: []
    }};
    const mtime = valueAt(packed[5]);
    const mime = valueAt(packed[6]);
    const flag = valueAt(packed[7]);
    if (mtime) node.mtime = mtime;
    if (mime) node.mime = mime;
    if (flag) node.flag = flag;
    node.children = (Array.isArray(packed[8]) ? packed[8] : []).map(child => decodeNode(child, path));
    return node;
  }}

  return decodeNode(packedRoot, "");
}}

function bytesFromBase64(value) {{
  const binary = atob(value || "");
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}}

function utf8Bytes(value) {{
  return new TextEncoder().encode(value);
}}

async function loadCompressedReportData(bytes) {{
  if (typeof DecompressionStream !== "function") {{
    throw new Error("This browser cannot decompress embedded report data. Use a current Chrome, Edge, Firefox, or Safari release.");
  }}
  const stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream("gzip"));
  const text = await new Response(stream).text();
  return unpackReportData(JSON.parse(text));
}}

async function loadReportData(payload) {{
  const compressed = payload && payload.encrypted
    ? await decryptReportPayload(payload)
    : bytesFromBase64(payload && payload.payload);
  return loadCompressedReportData(compressed);
}}

async function decryptReportPayload(payload) {{
  if (payload.algorithm !== "ChaCha20-Poly1305") {{
    throw new Error(`Unsupported encrypted report algorithm: ${{payload.algorithm || "unknown"}}`);
  }}
  if (payload.kdf !== "PBKDF2-SHA256") {{
    throw new Error(`Unsupported encrypted report KDF: ${{payload.kdf || "unknown"}}`);
  }}
  const iterations = Number(payload.iterations);
  if (!Number.isInteger(iterations) || iterations < 1 || iterations > 5000000) {{
    throw new Error("Encrypted report KDF parameters are invalid.");
  }}

  let lastError = null;
  for (let attempt = 0; attempt < 3; attempt++) {{
    const promptText = attempt
      ? "Incorrect password. Try again:"
      : "Enter password to open this encrypted report:";
    const password = window.prompt(promptText);
    if (password === null) throw new Error("Password required to open encrypted report.");

    try {{
      const key = await deriveReportKey(password, bytesFromBase64(payload.salt), iterations);
      return chacha20Poly1305Decrypt(
        key,
        bytesFromBase64(payload.nonce),
        bytesFromBase64(payload.payload),
        bytesFromBase64(payload.tag),
        utf8Bytes(payload.aad || "webdiskstat-report-data-v1")
      );
    }} catch (error) {{
      lastError = error;
    }}
  }}

  throw new Error(lastError && lastError.message ? "Unable to decrypt report data. Check the password." : "Unable to decrypt report data.");
}}

async function deriveReportKey(password, salt, iterations) {{
  const passwordBytes = utf8Bytes(password);
  if (globalThis.crypto && crypto.subtle) {{
    const keyMaterial = await crypto.subtle.importKey(
      "raw",
      passwordBytes,
      "PBKDF2",
      false,
      ["deriveBits"]
    );
    const bits = await crypto.subtle.deriveBits(
      {{
        name: "PBKDF2",
        hash: "SHA-256",
        salt,
        iterations
      }},
      keyMaterial,
      256
    );
    return new Uint8Array(bits);
  }}

  return pbkdf2Sha256(passwordBytes, salt, iterations, 32);
}}

function pbkdf2Sha256(password, salt, iterations, length) {{
  const hashLength = 32;
  const blockCount = Math.ceil(length / hashLength);
  const output = new Uint8Array(blockCount * hashLength);

  for (let block = 1; block <= blockCount; block++) {{
    const saltBlock = new Uint8Array(salt.length + 4);
    saltBlock.set(salt);
    saltBlock[salt.length] = (block >>> 24) & 255;
    saltBlock[salt.length + 1] = (block >>> 16) & 255;
    saltBlock[salt.length + 2] = (block >>> 8) & 255;
    saltBlock[salt.length + 3] = block & 255;

    let u = hmacSha256(password, saltBlock);
    const t = new Uint8Array(u);
    for (let iteration = 1; iteration < iterations; iteration++) {{
      u = hmacSha256(password, u);
      for (let index = 0; index < hashLength; index++) t[index] ^= u[index];
    }}
    output.set(t, (block - 1) * hashLength);
  }}

  return output.slice(0, length);
}}

function hmacSha256(key, message) {{
  let normalizedKey = key;
  if (normalizedKey.length > 64) normalizedKey = sha256(normalizedKey);

  const inner = new Uint8Array(64 + message.length);
  const outer = new Uint8Array(96);
  for (let index = 0; index < 64; index++) {{
    const value = index < normalizedKey.length ? normalizedKey[index] : 0;
    inner[index] = value ^ 0x36;
    outer[index] = value ^ 0x5c;
  }}
  inner.set(message, 64);
  outer.set(sha256(inner), 64);
  return sha256(outer);
}}

const SHA256_K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5,
  0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
  0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
  0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
  0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5,
  0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
]);

function sha256(message) {{
  const bitLength = BigInt(message.length) * 8n;
  const paddedLength = Math.ceil((message.length + 9) / 64) * 64;
  const padded = new Uint8Array(paddedLength);
  padded.set(message);
  padded[message.length] = 0x80;
  for (let index = 0; index < 8; index++) {{
    padded[paddedLength - 1 - index] = Number((bitLength >> BigInt(index * 8)) & 255n);
  }}

  let h0 = 0x6a09e667;
  let h1 = 0xbb67ae85;
  let h2 = 0x3c6ef372;
  let h3 = 0xa54ff53a;
  let h4 = 0x510e527f;
  let h5 = 0x9b05688c;
  let h6 = 0x1f83d9ab;
  let h7 = 0x5be0cd19;
  const words = new Uint32Array(64);

  for (let offset = 0; offset < padded.length; offset += 64) {{
    for (let index = 0; index < 16; index++) {{
      words[index] = readU32BE(padded, offset + index * 4);
    }}
    for (let index = 16; index < 64; index++) {{
      const s0 = rotateRight(words[index - 15], 7) ^ rotateRight(words[index - 15], 18) ^ (words[index - 15] >>> 3);
      const s1 = rotateRight(words[index - 2], 17) ^ rotateRight(words[index - 2], 19) ^ (words[index - 2] >>> 10);
      words[index] = (words[index - 16] + s0 + words[index - 7] + s1) >>> 0;
    }}

    let a = h0;
    let b = h1;
    let c = h2;
    let d = h3;
    let e = h4;
    let f = h5;
    let g = h6;
    let h = h7;

    for (let index = 0; index < 64; index++) {{
      const s1 = rotateRight(e, 6) ^ rotateRight(e, 11) ^ rotateRight(e, 25);
      const ch = (e & f) ^ (~e & g);
      const temp1 = (h + s1 + ch + SHA256_K[index] + words[index]) >>> 0;
      const s0 = rotateRight(a, 2) ^ rotateRight(a, 13) ^ rotateRight(a, 22);
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const temp2 = (s0 + maj) >>> 0;
      h = g;
      g = f;
      f = e;
      e = (d + temp1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + temp2) >>> 0;
    }}

    h0 = (h0 + a) >>> 0;
    h1 = (h1 + b) >>> 0;
    h2 = (h2 + c) >>> 0;
    h3 = (h3 + d) >>> 0;
    h4 = (h4 + e) >>> 0;
    h5 = (h5 + f) >>> 0;
    h6 = (h6 + g) >>> 0;
    h7 = (h7 + h) >>> 0;
  }}

  const digest = new Uint8Array(32);
  [h0, h1, h2, h3, h4, h5, h6, h7].forEach((value, index) => writeU32BE(digest, index * 4, value));
  return digest;
}}

function rotateRight(value, bits) {{
  return ((value >>> bits) | (value << (32 - bits))) >>> 0;
}}

function readU32BE(bytes, offset) {{
  return ((bytes[offset] << 24) | (bytes[offset + 1] << 16) | (bytes[offset + 2] << 8) | bytes[offset + 3]) >>> 0;
}}

function writeU32BE(bytes, offset, value) {{
  bytes[offset] = (value >>> 24) & 255;
  bytes[offset + 1] = (value >>> 16) & 255;
  bytes[offset + 2] = (value >>> 8) & 255;
  bytes[offset + 3] = value & 255;
}}

function chacha20Poly1305Decrypt(key, nonce, ciphertext, tag, aad) {{
  if (key.length !== 32 || nonce.length !== 12 || tag.length !== 16) {{
    throw new Error("Encrypted report payload is malformed.");
  }}
  const polyKey = chacha20Block(key, nonce, 0).slice(0, 32);
  const expectedTag = poly1305Mac(poly1305AeadData(aad, ciphertext), polyKey);
  if (!timingSafeEqual(tag, expectedTag)) {{
    throw new Error("Decryption failed.");
  }}
  return chacha20Xor(key, nonce, 1, ciphertext);
}}

function chacha20Xor(key, nonce, counter, input) {{
  const output = new Uint8Array(input.length);
  for (let offset = 0; offset < input.length; offset += 64) {{
    const block = chacha20Block(key, nonce, counter);
    const length = Math.min(64, input.length - offset);
    for (let index = 0; index < length; index++) {{
      output[offset + index] = input[offset + index] ^ block[index];
    }}
    counter = (counter + 1) >>> 0;
    if (counter === 0 && offset + 64 < input.length) {{
      throw new Error("Encrypted report payload is too large.");
    }}
  }}
  return output;
}}

function chacha20Block(key, nonce, counter) {{
  const state = new Uint32Array(16);
  state[0] = 0x61707865;
  state[1] = 0x3320646e;
  state[2] = 0x79622d32;
  state[3] = 0x6b206574;
  for (let index = 0; index < 8; index++) state[4 + index] = readU32(key, index * 4);
  state[12] = counter >>> 0;
  state[13] = readU32(nonce, 0);
  state[14] = readU32(nonce, 4);
  state[15] = readU32(nonce, 8);

  const working = new Uint32Array(state);
  for (let round = 0; round < 10; round++) {{
    quarterRound(working, 0, 4, 8, 12);
    quarterRound(working, 1, 5, 9, 13);
    quarterRound(working, 2, 6, 10, 14);
    quarterRound(working, 3, 7, 11, 15);
    quarterRound(working, 0, 5, 10, 15);
    quarterRound(working, 1, 6, 11, 12);
    quarterRound(working, 2, 7, 8, 13);
    quarterRound(working, 3, 4, 9, 14);
  }}

  const output = new Uint8Array(64);
  for (let index = 0; index < 16; index++) {{
    writeU32(output, index * 4, (working[index] + state[index]) >>> 0);
  }}
  return output;
}}

function quarterRound(state, a, b, c, d) {{
  state[a] = (state[a] + state[b]) >>> 0;
  state[d] = rotateLeft(state[d] ^ state[a], 16);
  state[c] = (state[c] + state[d]) >>> 0;
  state[b] = rotateLeft(state[b] ^ state[c], 12);
  state[a] = (state[a] + state[b]) >>> 0;
  state[d] = rotateLeft(state[d] ^ state[a], 8);
  state[c] = (state[c] + state[d]) >>> 0;
  state[b] = rotateLeft(state[b] ^ state[c], 7);
}}

function rotateLeft(value, bits) {{
  return ((value << bits) | (value >>> (32 - bits))) >>> 0;
}}

function readU32(bytes, offset) {{
  return (bytes[offset] | (bytes[offset + 1] << 8) | (bytes[offset + 2] << 16) | (bytes[offset + 3] << 24)) >>> 0;
}}

function writeU32(bytes, offset, value) {{
  bytes[offset] = value & 255;
  bytes[offset + 1] = (value >>> 8) & 255;
  bytes[offset + 2] = (value >>> 16) & 255;
  bytes[offset + 3] = (value >>> 24) & 255;
}}

function poly1305AeadData(aad, ciphertext) {{
  const aadPad = (16 - aad.length % 16) % 16;
  const ciphertextPad = (16 - ciphertext.length % 16) % 16;
  const data = new Uint8Array(aad.length + aadPad + ciphertext.length + ciphertextPad + 16);
  let offset = 0;
  data.set(aad, offset);
  offset += aad.length + aadPad;
  data.set(ciphertext, offset);
  offset += ciphertext.length + ciphertextPad;
  writeU64(data, offset, aad.length);
  writeU64(data, offset + 8, ciphertext.length);
  return data;
}}

function poly1305Mac(message, key) {{
  if (key.length !== 32) throw new Error("Poly1305 key is malformed.");
  const rBytes = key.slice(0, 16);
  rBytes[3] &= 15;
  rBytes[7] &= 15;
  rBytes[11] &= 15;
  rBytes[15] &= 15;
  rBytes[4] &= 252;
  rBytes[8] &= 252;
  rBytes[12] &= 252;

  const r = littleEndianToBigInt(rBytes);
  const s = littleEndianToBigInt(key.slice(16, 32));
  const modulus = (1n << 130n) - 5n;
  let accumulator = 0n;

  for (let offset = 0; offset < message.length; offset += 16) {{
    const block = message.slice(offset, Math.min(offset + 16, message.length));
    const n = littleEndianToBigInt(block) + (1n << BigInt(block.length * 8));
    accumulator = ((accumulator + n) * r) % modulus;
  }}

  return bigIntTo16Bytes((accumulator + s) & ((1n << 128n) - 1n));
}}

function littleEndianToBigInt(bytes) {{
  let value = 0n;
  for (let index = bytes.length - 1; index >= 0; index--) {{
    value = (value << 8n) + BigInt(bytes[index]);
  }}
  return value;
}}

function bigIntTo16Bytes(value) {{
  const bytes = new Uint8Array(16);
  for (let index = 0; index < 16; index++) {{
    bytes[index] = Number((value >> BigInt(index * 8)) & 255n);
  }}
  return bytes;
}}

function writeU64(bytes, offset, value) {{
  let remaining = BigInt(value);
  for (let index = 0; index < 8; index++) {{
    bytes[offset + index] = Number(remaining & 255n);
    remaining >>= 8n;
  }}
}}

function timingSafeEqual(left, right) {{
  if (left.length !== right.length) return false;
  let diff = 0;
  for (let index = 0; index < left.length; index++) diff |= left[index] ^ right[index];
  return diff === 0;
}}

const palette = [
  "#2563eb", "#0f766e", "#c2410c", "#7c3aed", "#be123c", "#047857",
  "#b45309", "#0369a1", "#a21caf", "#4d7c0f", "#b91c1c", "#1d4ed8",
  "#0e7490", "#9333ea", "#ca8a04", "#15803d", "#db2777", "#4338ca"
];
const DEFAULT_TREEMAP_TILE_CAP = 10;
const TREEMAP_TILE_CAP_OPTIONS = [5, 10, 20, 50, 100, 500];
const TREEMAP_MAX_DEPTH = 8;
const TREEMAP_CHILD_INSET = 4;
const TREEMAP_CHILD_LABEL_HEIGHT = 24;
const TREEMAP_MIN_NESTED_WIDTH = 92;
const TREEMAP_MIN_NESTED_HEIGHT = 68;
const TREE_ROW_HEIGHT = 36;
const TREE_OVERSCAN_ROWS = 8;
const TOP_FILES_LIMITS = [10, 20, 30, 40, 50];
const DEFAULT_TREE_COLUMNS = Object.freeze({{
  items: true,
  files: true,
  size: true,
  modified: true,
  percent: true
}});
const TREE_COLUMNS = [
  {{ key: "name", label: "Name", menuLabel: "Name", grid: "minmax(150px, 1fr)", minWidth: 260, required: true, sortKey: "name" }},
  {{ key: "items", label: "Items", menuLabel: "Items", grid: "52px", minWidth: 52, numeric: true, sortKey: "items" }},
  {{ key: "files", label: "Files", menuLabel: "Files", grid: "52px", minWidth: 52, numeric: true, sortKey: "files" }},
  {{ key: "size", label: "Size", menuLabel: "Size", grid: "82px", minWidth: 82, numeric: true, sortKey: "size" }},
  {{ key: "modified", label: "Modified", menuLabel: "Modified", grid: "126px", minWidth: 126, sortKey: "modified" }},
  {{ key: "percent", label: "%", menuLabel: "Percent", grid: "46px", minWidth: 46, numeric: true }}
];
const SORT_SHORTCUTS = new Map([
  ["n", "name"],
  ["s", "size"],
  ["C", "files"],
  ["M", "modified"],
  ["m", "modified"]
]);
const TREE_COLUMN_STORAGE_KEY = "webdiskstat-tree-columns";
const THEME_STORAGE_KEY = "webdiskstat-theme";
const TREEMAP_TILE_CAP_STORAGE_KEY = "webdiskstat-treemap-tile-cap";
const MAIN_PANE_STORAGE_KEY = "webdiskstat-sidebar-size";
const MAIN_MIN_SIDEBAR_SIZE = 280;
const MAIN_MIN_CONTENT_SIZE = 360;
const MAIN_RESIZER_SIZE = 10;
const MAIN_RESIZE_STEP = 32;
const HOME_TREEMAP_STORAGE_KEY = "webdiskstat-home-treemap-size";
const HOME_MIN_TREEMAP_SIZE = 180;
const HOME_MIN_TOP_FILES_SIZE = 260;
const HOME_RESIZER_SIZE = 12;
const HOME_RESIZE_STEP = 32;

const state = {{
  current: null,
  selected: null,
  sortKey: "size",
  sortDir: "desc",
  topFilesLimit: 10,
  treemapTileCap: DEFAULT_TREEMAP_TILE_CAP,
  visibleColumns: {{ ...DEFAULT_TREE_COLUMNS }}
}};

const byId = new Map();
const byPath = new Map();
const parent = new Map();
let nextNodeId = 0;
const treeView = {{
  node: null,
  children: [],
  total: 0,
  body: null,
  start: -1,
  end: -1
}};

const el = {{
  crumbs: document.getElementById("crumbs"),
  themeToggle: document.getElementById("themeToggle"),
  helpButton: document.getElementById("helpButton"),
  helpPage: document.getElementById("helpPage"),
  helpCloseButton: document.getElementById("helpCloseButton"),
  selectedSize: document.getElementById("selectedSize"),
  selectedItems: document.getElementById("selectedItems"),
  selectedFiles: document.getElementById("selectedFiles"),
  main: document.getElementById("main"),
  sidebar: document.getElementById("sidebar"),
  mainResizer: document.getElementById("mainResizer"),
  tree: document.getElementById("tree"),
  content: document.getElementById("content"),
  treemapFrame: document.getElementById("treemapFrame"),
  treemapTileCap: document.getElementById("treemapTileCap"),
  treemap: document.getElementById("treemap"),
  homeResizer: document.getElementById("homeResizer"),
  topFiles: document.getElementById("topFiles"),
  topFilesTitle: document.getElementById("topFilesTitle"),
  topFilesLimit: document.getElementById("topFilesLimit"),
  topFilesBody: document.getElementById("topFilesBody"),
  details: document.getElementById("details"),
  detailName: document.getElementById("detailName"),
  detailPath: document.getElementById("detailPath"),
  detailStats: document.getElementById("detailStats"),
  tooltip: document.getElementById("tooltip")
}};

function walk(node, parentNode, depth = 0) {{
  node.id = nextNodeId++;
  node.depth = depth;
  byId.set(node.id, node);
  byPath.set(node.path || node.name, node);
  parent.set(node.id, parentNode);
  let total = 1;
  let files = node.type === "dir" ? 0 : 1;
  if (node.children) {{
    node.children.forEach(child => {{
      const counts = walk(child, node, depth + 1);
      total += counts.total;
      files += counts.files;
    }});
  }}
  node.items = total - 1;
  node.files = files;
  return {{ total, files }};
}}

function formatBytes(bytes) {{
  if (!bytes) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {{
    value /= 1024;
    unit++;
  }}
  const digits = value >= 100 || unit === 0 ? 0 : value >= 10 ? 1 : 2;
  return `${{value.toFixed(digits)}} ${{units[unit]}}`;
}}

function formatCount(value) {{
  return new Intl.NumberFormat().format(value || 0);
}}

function dateFromModifiedValue(value) {{
  if (value === undefined || value === null || value === "") return "";
  const text = String(value).trim();
  let date;
  if (/^-?\\d+(\\.\\d+)?$/.test(text)) {{
    const numeric = Number(text);
    const millis = Math.abs(numeric) < 100000000000 ? numeric * 1000 : numeric;
    date = new Date(millis);
  }} else {{
    date = new Date(text);
  }}
  return !date || Number.isNaN(date.getTime()) ? "" : date;
}}

function formatModifiedTime(value) {{
  const date = dateFromModifiedValue(value);
  if (!date) return value === undefined || value === null || value === "" ? "" : String(value);
  return new Intl.DateTimeFormat(undefined, {{
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit"
  }}).format(date);
}}

function formatListModifiedTime(value) {{
  const date = dateFromModifiedValue(value);
  if (!date) return value === undefined || value === null || value === "" ? "-" : String(value);
  return new Intl.DateTimeFormat(undefined, {{
    year: "2-digit",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  }}).format(date);
}}

function modifiedSortValue(node) {{
  const date = dateFromModifiedValue(node.mtime);
  return date ? date.getTime() : null;
}}

function pct(part, total) {{
  if (!total) return "0%";
  const value = part / total * 100;
  return value >= 10 ? `${{value.toFixed(1)}}%` : `${{value.toFixed(2)}}%`;
}}

function hashString(value) {{
  let hash = 0;
  for (let i = 0; i < value.length; i++) hash = ((hash << 5) - hash + value.charCodeAt(i)) | 0;
  return Math.abs(hash);
}}

function colorFor(node) {{
  const key = node.type === "dir" ? "directory" : (node.ext || "[no extension]");
  const hash = hashString(key);
  return palette[Math.abs(hash) % palette.length];
}}

function treemapColorFor(node) {{
  if (node.type !== "dir") return colorFor(node);
  const hash = hashString(pathForNode(node) || node.name || String(node.id));
  const hue = 176 + (hash % 26);
  const saturation = 52 + (Math.floor(hash / 29) % 18);
  const lightness = 28 + (Math.floor(hash / 521) % 24);
  return `hsl(${{hue}}, ${{saturation}}%, ${{lightness}}%)`;
}}

function pathForNode(node) {{
  return node.path || node.name || "";
}}

function hashForNode(node) {{
  return node === DATA ? "" : `#path=${{encodeURIComponent(pathForNode(node))}}`;
}}

function currentUrlWithoutHash() {{
  if (!window.location || !window.location.href) return "";
  const hashIndex = window.location.href.indexOf("#");
  return hashIndex >= 0 ? window.location.href.slice(0, hashIndex) : window.location.href;
}}

function nodeFromLocationHash() {{
  if (!window.location || !window.location.hash) return DATA;
  const hash = window.location.hash.slice(1);
  if (!hash) return DATA;

  let path = "";
  if (hash.startsWith("path=")) {{
    path = hash.slice(5);
  }} else {{
    path = hash;
  }}

  try {{
    path = decodeURIComponent(path);
  }} catch (error) {{
    return null;
  }}

  const node = byPath.get(path);
  return node && node.type === "dir" ? node : null;
}}

function syncUrlToCurrent(node, replace = false) {{
  if (!window.location) return;
  const base = currentUrlWithoutHash();
  if (!base) return;

  const nextUrl = base + hashForNode(node);
  if (window.location.href === nextUrl) return;

  const method = replace ? "replaceState" : "pushState";
  if (window.history && typeof window.history[method] === "function") {{
    window.history[method](null, "", nextUrl);
  }} else {{
    window.location.hash = hashForNode(node);
  }}
}}

function applyLocationHash() {{
  if (!DATA) return;
  const node = nodeFromLocationHash();
  if (!node || node === state.current) return;
  setCurrent(node, false);
}}

function setCurrent(node, updateUrl = true) {{
  if (!node) return;
  state.current = node;
  state.selected = node;
  if (updateUrl) syncUrlToCurrent(node);
  renderSafely();
}}

function goParent() {{
  if (!state.current) return;
  const parentNode = parent.get(state.current.id);
  if (parentNode) setCurrent(parentNode);
}}

function directoryForNode(node) {{
  let cursor = node && node.type === "dir" ? node : parent.get(node.id);
  while (cursor && cursor.type !== "dir") {{
    cursor = parent.get(cursor.id);
  }}
  return cursor || DATA;
}}

function setSelected(node) {{
  if (!node) return;
  state.selected = node;
  renderDetails();
  document.querySelectorAll(".row.active, .tile.active, .top-file-row.active").forEach(item => item.classList.remove("active"));
  document.querySelectorAll(`[data-id="${{node.id}}"]`).forEach(item => item.classList.add("active"));
}}

function ensureListSelection(children) {{
  if (!children.length) {{
    state.selected = state.current;
    return;
  }}
  if (!state.selected || state.selected === state.current || !children.some(child => child.id === state.selected.id)) {{
    state.selected = children[0];
  }}
}}

function pathToRoot(node) {{
  const items = [];
  let cursor = node;
  while (cursor) {{
    items.unshift(cursor);
    cursor = parent.get(cursor.id);
  }}
  return items;
}}

function makeHomeIcon() {{
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "icon");
  svg.setAttribute("viewBox", "0 0 24 24");
  [
    "M3 10.5 12 3l9 7.5",
    "M5 10v10h14V10",
    "M9 20v-6h6v6"
  ].forEach(d => {{
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", d);
    svg.appendChild(path);
  }});
  return svg;
}}

function makeUpIcon() {{
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "icon");
  svg.setAttribute("viewBox", "0 0 24 24");
  [
    "M12 19V5",
    "m5 12 7-7 7 7"
  ].forEach(d => {{
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", d);
    svg.appendChild(path);
  }});
  return svg;
}}

function makeColumnsIcon() {{
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "icon");
  svg.setAttribute("viewBox", "0 0 24 24");
  [
    "M4 5h16",
    "M4 12h16",
    "M4 19h16",
    "M8 5v14",
    "M16 5v14"
  ].forEach(d => {{
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", d);
    svg.appendChild(path);
  }});
  return svg;
}}

function renderCrumbs() {{
  el.crumbs.textContent = "";
  pathToRoot(state.current).forEach((node, index, nodes) => {{
    const button = document.createElement("button");
    button.className = index === 0 ? "crumb root" : "crumb";
    if (index === 0) {{
      button.appendChild(makeHomeIcon());
    }} else {{
      button.textContent = node.name;
    }}
    button.title = node.path || node.name;
    button.setAttribute("aria-label", index === 0 ? "Root" : node.name);
    button.addEventListener("click", () => setCurrent(node));
    el.crumbs.appendChild(button);
    if (index < nodes.length - 1) {{
      const sep = document.createElement("span");
      sep.className = "sep";
      sep.textContent = "/";
      el.crumbs.appendChild(sep);
    }}
  }});
}}

function filteredChildren(node) {{
  return (node.children || []).slice();
}}

function sortedChildren(node) {{
  const children = filteredChildren(node);
  const direction = state.sortDir === "asc" ? 1 : -1;
  children.sort((a, b) => {{
    let result = 0;
    if (state.sortKey === "name") {{
      result = a.name.localeCompare(b.name, undefined, {{ numeric: true, sensitivity: "base" }});
    }} else if (state.sortKey === "items") {{
      result = (a.items || 0) - (b.items || 0);
    }} else if (state.sortKey === "files") {{
      result = (a.files || 0) - (b.files || 0);
    }} else if (state.sortKey === "modified") {{
      const aTime = modifiedSortValue(a);
      const bTime = modifiedSortValue(b);
      if (aTime === null && bTime === null) {{
        result = 0;
      }} else if (aTime === null) {{
        return 1;
      }} else if (bTime === null) {{
        return -1;
      }} else {{
        result = aTime - bTime;
      }}
    }} else {{
      result = (a.size || 0) - (b.size || 0);
    }}
    if (result !== 0) return result * direction;
    result = (b.size || 0) - (a.size || 0);
    if (result !== 0) return result;
    return a.name.localeCompare(b.name, undefined, {{ numeric: true, sensitivity: "base" }});
  }});
  return children;
}}

function sortIndicator(key) {{
  if (state.sortKey !== key) return "";
  return state.sortDir === "asc" ? " ↑" : " ↓";
}}

function setSort(key) {{
  if (state.sortKey === key) {{
    state.sortDir = state.sortDir === "asc" ? "desc" : "asc";
  }} else {{
    state.sortKey = key;
    state.sortDir = key === "name" ? "asc" : "desc";
  }}
  renderTree();
  setSelected(state.selected);
}}

function handleSortShortcut(event) {{
  if (event.altKey || event.ctrlKey || event.metaKey) return false;
  const key = SORT_SHORTCUTS.get(event.key);
  if (!key) return false;
  event.preventDefault();
  setSort(key);
  return true;
}}

function currentTreeChildren() {{
  if (treeView.node === state.current) return treeView.children;
  return sortedChildren(state.current);
}}

function defaultTreeColumns() {{
  return {{ ...DEFAULT_TREE_COLUMNS }};
}}

function readStoredTreeColumns() {{
  const columns = defaultTreeColumns();
  try {{
    const stored = JSON.parse(localStorage.getItem(TREE_COLUMN_STORAGE_KEY) || "{{}}");
    Object.keys(columns).forEach(key => {{
      if (typeof stored[key] === "boolean") columns[key] = stored[key];
    }});
  }} catch (error) {{
    // Column settings are optional; the report still works when storage is unavailable.
  }}
  return columns;
}}

function storeTreeColumns() {{
  try {{
    localStorage.setItem(TREE_COLUMN_STORAGE_KEY, JSON.stringify(state.visibleColumns));
  }} catch (error) {{
    // Ignore storage failures in strict file contexts.
  }}
}}

function isTreeColumnVisible(key) {{
  if (key === "name") return true;
  return state.visibleColumns[key] !== false;
}}

function visibleTreeColumns() {{
  return TREE_COLUMNS.filter(column => isTreeColumnVisible(column.key));
}}

function applyTreeColumnLayout() {{
  const columns = visibleTreeColumns();
  const template = columns.map(column => column.grid).join(" ");
  const minWidth = columns.reduce((total, column) => total + column.minWidth, 0) +
    Math.max(0, columns.length - 1) * 8 +
    22;
  el.tree.style.setProperty("--tree-columns", template);
  el.tree.style.setProperty("--tree-min-width", `${{Math.max(300, minWidth)}}px`);
}}

function closeTreeColumnsMenu(focusButton = false) {{
  const menu = el.tree.querySelector(".tree-columns-menu");
  const button = el.tree.querySelector(".tree-columns-btn");
  if (!menu || menu.hidden) return false;
  menu.hidden = true;
  if (button) {{
    button.setAttribute("aria-expanded", "false");
    if (focusButton) button.focus();
  }}
  return true;
}}

function reopenTreeColumnsMenu() {{
  const menu = el.tree.querySelector(".tree-columns-menu");
  const button = el.tree.querySelector(".tree-columns-btn");
  if (!menu || !button) return;
  menu.hidden = false;
  button.setAttribute("aria-expanded", "true");
}}

function setTreeColumnVisible(key, visible) {{
  state.visibleColumns[key] = visible;
  storeTreeColumns();
  renderTree();
  setSelected(state.selected);
  reopenTreeColumnsMenu();
}}

function makeTreeColumnsButton(menu) {{
  const button = document.createElement("button");
  button.className = "tree-columns-btn";
  button.type = "button";
  button.title = "Column settings";
  button.setAttribute("aria-label", "Column settings");
  button.setAttribute("aria-haspopup", "true");
  button.setAttribute("aria-expanded", "false");
  button.appendChild(makeColumnsIcon());
  button.addEventListener("click", event => {{
    event.stopPropagation();
    const open = menu.hidden;
    closeTreeColumnsMenu();
    menu.hidden = !open;
    button.setAttribute("aria-expanded", String(open));
  }});
  return button;
}}

function makeTreeColumnsMenu() {{
  const menu = document.createElement("div");
  menu.className = "tree-columns-menu";
  menu.hidden = true;
  menu.addEventListener("click", event => event.stopPropagation());

  const title = document.createElement("div");
  title.className = "tree-columns-title";
  title.textContent = "Columns";
  menu.appendChild(title);

  TREE_COLUMNS.filter(column => !column.required).forEach(column => {{
    const label = document.createElement("label");
    label.className = "tree-column-option";

    const input = document.createElement("input");
    input.type = "checkbox";
    input.checked = isTreeColumnVisible(column.key);
    input.addEventListener("change", () => setTreeColumnVisible(column.key, input.checked));

    const text = document.createElement("span");
    text.textContent = column.menuLabel;
    label.append(input, text);
    menu.appendChild(label);
  }});
  return menu;
}}

function makeHeaderButton(label, key, numeric = key !== "name" && key !== "modified") {{
  const button = document.createElement("button");
  button.className = "tree-sort";
  if (numeric) button.classList.add("numeric");
  if (state.sortKey === key) button.classList.add("sort-active");
  button.type = "button";
  button.textContent = label + sortIndicator(key);
  button.title = `Sort by ${{label.toLowerCase()}}`;
  button.addEventListener("click", () => setSort(key));
  return button;
}}

function makeParentHeaderButton() {{
  const button = document.createElement("button");
  const parentNode = parent.get(state.current.id);
  button.className = "tree-parent-btn";
  button.type = "button";
  button.title = parentNode ? "Parent" : "Already at root";
  button.setAttribute("aria-label", "Parent");
  button.disabled = !parentNode;
  button.appendChild(makeUpIcon());
  button.addEventListener("click", goParent);
  return button;
}}

function makeHeaderLabel(label, numeric = false) {{
  const span = document.createElement("span");
  span.className = numeric ? "tree-label numeric" : "tree-label";
  span.textContent = label;
  return span;
}}

function renderTreeHeader() {{
  const header = document.createElement("div");
  header.className = "tree-header";
  const columnsMenu = makeTreeColumnsMenu();
  const nameHead = document.createElement("div");
  nameHead.className = "tree-name-head";
  nameHead.append(makeParentHeaderButton(), makeHeaderButton("Name", "name"), makeTreeColumnsButton(columnsMenu));

  const cells = visibleTreeColumns().map(column => {{
    if (column.key === "name") return nameHead;
    if (column.sortKey) return makeHeaderButton(column.label, column.sortKey, column.numeric);
    return makeHeaderLabel(column.label, column.numeric);
  }});
  header.append(...cells, columnsMenu);
  el.tree.appendChild(header);
}}

function resetTreeView() {{
  treeView.node = null;
  treeView.children = [];
  treeView.total = 0;
  treeView.body = null;
  treeView.start = -1;
  treeView.end = -1;
}}

function createTreeRow(child, total) {{
  const row = document.createElement("div");
  row.className = `row ${{child.type}}`;
  if (state.selected && child.id === state.selected.id) row.classList.add("active");
  row.dataset.id = child.id;
  row.style.setProperty("--bar", `${{Math.max(2, child.size / Math.max(total, 1) * 100)}}%`);
  row.style.setProperty("--row-color", colorFor(child));
  row.title = child.path || child.name;
  row.addEventListener("click", () => setSelected(child));
  row.addEventListener("dblclick", () => {{
    if (child.type === "dir") setCurrent(child);
  }});

  const name = document.createElement("div");
  name.className = "row-name";
  const swatch = document.createElement("span");
  swatch.className = "swatch";
  const kind = document.createElement("span");
  kind.className = "row-kind";
  kind.textContent = child.type === "dir" ? "DIR" : "FILE";
  const label = document.createElement("span");
  label.textContent = child.name;
  name.append(swatch, kind, label);

  const size = document.createElement("div");
  size.className = "row-size";
  size.textContent = formatBytes(child.size);

  const items = document.createElement("div");
  items.className = "row-count";
  items.textContent = formatCount(child.items);

  const files = document.createElement("div");
  files.className = "row-count";
  files.textContent = formatCount(child.files);

  const modified = document.createElement("div");
  modified.className = "row-modified";
  modified.textContent = formatListModifiedTime(child.mtime);
  modified.title = formatModifiedTime(child.mtime) || "Modified time unavailable";

  const percent = document.createElement("div");
  percent.className = "row-pct";
  percent.textContent = pct(child.size, total);

  const cells = [name];
  if (isTreeColumnVisible("items")) cells.push(items);
  if (isTreeColumnVisible("files")) cells.push(files);
  if (isTreeColumnVisible("size")) cells.push(size);
  if (isTreeColumnVisible("modified")) cells.push(modified);
  if (isTreeColumnVisible("percent")) cells.push(percent);
  row.append(...cells);
  return row;
}}

function renderVisibleTreeRows(force = false) {{
  if (!treeView.body) return;
  const header = el.tree.querySelector(".tree-header");
  const headerHeight = header ? header.offsetHeight : 0;
  const bodyScrollTop = Math.max(0, el.tree.scrollTop - headerHeight);
  const viewportHeight = Math.max(0, el.tree.clientHeight - headerHeight);
  const start = Math.max(0, Math.floor(bodyScrollTop / TREE_ROW_HEIGHT) - TREE_OVERSCAN_ROWS);
  const end = Math.min(
    treeView.children.length,
    Math.ceil((bodyScrollTop + viewportHeight) / TREE_ROW_HEIGHT) + TREE_OVERSCAN_ROWS
  );
  if (!force && start === treeView.start && end === treeView.end) return;

  treeView.start = start;
  treeView.end = end;
  treeView.body.textContent = "";
  const fragment = document.createDocumentFragment();
  for (let index = start; index < end; index++) {{
    const row = createTreeRow(treeView.children[index], treeView.total);
    row.style.transform = `translateY(${{index * TREE_ROW_HEIGHT}}px)`;
    fragment.appendChild(row);
  }}
  treeView.body.appendChild(fragment);
}}

function renderTree() {{
  el.tree.textContent = "";
  resetTreeView();
  applyTreeColumnLayout();
  renderTreeHeader();
  const children = sortedChildren(state.current);
  ensureListSelection(children);
  const total = state.current.size || children.reduce((sum, child) => sum + child.size, 0);
  if (!children.length) {{
    const empty = document.createElement("div");
    empty.className = "row";
    empty.textContent = "No entries";
    el.tree.appendChild(empty);
    return;
  }}

  const body = document.createElement("div");
  body.className = "tree-body";
  body.style.height = `${{children.length * TREE_ROW_HEIGHT}}px`;
  el.tree.appendChild(body);
  treeView.node = state.current;
  treeView.children = children;
  treeView.total = total;
  treeView.body = body;
  renderVisibleTreeRows(true);
}}

function treemapItems(node, maxItems = DEFAULT_TREEMAP_TILE_CAP) {{
  const tileLimit = Math.max(0, Math.floor(maxItems));
  if (tileLimit <= 0) return [];
  const entries = (node.children || [])
    .filter(child => child.size > 0)
    .sort((a, b) => b.size - a.size);
  if (!entries.length && node.type !== "dir" && node.size > 0) return [node];
  if (entries.length <= tileLimit) return entries;

  const visibleLimit = Math.max(0, tileLimit - 1);
  const visible = entries.slice(0, visibleLimit);
  const hidden = entries.slice(visibleLimit);
  const hiddenSize = hidden.reduce((sum, item) => sum + item.size, 0);
  if (hiddenSize > 0) {{
    visible.push({{
      id: `other-${{node.id}}`,
      name: `${{formatCount(hidden.length)}} smaller entries`,
      path: `${{node.path || node.name}} / smaller entries`,
      size: hiddenSize,
      type: "file",
      ext: "[other]",
      children: [],
      items: hidden.reduce((sum, item) => sum + (item.items || 0) + 1, 0),
      files: hidden.reduce((sum, item) => sum + (item.files || (item.type === "file" ? 1 : 0)), 0),
      depth: (node.depth || 0) + 1
    }});
  }}
  return visible;
}}

function hasTreemapChildren(node) {{
  return node.type === "dir" && (node.children || []).some(child => child.size > 0);
}}

function nestedTreemapBounds(node, rect, depth) {{
  if (!hasTreemapChildren(node) || depth >= TREEMAP_MAX_DEPTH) return null;
  if (rect.w < TREEMAP_MIN_NESTED_WIDTH || rect.h < TREEMAP_MIN_NESTED_HEIGHT) return null;

  const labelHeight = rect.w > 56 && rect.h > 32 ? TREEMAP_CHILD_LABEL_HEIGHT : TREEMAP_CHILD_INSET;
  const x = TREEMAP_CHILD_INSET;
  const y = labelHeight;
  const w = rect.w - TREEMAP_CHILD_INSET * 2;
  const h = rect.h - y - TREEMAP_CHILD_INSET;
  if (w < 24 || h < 24) return null;
  return {{ x, y, w, h }};
}}

function layoutTreemap(items, x, y, w, h) {{
  const total = items.reduce((sum, item) => sum + item.size, 0);
  if (!total || !items.length || w <= 0 || h <= 0) return [];

  const rects = [];
  let row = [];
  let rowSize = 0;
  let remaining = items.slice();
  let offsetX = x;
  let offsetY = y;
  let width = w;
  let height = h;

  while (remaining.length) {{
    const item = remaining[0];
    const nextRow = row.concat(item);
    const nextSize = rowSize + item.size;
    const side = Math.min(width, height);
    if (!row.length || worst(nextRow, nextSize, side) <= worst(row, rowSize, side)) {{
      row = nextRow;
      rowSize = nextSize;
      remaining.shift();
    }} else {{
      placeRow(row, rowSize);
      row = [];
      rowSize = 0;
    }}
  }}
  if (row.length) placeRow(row, rowSize);
  return rects;

  function worst(rowItems, size, side) {{
    if (!size || !side || !rowItems.length) return Infinity;
    let max = 0;
    let min = Infinity;
    rowItems.forEach(item => {{
      const area = item.size / total * w * h;
      if (area > max) max = area;
      if (area < min) min = area;
    }});
    if (!min || !Number.isFinite(min)) return Infinity;
    const side2 = side * side;
    const rowArea = sizeArea(size);
    return Math.max(side2 * max / (rowArea * rowArea), rowArea * rowArea / (side2 * min));
  }}

  function sizeArea(size) {{
    return size / total * w * h;
  }}

  function placeRow(rowItems, size) {{
    const area = sizeArea(size);
    if (width >= height) {{
      const rowHeight = area / width;
      let cx = offsetX;
      rowItems.forEach(item => {{
        const itemWidth = sizeArea(item.size) / rowHeight;
        rects.push({{ node: item, x: cx, y: offsetY, w: itemWidth, h: rowHeight }});
        cx += itemWidth;
      }});
      offsetY += rowHeight;
      height -= rowHeight;
    }} else {{
      const rowWidth = area / height;
      let cy = offsetY;
      rowItems.forEach(item => {{
        const itemHeight = sizeArea(item.size) / rowWidth;
        rects.push({{ node: item, x: offsetX, y: cy, w: rowWidth, h: itemHeight }});
        cy += itemHeight;
      }});
      offsetX += rowWidth;
      width -= rowWidth;
    }}
  }}
}}

function renderTreemapTile(container, rect, depth, budget, maxTiles, reserveTiles = 0) {{
  const budgetLimit = Math.max(0, maxTiles - reserveTiles);
  if (budget.count >= budgetLimit || rect.w < 1 || rect.h < 1) return;

  const node = rect.node;
  const childBounds = nestedTreemapBounds(node, rect, depth);
  const childBudget = budgetLimit - budget.count - 1;
  const childItems = childBounds && childBudget > 0 ? treemapItems(node, childBudget) : [];
  const childRects = childItems.length
    ? layoutTreemap(childItems, 0, 0, childBounds.w, childBounds.h)
    : [];
  const hasNestedTiles = childRects.length > 0;
  const tile = document.createElement("div");
  tile.className = `tile ${{node.type}}${{depth > 0 ? " nested" : ""}}${{hasNestedTiles ? " has-children" : ""}}`;
  tile.dataset.id = node.id;
  tile.style.left = `${{rect.x}}px`;
  tile.style.top = `${{rect.y}}px`;
  tile.style.width = `${{Math.max(0, rect.w)}}px`;
  tile.style.height = `${{Math.max(0, rect.h)}}px`;
  tile.style.setProperty("--tile-color", treemapColorFor(node));
  tile.title = "";
  tile.addEventListener("click", event => {{
    event.stopPropagation();
    setSelected(node);
  }});
  tile.addEventListener("dblclick", event => {{
    event.stopPropagation();
    if (node.type === "dir") setCurrent(node);
  }});
  tile.addEventListener("mousemove", event => {{
    event.stopPropagation();
    showTooltip(event, node);
  }});
  tile.addEventListener("mouseleave", hideTooltip);
  if (node.type === "dir" && rect.w > 52 && rect.h > 28) {{
    const kind = document.createElement("div");
    kind.className = "tile-kind";
    kind.textContent = "DIR";
    tile.appendChild(kind);
  }}
  if (rect.w > 56 && rect.h > 32) {{
    const label = document.createElement("div");
    label.className = "tile-label";
    label.textContent = node.name;
    tile.appendChild(label);
  }}

  budget.count += 1;
  if (hasNestedTiles) {{
    const childLayer = document.createElement("div");
    childLayer.className = "tile-children";
    childLayer.style.left = `${{childBounds.x}}px`;
    childLayer.style.top = `${{childBounds.y}}px`;
    childLayer.style.width = `${{childBounds.w}}px`;
    childLayer.style.height = `${{childBounds.h}}px`;
    childRects.forEach(childRect => renderTreemapTile(childLayer, childRect, depth + 1, budget, maxTiles, reserveTiles));
    if (childLayer.childElementCount) tile.appendChild(childLayer);
  }}

  container.appendChild(tile);
}}

function renderTreemap() {{
  el.treemap.textContent = "";
  const bounds = el.treemap.getBoundingClientRect();
  state.treemapTileCap = normalizeTreemapTileCap(state.treemapTileCap);
  el.treemapTileCap.value = String(state.treemapTileCap);
  el.treemap.setAttribute("aria-label", `Treemap, showing up to ${{state.treemapTileCap}} tiles`);
  const items = treemapItems(state.current, state.treemapTileCap);
  const rects = layoutTreemap(items, 0, 0, bounds.width, bounds.height);
  if (!rects.length) {{
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "Empty";
    el.treemap.appendChild(empty);
    return;
  }}

  const budget = {{ count: 0 }};
  rects.forEach((rect, index) => {{
    renderTreemapTile(el.treemap, rect, 0, budget, state.treemapTileCap, rects.length - index - 1);
  }});
}}

function collectFiles(node, files) {{
  if (node.type !== "dir") {{
    if (node.size > 0) files.push(node);
    return;
  }}
  (node.children || []).forEach(child => collectFiles(child, files));
}}

function normalizeTopFilesLimit(value) {{
  const numeric = Number(value);
  return TOP_FILES_LIMITS.includes(numeric) ? numeric : TOP_FILES_LIMITS[0];
}}

function normalizeTreemapTileCap(value) {{
  const numeric = Number(value);
  return TREEMAP_TILE_CAP_OPTIONS.includes(numeric) ? numeric : DEFAULT_TREEMAP_TILE_CAP;
}}

function readStoredTreemapTileCap() {{
  try {{
    return normalizeTreemapTileCap(localStorage.getItem(TREEMAP_TILE_CAP_STORAGE_KEY));
  }} catch (error) {{
    return DEFAULT_TREEMAP_TILE_CAP;
  }}
}}

function storeTreemapTileCap(value) {{
  try {{
    localStorage.setItem(TREEMAP_TILE_CAP_STORAGE_KEY, String(value));
  }} catch (error) {{
    // The report still works when localStorage is unavailable.
  }}
}}

function isMainResizerVisible() {{
  return getComputedStyle(el.mainResizer).display !== "none";
}}

function readStoredMainSidebarSize() {{
  try {{
    const value = Number(localStorage.getItem(MAIN_PANE_STORAGE_KEY));
    return Number.isFinite(value) && value > 0 ? value : 0;
  }} catch (error) {{
    return 0;
  }}
}}

function storeMainSidebarSize(size) {{
  try {{
    localStorage.setItem(MAIN_PANE_STORAGE_KEY, String(Math.round(size)));
  }} catch (error) {{
    // The report still works when localStorage is unavailable.
  }}
}}

function mainSidebarMaxSize() {{
  const width = el.main.getBoundingClientRect().width;
  return Math.max(MAIN_MIN_SIDEBAR_SIZE, width - MAIN_MIN_CONTENT_SIZE - MAIN_RESIZER_SIZE);
}}

function clampMainSidebarSize(size) {{
  return Math.max(MAIN_MIN_SIDEBAR_SIZE, Math.min(mainSidebarMaxSize(), size || MAIN_MIN_SIDEBAR_SIZE));
}}

function currentMainSidebarSize() {{
  const inlineSize = parseFloat(el.main.style.getPropertyValue("--sidebar-size"));
  if (Number.isFinite(inlineSize)) return inlineSize;

  const sidebarRect = el.sidebar.getBoundingClientRect();
  return sidebarRect.width || MAIN_MIN_SIDEBAR_SIZE;
}}

function updateMainResizerAttributes(size = currentMainSidebarSize()) {{
  const clamped = clampMainSidebarSize(size);
  el.mainResizer.setAttribute("aria-valuemin", String(MAIN_MIN_SIDEBAR_SIZE));
  el.mainResizer.setAttribute("aria-valuemax", String(Math.round(mainSidebarMaxSize())));
  el.mainResizer.setAttribute("aria-valuenow", String(Math.round(clamped)));
}}

function setMainSidebarSize(size, persist = true, rerender = true) {{
  if (!isMainResizerVisible()) return;
  const clamped = clampMainSidebarSize(size);
  el.main.style.setProperty("--sidebar-size", `${{Math.round(clamped)}}px`);
  updateMainResizerAttributes(clamped);
  if (persist) storeMainSidebarSize(clamped);
  if (rerender) renderTreemap();
}}

function syncMainPaneSize() {{
  if (!isMainResizerVisible()) return;
  const currentInlineSize = el.main.style.getPropertyValue("--sidebar-size");
  if (!currentInlineSize) {{
    const stored = readStoredMainSidebarSize();
    if (stored) {{
      setMainSidebarSize(stored, false, false);
      return;
    }}
  }} else {{
    setMainSidebarSize(currentMainSidebarSize(), false, false);
    return;
  }}
  updateMainResizerAttributes();
}}

function resizeMainPaneAt(clientX) {{
  const rect = el.main.getBoundingClientRect();
  setMainSidebarSize(clientX - rect.left);
}}

function beginMainResize(event) {{
  if (event.button !== undefined && event.button !== 0) return;
  event.preventDefault();
  hideTooltip();
  el.mainResizer.classList.add("dragging");
  document.body.classList.add("resizing-main-pane");
  resizeMainPaneAt(event.clientX);
  window.addEventListener("pointermove", handleMainResizeMove);
  window.addEventListener("pointerup", endMainResize);
  window.addEventListener("pointercancel", endMainResize);
}}

function handleMainResizeMove(event) {{
  event.preventDefault();
  resizeMainPaneAt(event.clientX);
}}

function endMainResize() {{
  window.removeEventListener("pointermove", handleMainResizeMove);
  window.removeEventListener("pointerup", endMainResize);
  window.removeEventListener("pointercancel", endMainResize);
  el.mainResizer.classList.remove("dragging");
  document.body.classList.remove("resizing-main-pane");
}}

function handleMainResizerKey(event) {{
  let handled = true;
  if (event.key === "ArrowLeft") {{
    setMainSidebarSize(currentMainSidebarSize() - MAIN_RESIZE_STEP);
  }} else if (event.key === "ArrowRight") {{
    setMainSidebarSize(currentMainSidebarSize() + MAIN_RESIZE_STEP);
  }} else if (event.key === "PageUp") {{
    setMainSidebarSize(currentMainSidebarSize() - MAIN_RESIZE_STEP * 3);
  }} else if (event.key === "PageDown") {{
    setMainSidebarSize(currentMainSidebarSize() + MAIN_RESIZE_STEP * 3);
  }} else if (event.key === "Home") {{
    setMainSidebarSize(MAIN_MIN_SIDEBAR_SIZE);
  }} else if (event.key === "End") {{
    setMainSidebarSize(mainSidebarMaxSize());
  }} else {{
    handled = false;
  }}

  if (handled) event.preventDefault();
}}

function readStoredHomeTreemapSize() {{
  try {{
    const value = Number(localStorage.getItem(HOME_TREEMAP_STORAGE_KEY));
    return Number.isFinite(value) && value > 0 ? value : 0;
  }} catch (error) {{
    return 0;
  }}
}}

function storeHomeTreemapSize(size) {{
  try {{
    localStorage.setItem(HOME_TREEMAP_STORAGE_KEY, String(Math.round(size)));
  }} catch (error) {{
    // The report still works when localStorage is unavailable.
  }}
}}

function homeTreemapMaxSize() {{
  const height = el.content.getBoundingClientRect().height;
  return Math.max(HOME_MIN_TREEMAP_SIZE, height - HOME_MIN_TOP_FILES_SIZE - HOME_RESIZER_SIZE);
}}

function clampHomeTreemapSize(size) {{
  return Math.max(HOME_MIN_TREEMAP_SIZE, Math.min(homeTreemapMaxSize(), size || HOME_MIN_TREEMAP_SIZE));
}}

function currentHomeTreemapSize() {{
  const inlineSize = parseFloat(el.content.style.getPropertyValue("--home-treemap-size"));
  if (Number.isFinite(inlineSize)) return inlineSize;

  const contentRect = el.content.getBoundingClientRect();
  const frameRect = el.treemapFrame.getBoundingClientRect();
  if (frameRect.height > 0) {{
    const frameStyle = getComputedStyle(el.treemapFrame);
    const marginBottom = parseFloat(frameStyle.marginBottom) || 0;
    return frameRect.bottom - contentRect.top + marginBottom;
  }}
  return HOME_MIN_TREEMAP_SIZE;
}}

function updateHomeResizerAttributes(size = currentHomeTreemapSize()) {{
  const clamped = clampHomeTreemapSize(size);
  el.homeResizer.setAttribute("aria-valuemin", String(HOME_MIN_TREEMAP_SIZE));
  el.homeResizer.setAttribute("aria-valuemax", String(Math.round(homeTreemapMaxSize())));
  el.homeResizer.setAttribute("aria-valuenow", String(Math.round(clamped)));
}}

function setHomeTreemapSize(size, persist = true, rerender = true) {{
  const clamped = clampHomeTreemapSize(size);
  el.content.style.setProperty("--home-treemap-size", `${{Math.round(clamped)}}px`);
  updateHomeResizerAttributes(clamped);
  if (persist) storeHomeTreemapSize(clamped);
  if (rerender) renderTreemap();
}}

function syncHomePaneSize() {{
  if (state.current !== DATA) return;
  const currentInlineSize = el.content.style.getPropertyValue("--home-treemap-size");
  if (!currentInlineSize) {{
    const stored = readStoredHomeTreemapSize();
    if (stored) {{
      setHomeTreemapSize(stored, false, false);
      return;
    }}
  }} else {{
    setHomeTreemapSize(currentHomeTreemapSize(), false, false);
    return;
  }}
  updateHomeResizerAttributes();
}}

function resizeHomePaneAt(clientY) {{
  const rect = el.content.getBoundingClientRect();
  setHomeTreemapSize(clientY - rect.top);
}}

function beginHomeResize(event) {{
  if (event.button !== undefined && event.button !== 0) return;
  event.preventDefault();
  hideTooltip();
  el.homeResizer.classList.add("dragging");
  document.body.classList.add("resizing-home-pane");
  resizeHomePaneAt(event.clientY);
  window.addEventListener("pointermove", handleHomeResizeMove);
  window.addEventListener("pointerup", endHomeResize);
  window.addEventListener("pointercancel", endHomeResize);
}}

function handleHomeResizeMove(event) {{
  event.preventDefault();
  resizeHomePaneAt(event.clientY);
}}

function endHomeResize() {{
  window.removeEventListener("pointermove", handleHomeResizeMove);
  window.removeEventListener("pointerup", endHomeResize);
  window.removeEventListener("pointercancel", endHomeResize);
  el.homeResizer.classList.remove("dragging");
  document.body.classList.remove("resizing-home-pane");
}}

function handleHomeResizerKey(event) {{
  let handled = true;
  if (event.key === "ArrowUp") {{
    setHomeTreemapSize(currentHomeTreemapSize() - HOME_RESIZE_STEP);
  }} else if (event.key === "ArrowDown") {{
    setHomeTreemapSize(currentHomeTreemapSize() + HOME_RESIZE_STEP);
  }} else if (event.key === "PageUp") {{
    setHomeTreemapSize(currentHomeTreemapSize() - HOME_RESIZE_STEP * 3);
  }} else if (event.key === "PageDown") {{
    setHomeTreemapSize(currentHomeTreemapSize() + HOME_RESIZE_STEP * 3);
  }} else if (event.key === "Home") {{
    setHomeTreemapSize(HOME_MIN_TREEMAP_SIZE);
  }} else if (event.key === "End") {{
    setHomeTreemapSize(homeTreemapMaxSize());
  }} else {{
    handled = false;
  }}

  if (handled) event.preventDefault();
}}

function renderHomePanel() {{
  const isHome = state.current === DATA;
  el.content.classList.toggle("home", isHome);
  el.homeResizer.hidden = !isHome;
  el.topFiles.hidden = !isHome;
  el.details.hidden = isHome;
  if (!isHome) {{
    el.topFilesBody.textContent = "";
    return;
  }}
  syncHomePaneSize();

  const files = [];
  collectFiles(DATA, files);
  files.sort((a, b) => b.size - a.size);
  state.topFilesLimit = normalizeTopFilesLimit(state.topFilesLimit);
  el.topFilesLimit.value = String(state.topFilesLimit);
  el.topFilesTitle.textContent = "List of biggest file";
  el.topFiles.setAttribute("aria-label", `List of biggest file, showing ${{state.topFilesLimit}} entries`);

  el.topFilesBody.textContent = "";
  el.topFilesBody.scrollTop = 0;
  if (!files.length) {{
    const empty = document.createElement("div");
    empty.className = "top-file-row";
    empty.textContent = "No files";
    el.topFilesBody.appendChild(empty);
    return;
  }}

  const topFiles = files.slice(0, state.topFilesLimit);
  topFiles.forEach(file => {{
    const row = document.createElement("div");
    row.className = "top-file-row";
    row.dataset.id = file.id;
    row.title = file.path || file.name;
    row.addEventListener("click", () => setSelected(file));
    row.addEventListener("dblclick", () => setCurrent(directoryForNode(file)));

    const main = document.createElement("div");
    main.className = "top-file-main";

    const name = document.createElement("div");
    name.className = "top-file-name";
    name.textContent = file.name;

    const path = document.createElement("div");
    path.className = "top-file-path";
    path.textContent = file.path || file.name;

    const size = document.createElement("div");
    size.className = "top-file-size";
    size.textContent = formatBytes(file.size);

    main.append(name, path);
    row.append(main, size);
    el.topFilesBody.appendChild(row);
  }});
}}

function renderDetails() {{
  const node = state.selected || state.current;
  el.selectedSize.textContent = formatBytes(state.current.size);
  el.selectedItems.textContent = formatCount(state.current.items);
  el.selectedFiles.textContent = formatCount(state.current.files);
  el.detailName.textContent = node.name;
  el.detailPath.textContent = node.path || node.name;
  el.detailStats.textContent = "";
  const stats = [
    formatBytes(node.size),
    pct(node.size, DATA.size),
    node.type,
    node.ext || "directory",
    `${{formatCount(node.items)}} items`,
    `${{formatCount(node.files)}} files`
  ];
  const modified = formatModifiedTime(node.mtime);
  if (modified) stats.push(`Modified ${{modified}}`);
  stats.forEach(value => {{
    const pill = document.createElement("span");
    pill.className = "pill";
    pill.textContent = value;
    el.detailStats.appendChild(pill);
  }});
}}

function showTooltip(event, node) {{
  el.tooltip.innerHTML = `<strong>${{escapeHtml(node.name)}}</strong>${{escapeHtml(formatBytes(node.size))}} · ${{escapeHtml(pct(node.size, DATA.size))}}<br>${{escapeHtml(node.path || node.name)}}`;
  el.tooltip.style.left = `${{event.clientX}}px`;
  el.tooltip.style.top = `${{event.clientY}}px`;
  el.tooltip.style.display = "block";
}}

function hideTooltip() {{
  el.tooltip.style.display = "none";
}}

function setTheme(theme, persist = true) {{
  const normalized = theme === "light" ? "light" : "dark";
  document.documentElement.dataset.theme = normalized;
  el.themeToggle.checked = normalized === "light";
  el.themeToggle.setAttribute("aria-checked", String(el.themeToggle.checked));
  if (!persist) return;
  try {{
    localStorage.setItem(THEME_STORAGE_KEY, normalized);
  }} catch (error) {{
    // The report still works when localStorage is unavailable, such as in strict file contexts.
  }}
}}

function openHelpPage() {{
  hideTooltip();
  el.helpPage.hidden = false;
  el.helpCloseButton.focus();
}}

function closeHelpPage(focusHelpButton = true) {{
  el.helpPage.hidden = true;
  if (focusHelpButton) el.helpButton.focus();
}}

function escapeHtml(value) {{
  return String(value).replace(/[&<>"']/g, char => ({{
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#039;"
  }}[char]));
}}

function render() {{
  renderCrumbs();
  renderTree();
  renderHomePanel();
  renderTreemap();
  renderDetails();
  setSelected(state.selected);
}}

function renderSafely() {{
  try {{
    render();
  }} catch (error) {{
    showRenderError(error);
  }}
}}

function showRenderError(error) {{
  console.error(error);
  el.tree.textContent = "";
  const row = document.createElement("div");
  row.className = "row";
  row.textContent = "Unable to render this directory";
  el.tree.appendChild(row);

  el.treemap.textContent = "";
  const empty = document.createElement("div");
  empty.className = "empty";
  empty.textContent = "Unable to render this directory";
  el.treemap.appendChild(empty);

  el.detailName.textContent = state.current.name || "Render error";
  el.detailPath.textContent = error && error.message ? error.message : String(error);
  el.detailStats.textContent = "";
}}

function prepareReportData(root) {{
  byId.clear();
  byPath.clear();
  parent.clear();
  nextNodeId = 0;
  DATA = root;
  walk(DATA, null);
  state.current = DATA;
  state.selected = DATA;
}}

function showLoadError(error) {{
  console.error(error);
  el.tree.textContent = "";
  const row = document.createElement("div");
  row.className = "row";
  row.textContent = "Unable to load report data";
  el.tree.appendChild(row);

  el.treemap.textContent = "";
  const empty = document.createElement("div");
  empty.className = "empty";
  empty.textContent = "Unable to load report data";
  el.treemap.appendChild(empty);

  el.content.classList.remove("home");
  el.homeResizer.hidden = true;
  el.topFiles.hidden = true;
  el.detailName.textContent = "Unable to load report";
  el.detailPath.textContent = error && error.message ? error.message : String(error);
  el.detailStats.textContent = "";
}}

async function initReport() {{
  setTheme(document.documentElement.dataset.theme, false);
  state.treemapTileCap = readStoredTreemapTileCap();
  el.treemapTileCap.value = String(state.treemapTileCap);
  state.visibleColumns = readStoredTreeColumns();
  applyTreeColumnLayout();
  syncMainPaneSize();

  try {{
    const root = await loadReportData(REPORT_DATA_PAYLOAD);
    prepareReportData(root);
    const initialNode = nodeFromLocationHash();
    if (initialNode) {{
      state.current = initialNode;
      state.selected = initialNode;
      syncUrlToCurrent(initialNode, true);
    }}
    renderSafely();
  }} catch (error) {{
    showLoadError(error);
  }}
}}

el.themeToggle.addEventListener("change", event => {{
  setTheme(event.target.checked ? "light" : "dark");
}});
el.helpButton.addEventListener("click", openHelpPage);
el.helpCloseButton.addEventListener("click", () => closeHelpPage());
el.helpPage.addEventListener("click", event => {{
  if (event.target === el.helpPage) closeHelpPage();
}});
el.mainResizer.addEventListener("pointerdown", beginMainResize);
el.mainResizer.addEventListener("keydown", handleMainResizerKey);
el.homeResizer.addEventListener("pointerdown", beginHomeResize);
el.homeResizer.addEventListener("keydown", handleHomeResizerKey);
el.topFilesLimit.addEventListener("change", event => {{
  state.topFilesLimit = normalizeTopFilesLimit(event.target.value);
  renderHomePanel();
  setSelected(state.selected);
}});
el.treemapTileCap.addEventListener("change", event => {{
  state.treemapTileCap = normalizeTreemapTileCap(event.target.value);
  el.treemapTileCap.value = String(state.treemapTileCap);
  storeTreemapTileCap(state.treemapTileCap);
  renderTreemap();
  setSelected(state.selected);
}});
el.tree.addEventListener("scroll", () => renderVisibleTreeRows(), {{ passive: true }});
document.addEventListener("click", event => {{
  if (
    event.target &&
    typeof event.target.closest === "function" &&
    (event.target.closest(".tree-columns-menu") || event.target.closest(".tree-columns-btn"))
  ) return;
  closeTreeColumnsMenu();
}});
el.sidebar.addEventListener("wheel", event => {{
  if (event.target && event.target.closest(".tree")) return;
  if (!event.deltaY && !event.deltaX) return;
  el.tree.scrollTop += event.deltaY;
  el.tree.scrollLeft += event.deltaX;
  event.preventDefault();
}}, {{ passive: false }});
window.addEventListener("resize", () => {{
  syncMainPaneSize();
  if (!DATA) return;
  if (state.current === DATA) syncHomePaneSize();
  renderVisibleTreeRows();
  renderTreemap();
}});
window.addEventListener("popstate", applyLocationHash);
window.addEventListener("hashchange", applyLocationHash);

function isTextEditingTarget(target) {{
  if (!target) return false;
  const tagName = target.tagName;
  return target.isContentEditable ||
    tagName === "INPUT" ||
    tagName === "TEXTAREA" ||
    tagName === "SELECT";
}}

function scrollTreeSelectionIntoView(node) {{
  if (!node) return;
  const row = document.querySelector(`.tree .row[data-id="${{node.id}}"]`);
  if (row && typeof row.scrollIntoView === "function") {{
    row.scrollIntoView({{ block: "nearest" }});
    return;
  }}

  const children = currentTreeChildren();
  const index = children.findIndex(child => child.id === node.id);
  if (index < 0) return;

  const header = el.tree.querySelector(".tree-header");
  const headerHeight = header ? header.offsetHeight : 0;
  const rowTop = headerHeight + index * TREE_ROW_HEIGHT;
  const rowBottom = rowTop + TREE_ROW_HEIGHT;
  const visibleTop = el.tree.scrollTop + headerHeight;
  const visibleBottom = el.tree.scrollTop + el.tree.clientHeight;
  if (rowTop < visibleTop) {{
    el.tree.scrollTop = Math.max(0, rowTop - headerHeight);
  }} else if (rowBottom > visibleBottom) {{
    el.tree.scrollTop = rowBottom - el.tree.clientHeight;
  }}
  renderVisibleTreeRows();
}}

function setListSelectionByIndex(index) {{
  if (!state.current) return;
  const children = currentTreeChildren();
  if (!children.length) return;
  const clamped = Math.max(0, Math.min(children.length - 1, index));
  const node = children[clamped];
  setSelected(node);
  scrollTreeSelectionIntoView(node);
}}

function moveListSelection(delta) {{
  if (!state.current) return;
  const children = currentTreeChildren();
  if (!children.length) return;
  let index = children.findIndex(child => state.selected && child.id === state.selected.id);
  if (index < 0) {{
    index = delta > 0 ? 0 : children.length - 1;
  }} else {{
    index += delta;
  }}
  setListSelectionByIndex(index);
}}

function treePageRowCount() {{
  const header = el.tree.querySelector(".tree-header");
  const headerHeight = header ? header.offsetHeight : 0;
  const availableHeight = Math.max(TREE_ROW_HEIGHT, el.tree.clientHeight - headerHeight);
  return Math.max(1, Math.floor(availableHeight / TREE_ROW_HEIGHT));
}}

function openSelectedDirectory() {{
  if (state.selected && state.selected.type === "dir") {{
    setCurrent(state.selected);
  }}
}}

function handleListKey(event) {{
  if (handleSortShortcut(event)) return true;
  if (event.key === "ArrowDown") {{
    event.preventDefault();
    moveListSelection(1);
    return true;
  }}
  if (event.key === "ArrowUp") {{
    event.preventDefault();
    moveListSelection(-1);
    return true;
  }}
  if (event.key === "PageDown") {{
    event.preventDefault();
    moveListSelection(treePageRowCount());
    return true;
  }}
  if (event.key === "PageUp") {{
    event.preventDefault();
    moveListSelection(-treePageRowCount());
    return true;
  }}
  if (event.key === "Home") {{
    event.preventDefault();
    setListSelectionByIndex(0);
    return true;
  }}
  if (event.key === "End") {{
    event.preventDefault();
    setListSelectionByIndex(currentTreeChildren().length - 1);
    return true;
  }}
  if (event.key === "Enter" || event.key === "ArrowRight") {{
    event.preventDefault();
    openSelectedDirectory();
    return true;
  }}
  return false;
}}

document.addEventListener("keydown", event => {{
  if (event.defaultPrevented) return;
  if (event.key === "Escape" && !el.helpPage.hidden) {{
    event.preventDefault();
    closeHelpPage();
    return;
  }}
  if (event.key === "Escape" && closeTreeColumnsMenu(true)) {{
    event.preventDefault();
    return;
  }}
  if (!el.helpPage.hidden) return;
  if (isTextEditingTarget(event.target)) return;
  if (event.key === "?" && !event.ctrlKey && !event.altKey && !event.metaKey) {{
    event.preventDefault();
    openHelpPage();
    return;
  }}
  if (!DATA) return;
  if (event.key === "Backspace" || event.key === "ArrowLeft") {{
    event.preventDefault();
    goParent();
    return;
  }}
  handleListKey(event);
}});

initReport();
</script>
</body>
</html>
"""
    return fill_report_size(optimize_report_html(report))


if __name__ == "__main__":
    raise SystemExit(main())
