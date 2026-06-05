package tools

// DataLoaderBootstrap is Python prepended to data/code tool executions. It
// defines migi_load() — a robust dataframe loader that fixes the most common
// real-world spreadsheet/CSV failure modes:
//
//   - Multi-sheet Excel: pd.read_excel(path) silently loads only the first
//     sheet. migi_load picks the richest sheet (and tells the model how to
//     target another) or migi_load_all() returns every sheet.
//   - CSV encoding: real CSVs are often Windows-1252/Latin-1/BOM, not UTF-8.
//     migi_load detects encoding (charset_normalizer) and falls back.
//   - CSV delimiter: European exports use ';', some use tabs/pipes. Sniffed.
//   - Path resolution: resolves bare filenames against /data, and if it still
//     can't find the file but /data holds exactly one, uses that.
//   - Parquet: installs pyarrow on demand.
//
// No `%` formatting and no backticks here — it is concatenated (never passed
// through fmt.Sprintf), so it is safe to embed verbatim.
const DataLoaderBootstrap = `
def _migi_list_data():
    import os
    try:
        return sorted([f for f in os.listdir('/data') if os.path.isfile(os.path.join('/data', f))])
    except Exception:
        return []

def _migi_resolve(path):
    import os
    if path and os.path.exists(path):
        return path
    cands = []
    if path and not os.path.isabs(path):
        cands.append(os.path.join('/data', path))
        cands.append(os.path.join('/data', os.path.basename(path)))
    for c in cands:
        if os.path.exists(c):
            return c
    files = _migi_list_data()
    if len(files) == 1:
        return os.path.join('/data', files[0])
    if path:
        base = os.path.basename(str(path)).lower()
        for f in files:
            if f.lower() == base:
                return os.path.join('/data', f)
    raise FileNotFoundError("migi_load: could not find " + repr(path) + ". Files available in /data: " + repr(files))

def _migi_norm(s):
    import re
    return re.sub(r'[^a-z0-9]', '', str(s).lower())

def migi_schema(df, name=''):
    """Print the EXACT columns + dtypes so names are never guessed."""
    try:
        cols = list(df.columns)
        dts = {str(c): str(t) for c, t in df.dtypes.items()}
        tag = (' ' + str(name)) if name else ''
        print('[schema]%s rows=%d cols=%d' % (tag, len(df), len(cols)))
        print('[schema] columns = %s' % cols)
        print('[schema] dtypes  = %s' % dts)
    except Exception:
        pass

def _migi_toks(s):
    import re
    return set(t for t in re.split(r'[^a-z0-9]+', str(s).lower()) if len(t) > 2)

def col(df, name):
    """Resolve a possibly-approximate column name to the REAL one (case-,
    punctuation- and word-order-insensitive, with fuzzy fallback). Use whenever
    a name might not match exactly, e.g. df[col(df, 'monthly salary clean')].
    Raises with the available columns if no confident match."""
    cols = list(df.columns)
    if name in cols:
        return name
    norm = {_migi_norm(c): c for c in cols}
    key = _migi_norm(name)
    if key in norm:
        return norm[key]
    # substring either direction
    subs = [c for c in cols if key and (key in _migi_norm(c) or _migi_norm(c) in key)]
    if len(subs) == 1:
        return subs[0]
    # word-token overlap (handles 'Monthly Salary Clean' -> 'Monthly Salary (Rs)')
    nt = _migi_toks(name)
    if nt:
        scored = sorted(((len(_migi_toks(c) & nt), c) for c in cols), reverse=True)
        if scored[0][0] >= 1 and (len(scored) == 1 or scored[0][0] > scored[1][0]):
            return scored[0][1]
    # fuzzy string fallback
    import difflib
    m = difflib.get_close_matches(key, list(norm.keys()), n=1, cutoff=0.6)
    if m:
        return norm[m[0]]
    raise KeyError('No column matching %r. Available columns: %s' % (name, cols))

def migi_load(path=None, sheet=None, sep=None):
    """Robust loader -> pandas DataFrame; prints the exact schema after loading."""
    df = _migi_load_impl(path, sheet, sep)
    migi_schema(df, path)
    return df

def _migi_load_impl(path=None, sheet=None, sep=None):
    import os, pandas as pd
    path = _migi_resolve(path)
    ext = os.path.splitext(path)[1].lower()
    if ext in ('.xlsx', '.xlsm', '.xls'):
        xls = pd.ExcelFile(path)
        names = list(xls.sheet_names)
        if sheet is None and len(names) > 1:
            # Show EVERY sheet's columns so the model sees the whole workbook,
            # then auto-load the richest sheet.
            print("[migi_load] Workbook has %d sheets:" % len(names))
            best, best_score = names[0], -1
            for s in names:
                try:
                    d = pd.read_excel(xls, s, nrows=50)
                    cols = list(d.columns)
                    score = int(d.shape[1]) * (1 + int(d.notna().sum().sum()))
                except Exception:
                    cols, score = [], -1
                print("  - %r: %d cols %s" % (s, len(cols), cols[:25]))
                if score > best_score:
                    best, best_score = s, score
            print("[migi_load] auto-loaded %r (richest). Use migi_load(path, sheet='Name') for another sheet, migi_describe(path) for full per-sheet schemas, or migi_load_all(path) for all." % best)
            sheet = best
        return pd.read_excel(xls, 0 if sheet is None else sheet)
    if ext == '.json':
        return pd.read_json(path)
    if ext == '.parquet':
        try:
            import pyarrow  # noqa: F401
        except Exception:
            import subprocess, sys
            subprocess.run([sys.executable, '-m', 'pip', 'install', '-q', 'pyarrow'], check=False)
        return pd.read_parquet(path)
    # CSV / TSV / delimited text
    enc = 'utf-8'
    try:
        from charset_normalizer import from_path
        guess = from_path(path).best()
        if guess and guess.encoding:
            enc = guess.encoding
    except Exception:
        pass
    if sep is None:
        try:
            import csv
            with open(path, 'r', encoding=enc, errors='replace') as fh:
                sample = fh.read(16384)
            sep = csv.Sniffer().sniff(sample, delimiters=',;\t|').delimiter
        except Exception:
            sep = ','
    try:
        return pd.read_csv(path, sep=sep, encoding=enc, engine='python', on_bad_lines='skip')
    except (UnicodeDecodeError, LookupError):
        return pd.read_csv(path, sep=sep, encoding='latin-1', engine='python', on_bad_lines='skip')

def migi_load_all(path=None):
    """Load every sheet of an Excel workbook as {sheet_name: DataFrame}."""
    import pandas as pd
    sheets = pd.read_excel(_migi_resolve(path), sheet_name=None)
    for nm, d in sheets.items():
        migi_schema(d, 'sheet=' + str(nm))
    return sheets

def migi_describe(path=None):
    """Inspect the FULL structure before analysis. For Excel, prints columns +
    dtypes for EVERY sheet (and returns {sheet_name: DataFrame}); for CSV/JSON,
    prints the single table's schema. Call this first on any workbook so you
    pick the right sheet AND the right column names."""
    import os, pandas as pd
    path = _migi_resolve(path)
    ext = os.path.splitext(path)[1].lower()
    if ext in ('.xlsx', '.xlsm', '.xls'):
        sheets = pd.read_excel(path, sheet_name=None)
        print('[describe] Excel workbook: %d sheet(s)' % len(sheets))
        for nm, d in sheets.items():
            migi_schema(d, 'sheet=' + str(nm))
        return sheets
    df = _migi_load_impl(path)
    migi_schema(df, path)
    return {'data': df}

def migi_chart_style(accent='#4F46E5'):
    """Apply Migi's modern chart aesthetics. Call once before plotting."""
    try:
        import matplotlib as mpl
        palette = [accent, '#06B6D4', '#F59E0B', '#10B981', '#EF4444', '#8B5CF6', '#EC4899', '#64748B']
        mpl.rcParams.update({
            'figure.figsize': (10, 5.6), 'figure.dpi': 150, 'savefig.dpi': 150,
            'savefig.bbox': 'tight', 'figure.autolayout': True,
            'font.size': 11, 'axes.titlesize': 15, 'axes.titleweight': 'bold', 'axes.titlepad': 12,
            'axes.labelsize': 11, 'axes.labelcolor': '#334155', 'text.color': '#1E293B',
            'axes.edgecolor': '#CBD5E1', 'axes.linewidth': 0.8,
            'axes.grid': True, 'grid.color': '#E2E8F0', 'grid.linewidth': 0.8,
            'axes.spines.top': False, 'axes.spines.right': False,
            'xtick.color': '#475569', 'ytick.color': '#475569',
            'axes.prop_cycle': mpl.cycler(color=palette),
        })
        try:
            import seaborn as sns
            sns.set_palette(palette)
        except Exception:
            pass
    except Exception:
        pass
`
