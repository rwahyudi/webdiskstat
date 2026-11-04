// Decode the compact GDD v2 diff stream directly into the report's established
// JSON-shaped model. Kept standalone so the same implementation is exercised
// by the Go tests and embedded in generated reports.
function decodeDiffBinary(input) {
  const bytes = input instanceof Uint8Array ? input : new Uint8Array(input);
  let pos = 0;
  const fail = message => { throw new Error("invalid diff binary: " + message); };
  const readUvarint = (exact = true) => {
    let value = 0;
    let factor = 1;
    for (let i = 0; i < 10; i++) {
      if (pos >= bytes.length) fail("truncated varint");
      const byte = bytes[pos++];
      const part = byte & 0x7f;
      if (i === 9 && (part > 1 || (byte & 0x80))) fail("varint exceeds uint64 range");
      if (exact && part && factor > Number.MAX_SAFE_INTEGER / part) fail("varint exceeds JavaScript's safe integer range");
      value += part * factor;
      if (!Number.isFinite(value) || (exact && !Number.isSafeInteger(value))) fail("varint exceeds JavaScript's safe integer range");
      if ((byte & 0x80) === 0) return value;
      factor *= 128;
      if (i === 9) fail("varint is too long");
    }
    fail("varint is too long");
  };
  if (bytes.length < 4 || bytes[0] !== 0x47 || bytes[1] !== 0x44 || bytes[2] !== 0x44 || bytes[3] !== 0x02) {
    fail("bad magic or unsupported version");
  }
  pos = 4;
  const metadataLength = readUvarint();
  if (metadataLength > 16 * 1024 * 1024 || metadataLength > bytes.length - pos) fail("invalid metadata length");
  let metadata;
  try {
    metadata = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes.subarray(pos, pos + metadataLength)));
  } catch (error) {
    fail("invalid metadata JSON or UTF-8 (" + error.message + ")");
  }
  pos += metadataLength;
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata) || Object.prototype.hasOwnProperty.call(metadata, "root")) {
    fail("metadata must be an object without root");
  }

  const stringCount = readUvarint();
  if (stringCount > 20_000_000 || stringCount > bytes.length - pos) fail("invalid string table count");
  const decoder = new TextDecoder("utf-8", { fatal: true });
  const strings = new Array(stringCount + 1);
  strings[0] = "";
  for (let i = 1; i <= stringCount; i++) {
    const length = readUvarint();
    if (length > bytes.length - pos) fail("string exceeds remaining input");
    try {
      strings[i] = decoder.decode(bytes.subarray(pos, pos + length));
    } catch (error) {
      fail("invalid string UTF-8 (" + error.message + ")");
    }
    pos += length;
  }
  const stringAt = ref => {
    if (ref >= strings.length) fail("string reference out of range");
    return strings[ref];
  };
  const nodeCount = readUvarint();
  if (nodeCount < 1 || nodeCount > 5_000_000 || nodeCount > Math.floor((bytes.length - pos - 1) / 8)) fail("invalid node count");
  const nodes = new Array(nodeCount);
  const stack = [];
  const typeNames = ["", "file", "dir"];
  for (let i = 0; i < nodeCount; i++) {
    while (stack.length && stack[stack.length - 1].remaining === 0) stack.pop();
    if (i > 0 && stack.length === 0) fail("node tree has more than one root");
    if (stack.length > 4096) fail("node tree exceeds nesting limit");
    const nameRef = readUvarint();
    const pathRef = readUvarint();
    const afterTypeCode = readUvarint();
    const after = readUvarint(false);
    const afterFiles = readUvarint();
    const afterItems = readUvarint();
    const afterMtimeRef = readUvarint();
    const childCount = readUvarint();
    if (nameRef === 0 || afterTypeCode > 2 || childCount > nodeCount - i - 1) fail("invalid base node record");
    if (afterFiles > 5_000_000 || afterItems > 5_000_000) fail("invalid after node counts");
    const name = stringAt(nameRef);
    if (!name) fail("empty node name");
    const parentPath = stack.length ? stack[stack.length - 1].path : "";
    const expectedPath = parentPath === "" ? name : parentPath === "/" ? "/" + name.replace(/^\/+/, "") : parentPath.replace(/\/+$/, "") + "/" + name.replace(/^\/+/, "");
    const path = pathRef ? stringAt(pathRef) : expectedPath;
    if (!path) fail("empty node path");
    const afterType = typeNames[afterTypeCode];
    const node = {
      name, path, type: afterType,
      before: after, after, delta: 0,
      beforeFiles: afterFiles, afterFiles,
      beforeItems: afterItems, afterItems,
      status: "unchanged"
    };
    if (afterType) node.beforeType = afterType;
    if (afterType) node.afterType = afterType;
    const afterMtime = stringAt(afterMtimeRef);
    if (afterMtime) {
      node.beforeMtime = afterMtime;
      node.afterMtime = afterMtime;
    }
    if (stack.length) {
      const parent = nodes[stack[stack.length - 1].index];
      const frame = stack[stack.length - 1];
      parent.children[frame.nextChild++] = node;
      stack[stack.length - 1].remaining--;
    }
    nodes[i] = node;
    if (childCount) {
      node.children = new Array(childCount);
      stack.push({ index: i, path, remaining: childCount, nextChild: 0 });
    }
  }
  while (stack.length && stack[stack.length - 1].remaining === 0) stack.pop();
  if (stack.length) fail("node tree ended before all children");

  const overlayCount = readUvarint();
  if (overlayCount > nodeCount || overlayCount > Math.floor((bytes.length - pos) / 7)) fail("overlay count exceeds node count or remaining input");
  let previousIndex = -1;
  const statusNames = ["unchanged", "added", "removed", "grown", "shrunk", "changed"];
  for (let i = 0; i < overlayCount; i++) {
    const encodedDelta = readUvarint();
    const index = previousIndex < 0 ? encodedDelta : previousIndex + encodedDelta;
    if (index >= nodeCount || (previousIndex >= 0 && index <= previousIndex)) fail("overlay indices are not strictly increasing");
    previousIndex = index;
    const beforeTypeCode = readUvarint();
    const before = readUvarint(false);
    const beforeFiles = readUvarint();
    const beforeItems = readUvarint();
    const beforeMtimeRef = readUvarint();
    const statusCode = readUvarint();
    if (beforeTypeCode > 2 || statusCode >= statusNames.length) fail("invalid overlay record");
    if (beforeFiles > 5_000_000 || beforeItems > 5_000_000) fail("invalid before node counts");
    const node = nodes[index];
    const beforeType = typeNames[beforeTypeCode];
    node.before = before;
    node.beforeFiles = beforeFiles;
    node.beforeItems = beforeItems;
    if (beforeType) node.beforeType = beforeType;
    else delete node.beforeType;
    const beforeMtime = stringAt(beforeMtimeRef);
    if (beforeMtime) node.beforeMtime = beforeMtime;
    else delete node.beforeMtime;
    node.delta = node.after - before;
    node.type = beforeType === "dir" || node.afterType === "dir" ? "dir" : node.afterType || beforeType;
    node.status = statusNames[statusCode];
  }
  if (pos !== bytes.length) fail("trailing bytes");
  metadata.root = nodes[0];
  return metadata;
}
