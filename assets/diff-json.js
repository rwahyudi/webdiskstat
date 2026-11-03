// Parse decompressed JSON incrementally. Only the current input chunk and one
// scalar token are retained; the resulting object has the same shape as
// JSON.parse, without materializing the report as a single string.
async function parseDiffJSONStream(stream) {
  const reader = stream.getReader();
  const decoder = new TextDecoder('utf-8', { fatal: true });
  const maxTokenLength = 16 * 1024 * 1024;
  const maxDepth = 4096;
  let buffer = '';
  let offset = 0;
  let done = false;
  let charsSinceYield = 0;

  async function fill() {
    if (offset) {
      buffer = buffer.slice(offset);
      offset = 0;
    }
    while (offset >= buffer.length && !done) {
      const result = await reader.read();
      if (result.done) {
        buffer += decoder.decode();
        done = true;
      } else {
        buffer += decoder.decode(result.value, { stream: true });
      }
    }
  }
  async function peek() {
    await fill();
    return offset < buffer.length ? buffer[offset] : '';
  }
  async function take() {
    const ch = await peek();
    if (ch) {
      offset++;
      charsSinceYield++;
      if (charsSinceYield >= 8 * 1024 * 1024) {
        charsSinceYield = 0;
        await new Promise(resolve => setTimeout(resolve, 0));
      }
    }
    return ch;
  }
  async function advance(count) {
    charsSinceYield += count;
    if (charsSinceYield >= 8 * 1024 * 1024) {
      charsSinceYield %= 8 * 1024 * 1024;
      await new Promise(resolve => setTimeout(resolve, 0));
    }
  }
  async function whitespace() {
    while (true) {
      await fill();
      let end = offset;
      while (end < buffer.length && ' \t\r\n'.includes(buffer[end])) end++;
      if (end > offset) {
        const consumed = end - offset;
        offset = end;
        await advance(consumed);
      }
      if (end < buffer.length || done) return;
    }
  }
  async function expect(wanted) {
    const actual = await take();
    if (actual !== wanted) throw new SyntaxError('Invalid comparison JSON: expected ' + wanted);
  }
  async function stringValue() {
    const pieces = ['"'];
    let length = 1;
    await expect('"');
    let escaped = false;
    while (true) {
      await fill();
      if (offset >= buffer.length) throw new SyntaxError('Invalid comparison JSON: unterminated string');
      const start = offset;
      let closed = false;
      while (offset < buffer.length) {
        const ch = buffer[offset++];
        if (escaped) escaped = false;
        else if (ch === '\\') escaped = true;
        else if (ch === '"') { closed = true; break; }
        else if (ch.charCodeAt(0) < 0x20) throw new SyntaxError('Invalid comparison JSON: control character in string');
      }
      const piece = buffer.slice(start, offset);
      pieces.push(piece);
      length += piece.length;
      if (length > maxTokenLength) throw new SyntaxError('Comparison JSON string exceeds the 16 MiB limit');
      await advance(offset - start);
      if (closed) break;
    }
    try {
      return JSON.parse(pieces.join(''));
    } catch (_) {
      throw new SyntaxError('Invalid comparison JSON string escape');
    }
  }
  async function scalar() {
    const pieces = [];
    let length = 0;
    while (true) {
      await fill();
      if (offset >= buffer.length) break;
      const start = offset;
      while (offset < buffer.length && !' \t\r\n,:]}'.includes(buffer[offset])) offset++;
      if (offset > start) {
        const piece = buffer.slice(start, offset);
        pieces.push(piece);
        length += piece.length;
        if (length > maxTokenLength) throw new SyntaxError('Comparison JSON scalar exceeds the 16 MiB limit');
        await advance(offset - start);
      }
      if (offset < buffer.length) break;
    }
    if (!length) throw new SyntaxError('Invalid comparison JSON value');
    try {
      return JSON.parse(pieces.join(''));
    } catch (_) {
      throw new SyntaxError('Invalid comparison JSON value');
    }
  }
  async function value(depth) {
    if (depth > maxDepth) throw new SyntaxError('Comparison JSON exceeds the 4096-level nesting limit');
    await whitespace();
    const ch = await peek();
    if (ch === '"') return stringValue();
    if (ch === '{') {
      await take();
      const object = {};
      await whitespace();
      if (await peek() === '}') { await take(); return object; }
      while (true) {
        await whitespace();
        if (await peek() !== '"') throw new SyntaxError('Invalid comparison JSON: expected an object key');
        const key = await stringValue();
        await whitespace();
        await expect(':');
        const item = await value(depth + 1);
        Object.defineProperty(object, key, { value: item, enumerable: true, configurable: true, writable: true });
        await whitespace();
        const separator = await take();
        if (separator === '}') return object;
        if (separator !== ',') throw new SyntaxError('Invalid comparison JSON: expected , or }');
      }
    }
    if (ch === '[') {
      await take();
      const array = [];
      await whitespace();
      if (await peek() === ']') { await take(); return array; }
      while (true) {
        array.push(await value(depth + 1));
        await whitespace();
        const separator = await take();
        if (separator === ']') return array;
        if (separator !== ',') throw new SyntaxError('Invalid comparison JSON: expected , or ]');
      }
    }
    return scalar();
  }

  try {
    const result = await value(0);
    await whitespace();
    if (await peek()) throw new SyntaxError('Invalid comparison JSON: trailing data');
    return result;
  } catch (error) {
    try { await reader.cancel(error); } catch (_) {}
    throw error;
  } finally {
    reader.releaseLock();
  }
}
