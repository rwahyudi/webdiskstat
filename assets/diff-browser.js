// Diff-only adapter around the ordinary report's extracted browser functions.
// No comparison summaries or ranking arrays enter this browser's mutable state.
window.DiffBrowser = (() => {
  "use strict";

  /* NORMAL_BROWSER_FUNCTIONS */
  const TREE_ROW_HEIGHT = 36;
  const TREE_OVERSCAN_ROWS = 8;
  const TREE_COLUMN_STORAGE_KEY = 'webdiskstat-diff-tree-columns';
  const TREE_COLUMN_WIDTH_STORAGE_KEY = 'webdiskstat-diff-tree-column-widths';
  const SEARCH_RESULT_LIMIT = 50;
  const SEARCH_DEBOUNCE_MS = 120;
  const TREE_COLUMNS = [{
    key: 'name',
    label: 'Name',
    grid: 'minmax(260px,1fr)',
    minWidth: 260,
    required: true,
    sortKey: 'name'
  }, {
    key: 'before',
    group: 'Size',
    label: 'Before',
    minWidth: 82,
    numeric: true
  }, {
    key: 'size',
    group: 'Size',
    label: 'After',
    minWidth: 82,
    numeric: true
  }, {
    key: 'delta',
    group: 'Size',
    label: 'Change',
    minWidth: 90,
    numeric: true
  }, {
    key: 'percent',
    group: 'Size',
    label: 'Change %',
    minWidth: 74,
    numeric: true
  }, {
    key: 'beforeFiles',
    group: 'Files',
    label: 'Before',
    minWidth: 82,
    numeric: true
  }, {
    key: 'files',
    group: 'Files',
    label: 'After',
    minWidth: 82,
    numeric: true
  }, {
    key: 'filesDelta',
    group: 'Files',
    label: 'Change',
    minWidth: 90,
    numeric: true
  }, {
    key: 'filesPercent',
    group: 'Files',
    label: 'Change %',
    minWidth: 74,
    numeric: true
  }, {
    key: 'beforeDirectories',
    group: 'Directories',
    label: 'Before',
    minWidth: 82,
    numeric: true
  }, {
    key: 'directories',
    group: 'Directories',
    label: 'After',
    minWidth: 82,
    numeric: true
  }, {
    key: 'directoriesDelta',
    group: 'Directories',
    label: 'Change',
    minWidth: 90,
    numeric: true
  }, {
    key: 'directoriesPercent',
    group: 'Directories',
    label: 'Change %',
    minWidth: 74,
    numeric: true
  }, {
    key: 'beforeItems',
    group: 'Items',
    label: 'Before',
    minWidth: 82,
    numeric: true
  }, {
    key: 'items',
    group: 'Items',
    label: 'After',
    minWidth: 82,
    numeric: true
  }, {
    key: 'itemsDelta',
    group: 'Items',
    label: 'Change',
    minWidth: 90,
    numeric: true
  }, {
    key: 'itemsPercent',
    group: 'Items',
    label: 'Change %',
    minWidth: 74,
    numeric: true
  }, {
    key: 'modified',
    label: 'Modified after',
    minWidth: 140
  }, {
    key: 'status',
    label: 'Status',
    minWidth: 86
  }];
  for (const column of TREE_COLUMNS) {
    column.grid ||= column.minWidth + 'px';
    column.sortKey ||= column.key;
    column.menuLabel = column.group ? column.group + ' ' + column.label.toLowerCase() : column.label;
  }
  const DEFAULT_TREE_COLUMNS = {
    before: true,
    size: true,
    delta: true,
    percent: false,
    beforeItems: true,
    items: true,
    itemsDelta: true,
    itemsPercent: false,
    beforeFiles: true,
    files: true,
    filesDelta: true,
    filesPercent: false,
    beforeDirectories: true,
    directories: true,
    directoriesDelta: true,
    directoriesPercent: false,
    modified: false,
    status: true
  };
  const state = {
    current: null,
    selected: null,
    sortKey: 'size',
    sortDir: 'desc',
    searchActiveIndex: -1,
    visibleColumns: {
      ...DEFAULT_TREE_COLUMNS
    },
    columnWidths: {}
  };
  const treeView = {
    node: null,
    children: [],
    body: null,
    total: 0,
    start: -1,
    end: -1
  };
  const byPath = new Map();
  const materialIconCache = new Map();
  const selectionMemory = new WeakMap();
  let DATA,
    el,
    searchIndex = [],
    searchResults = [],
    searchTimer = 0;
  let searchDisabled = false,
    searchIndexBuilt = true,
    eligibility = null;
  const nodeParent = node => parents.get(node) || null;
  function rebuildEligibility() {
    const wanted = document.getElementById('status').value;
    eligibility = null;
    if (wanted === 'all') return;
    eligibility = new WeakSet();
    const stack = [[DATA, false]];
    while (stack.length) {
      const [node, done] = stack.pop();
      if (!done) {
        stack.push([node, true]);
        for (const child of node.children || []) stack.push([child, false]);
        continue;
      }
      if (node.status === wanted || (node.children || []).some(child => eligibility.has(child))) eligibility.add(node);
    }
  }
  function filteredChildren(node) {
    return (node.children || []).filter(child => !eligibility || eligibility.has(child));
  }
  function sortedChildren(node) {
    if (['name', 'size', 'files', 'items', 'modified'].includes(state.sortKey)) return normalSortedChildren(node);
    const children = filteredChildren(node),
      direction = state.sortDir === 'asc' ? 1 : -1;
    children.sort((a, b) => {
      const value = n => {
        if (state.sortKey === 'percent') return n.before ? n.delta / n.before : n.after ? Infinity : 0;
        if (state.sortKey === 'itemsPercent') return n.beforeItems ? (n.afterItems - n.beforeItems) / n.beforeItems : n.afterItems ? Infinity : 0;
        if (state.sortKey === 'itemsDelta') return n.afterItems - n.beforeItems;
        if (state.sortKey === 'filesDelta') return n.afterFiles - n.beforeFiles;
        if (state.sortKey === 'filesPercent') return n.beforeFiles ? (n.afterFiles - n.beforeFiles) / n.beforeFiles : n.afterFiles ? Infinity : 0;
        if (state.sortKey === 'directoriesDelta') return n.afterDirectories - n.beforeDirectories;
        if (state.sortKey === 'directoriesPercent') return n.beforeDirectories ? (n.afterDirectories - n.beforeDirectories) / n.beforeDirectories : n.afterDirectories ? Infinity : 0;
        if (state.sortKey === 'before') return n.before;
        if (state.sortKey === 'beforeItems') return n.beforeItems;
        if (state.sortKey === 'beforeFiles') return n.beforeFiles;
        if (state.sortKey === 'files') return n.afterFiles;
        if (state.sortKey === 'beforeDirectories') return n.beforeDirectories;
        if (state.sortKey === 'directories') return n.afterDirectories;
        if (state.sortKey === 'items') return n.afterItems;
        return n[state.sortKey];
      };
      const av = value(a),
        bv = value(b);
      let result = typeof av === 'number' ? av === bv ? 0 : av > bv ? 1 : -1 : String(av ?? '').localeCompare(String(bv ?? ''));
      if (result) return result * direction;
      return (b.size || 0) - (a.size || 0) || a.name.localeCompare(b.name, undefined, {
        numeric: true,
        sensitivity: 'base'
      });
    });
    return children;
  }
  function activateSearchResult(index = state.searchActiveIndex) {
    if (!searchResults.length) return;
    if (document.getElementById('status').value !== 'all') {
      document.getElementById('status').value = 'all';
      rebuildEligibility();
    }
    normalActivateSearchResult(index);
  }
  function renderDetails() {
    const node = state.selected || state.current;
    if (!node) return;
    const info = document.getElementById('selectionInfo');
    info.replaceChildren();
    const path = document.createElement('span');
    path.textContent = node.path || node.name;
    path.title = path.textContent;
    const value = document.createElement('strong');
    value.textContent = formatBytes(node.after) + ' after · ' + bytes(node.delta, true) + ' change';
    info.append(path, value);
  }
  function setSelected(node) {
    if (!node) return;
    state.selected = node;
    selectionMemory.set(state.current, node);
    renderDetails();
    el.tree.querySelectorAll('.row[data-id]').forEach(row => {
      const active = row.dataset.id === String(node.id);
      row.classList.toggle('active', active);
      row.setAttribute('aria-pressed', String(active));
    });
  }
  function setCurrent(node, updateUrl = true, focusFirst = false) {
    if (!node) return;
    if (state.current && state.selected) selectionMemory.set(state.current, state.selected);
    const remembered = selectionMemory.get(node);
    state.current = node;
    current = node;
    state.selected = remembered || node;
    el.tree.scrollTop = 0;
    if (updateUrl) syncUrlToCurrent(node);
    renderSafely();
    if (focusFirst) {
      if (!remembered) focusFirstTreeRow();else {
        scrollTreeSelectionIntoView(remembered);
        requestAnimationFrame(() => el.tree.querySelector('.row[data-id="' + remembered.id + '"]')?.focus({
          preventScroll: true
        }));
      }
    }
  }
  function goParent() {
    const parent = nodeParent(state.current);
    if (parent) setCurrent(parent, true, true);
  }
  function createTreeRow(child) {
    const row = document.createElement('div');
    row.className = 'row ' + child.type;
    const selected = state.selected === child;
    row.classList.toggle('active', selected);
    row.dataset.id = String(child.id);
    row.title = child.path || child.name;
    row.tabIndex = 0;
    row.setAttribute('role', 'button');
    row.setAttribute('aria-pressed', String(selected));
    const spoken = [(child.type === 'dir' ? 'Directory ' : 'File ') + child.name];
    row.addEventListener('click', () => setSelected(child));
    row.addEventListener('dblclick', () => {
      if (child.type === 'dir') setCurrent(child, true, true);
    });
    row.addEventListener('keydown', event => {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        event.stopPropagation();
        setSelected(child);
        if (child.type === 'dir') setCurrent(child, true, true);
      }
    });
    for (const column of visibleTreeColumns()) {
      const cell = document.createElement('div');
      cell.dataset.column = column.key;
      if (column.key === 'name') {
        cell.className = 'row-name';
        const kind = document.createElement('span');
        kind.className = 'row-kind';
        kind.textContent = child.type === 'dir' ? 'DIR' : 'FILE';
        const label = document.createElement('span');
        label.textContent = child.name;
        cell.append(makeFileIcon(child), kind, label);
      } else {
        cell.className = column.numeric ? 'row-count' : 'row-modified';
        switch (column.key) {
          case 'before':
            cell.textContent = formatBytes(child.before);
            break;
          case 'size':
            cell.textContent = formatBytes(child.after);
            break;
          case 'delta':
            cell.textContent = bytes(child.delta, true);
            cell.classList.add(child.delta > 0 ? 'up' : child.delta < 0 ? 'down' : 'muted');
            break;
          case 'percent':
            cell.textContent = percentage(child);
            cell.classList.add(child.delta > 0 ? 'up' : child.delta < 0 ? 'down' : 'muted');
            break;
          case 'files':
            cell.textContent = formatCount(child.afterFiles);
            break;
          case 'beforeFiles':
            cell.textContent = formatCount(child.beforeFiles);
            break;
          case 'filesDelta':
          case 'filesPercent':
          case 'directoriesDelta':
          case 'directoriesPercent': {
            const directories = column.key.startsWith('directories');
            const before = directories ? child.beforeDirectories : child.beforeFiles;
            const after = directories ? child.afterDirectories : child.afterFiles;
            const delta = after - before;
            cell.textContent = column.key.endsWith('Percent') ? percentage({before, after, delta}) : signed(delta);
            cell.classList.add(delta > 0 ? 'up' : delta < 0 ? 'down' : 'muted');
            break;
          }
          case 'directories':
            cell.textContent = formatCount(child.afterDirectories);
            break;
          case 'beforeDirectories':
            cell.textContent = formatCount(child.beforeDirectories);
            break;
          case 'items':
            cell.textContent = child.afterItems === undefined ? '—' : formatCount(child.afterItems);
            break;
          case 'beforeItems':
            cell.textContent = child.beforeItems === undefined ? '—' : formatCount(child.beforeItems);
            break;
          case 'itemsDelta':
          case 'itemsPercent': {
            const delta = child.afterItems - child.beforeItems;
            cell.textContent = column.key === 'itemsDelta' ? signed(delta) : percentage({before: child.beforeItems, after: child.afterItems, delta});
            cell.classList.add(delta > 0 ? 'up' : delta < 0 ? 'down' : 'muted');
            break;
          }
          case 'modified':
            cell.textContent = formatListModifiedTime(child.afterMtime);
            cell.title = formatModifiedTime(child.afterMtime) || 'Modified time unavailable';
            break;
          case 'status':
            cell.textContent = child.status === 'unchanged' ? 'Context' : child.status;
            if (child.beforeType && child.afterType && child.beforeType !== child.afterType) {
              cell.title = child.beforeType + ' → ' + child.afterType;
            }
            break;
        }
        cell.title ||= cell.textContent;
        spoken.push(column.menuLabel + ': ' + cell.textContent);
      }
      row.append(cell);
    }
    row.setAttribute('aria-label', spoken.join(' · '));
    return row;
  }
  // Both header tiers use the row grid, so visibility and persisted resizing stay aligned.
  function renderDiffTreeHeader() {
    const header = document.createElement('div');
    header.className = 'tree-header diff-grouped-header';
    const columnsMenu = makeTreeColumnsMenu();
    const columns = visibleTreeColumns();
    for (const group of ['Size', 'Files', 'Directories', 'Items']) {
      const indexes = columns.flatMap((column, index) => column.group === group ? [index] : []);
      if (!indexes.length) continue;
      const heading = document.createElement('div');
      heading.className = 'tree-column-group';
      heading.textContent = group;
      heading.style.gridColumn = (indexes[0] + 1) + ' / ' + (indexes.at(-1) + 2);
      heading.style.gridRow = '1';
      header.append(heading);
    }
    columns.forEach((column, index) => {
      column.hasSeparator = index < columns.length - 1;
      let content;
      if (column.key === 'name') {
        content = document.createElement('div');
        content.className = 'tree-name-head';
        content.append(makeParentHeaderButton(), makeHeaderButton('Name', 'name'), makeTreeColumnsButton(columnsMenu));
      } else {
        content = makeHeaderButton(column.label, column.sortKey, column.numeric);
        // Nested labels need the metric in their accessible names, including percent.
        const label = 'Sort by ' + column.menuLabel;
        content.title = label;
        content.setAttribute('aria-label', label + (state.sortKey === column.sortKey ? ', currently ' + (state.sortDir === 'asc' ? 'ascending' : 'descending') : ''));
      }
      const cell = makeTreeHeaderCell(column, content);
      cell.style.gridColumn = String(index + 1);
      cell.style.gridRow = column.group ? '2' : '1 / 3';
      header.append(cell);
    });
    header.append(columnsMenu);
    el.tree.append(header);
  }
  function renderTree() {
    el.tree.replaceChildren();
    resetTreeView();
    applyTreeColumnLayout();
    renderDiffTreeHeader();
    const children = sortedChildren(state.current);
    ensureListSelection(children);
    document.getElementById('range').textContent = children.length.toLocaleString() + ' paths';
    if (!children.length) {
      const empty = document.createElement('div');
      empty.id = 'empty';
      empty.className = 'empty';
      empty.textContent = changed.length ? 'No matching changes in this directory. Clear the status filter or open another directory.' : 'No changes found. The snapshots have identical compared metadata.';
      el.tree.append(empty);
      renderDetails();
      return;
    }
    const body = document.createElement('div');
    body.className = 'tree-body';
    body.id = 'rows';
    body.style.height = children.length * TREE_ROW_HEIGHT + 'px';
    el.tree.append(body);
    treeView.node = state.current;
    treeView.children = children;
    treeView.total = state.current.size;
    treeView.body = body;
    renderVisibleTreeRows(true);
    renderDetails();
  }
  function renderSafely() {
    renderCrumbs();
    renderTree();
  }
  async function copyCurrentPath() {
    const text = pathForNode(state.current),
      message = document.getElementById('copyMessage');
    let copied = false;
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
        copied = true;
      }
    } catch (_) {}
    if (!copied) {
      const input = document.createElement('textarea');
      input.value = text;
      input.style.position = 'fixed';
      input.style.opacity = '0';
      document.body.append(input);
      input.select();
      try {
        copied = document.execCommand('copy');
      } catch (_) {}
      input.remove();
      el.copyPathButton.focus();
    }
    message.textContent = copied ? 'Copied' : 'Copy unavailable';
    message.classList.add('show');
    setTimeout(() => message.classList.remove('show'), 1600);
  }
  function initialize(root) {
    DATA = root;
    byPath.clear();
    let id = 0;
    const stack = [[root, 0]];
    while (stack.length) {
      const [node, depth] = stack.pop();
      node.id = String(++id);
      node.depth = depth;
      node.size = node.after;
      node.files = node.afterFiles;
      node.items = node.afterItems;
      // Items counts descendants, excluding the directory itself.
      node.beforeDirectories = Math.max(0, node.beforeItems - node.beforeFiles);
      node.afterDirectories = Math.max(0, node.afterItems - node.afterFiles);
      node.mtime = node.afterMtime;
      byPath.set(node.path, node);
      for (const child of node.children || []) stack.push([child, depth + 1]);
    }
    el = {
      tree: document.getElementById('tree'),
      crumbs: document.getElementById('crumbs'),
      copyPathButton: document.getElementById('copyPathButton'),
      searchInput: document.getElementById('search'),
      searchResults: document.getElementById('searchResults')
    };
    searchIndex = changed;
    state.visibleColumns = readStoredTreeColumns();
    state.columnWidths = readStoredTreeColumnWidths();
    state.current = nodeFromLocationHash() || root;
    state.selected = state.current;
    current = state.current;
    rebuildEligibility();
    renderSafely();
    // The report is unhidden by boot after initialization; measure virtualization
    // once it is paintable rather than trusting the encrypted loading layout.
    requestAnimationFrame(() => renderVisibleTreeRows(true));
    el.tree.addEventListener('scroll', () => renderVisibleTreeRows(), {
      passive: true
    });
    el.tree.addEventListener('keydown', event => {
      if (event.target === el.tree && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
        event.preventDefault();
        event.stopPropagation();
        el.tree.scrollBy({
          left: event.key === 'ArrowRight' ? 80 : -80
        });
      }
    });
    document.getElementById('status').addEventListener('change', () => {
      rebuildEligibility();
      el.tree.scrollTop = 0;
      renderTree();
    });
    document.getElementById('reset').onclick = () => {
      document.getElementById('status').value = 'all';
      el.searchInput.value = '';
      closeSearchResults();
      rebuildEligibility();
      setCurrent(root);
    };
    el.copyPathButton.addEventListener('click', copyCurrentPath);
    document.getElementById('searchShortcut').addEventListener('click', focusSearch);
    el.searchInput.addEventListener('input', scheduleSearchResults);
    el.searchInput.addEventListener('focus', () => renderSearchResultsForQuery(el.searchInput.value));
    el.searchInput.addEventListener('click', selectSearchInputText);
    el.searchInput.addEventListener('keydown', event => {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        if (!searchResults.length) renderSearchResultsForQuery(el.searchInput.value);
        moveSearchActiveResult(event.key === 'ArrowDown' ? 1 : -1);
      } else if (event.key === 'Enter') {
        event.preventDefault();
        if (searchTimer) {
          clearTimeout(searchTimer);
          searchTimer = 0;
          renderSearchResultsForQuery(el.searchInput.value);
        }
        activateSearchResult();
      } else if (event.key === 'Escape') {
        event.preventDefault();
        closeSearchResults();
        el.searchInput.blur();
      }
    });
    const help = document.getElementById('browserHelp');
    document.getElementById('browserHelpButton').onclick = () => help.showModal();
    document.getElementById('browserHelpClose').onclick = () => help.close();
    document.addEventListener('click', event => {
      if (!event.target.closest('.search')) closeSearchResults();
      if (!event.target.closest('.tree-columns-menu,.tree-columns-btn')) closeTreeColumnsMenu();
    });
    document.addEventListener('keydown', event => {
      if (event.defaultPrevented) return;
      if (help.open) return;
      if (event.target.closest('dialog[open]')) return;
      if (event.key === 'Escape') {
        if (closeTreeColumnsMenu(true)) {
          event.preventDefault();
          return;
        }
        closeSearchResults();
        return;
      }
      if ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === 'f') {
        event.preventDefault();
        focusSearch();
        return;
      }
      if (isTextEditingTarget(event.target) || event.target.closest('button,a,[contenteditable="true"]')) return;
      if (event.key === '?' && !event.ctrlKey && !event.metaKey && !event.altKey) {
        event.preventDefault();
        help.showModal();
        return;
      }
      if (event.key === '/' && !event.ctrlKey && !event.metaKey && !event.altKey) {
        event.preventDefault();
        focusSearch();
        return;
      }
      if (event.altKey || event.ctrlKey || event.metaKey) return;
      if (event.key === 'Backspace' || event.key === 'ArrowLeft') {
        event.preventDefault();
        goParent();
        return;
      }
      handleListKey(event);
    });
    window.addEventListener('resize', () => {
      compactCrumbs();
      renderVisibleTreeRows(true);
    });
    window.addEventListener('hashchange', applyLocationHash);
    window.addEventListener('popstate', applyLocationHash);
  }
  return {
    initialize,
    navigate: setCurrent,
    select: setSelected,
    refresh: renderTree,
    // Read-only snapshots are useful to the same-scenario normal/diff checks.
    snapshot: () => ({
      path: state.current?.path,
      selected: state.selected?.path,
      sortKey: state.sortKey,
      sortDir: state.sortDir,
      total: treeView.children.length
    })
  };
})();
