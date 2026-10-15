#!/usr/bin/env bash
# smoke-docs.sh: run every documented imgen example for real and check the output.
#
# It extracts each `imgen ...` command from the sh/bash/shell blocks in README.md
# and docs/*.md, classifies it, prices the paid ones with --dry-run, stops if the
# total is over the cap, then runs the pages in order and checks every image.
#
# Usage: scripts/smoke-docs.sh [--estimate] [--max-usd N] [--out DIR] [--reuse DIR]
#   --estimate   plan and price only; make no paid call
#   --max-usd N  refuse to run when the estimate is over N (default 6)
#   --out DIR    run folder (default: $SMOKE_OUT or $TMPDIR/imgen-smoke/run-<time>)
#   --reuse DIR  copy the result of each PASS step of an earlier run whose
#                command is unchanged, instead of paying for it again
#
# Results: <run>/summary.md (PASS/FAIL/SKIP per command), <run>/manifest.tsv,
# <run>/pages/<page>/ (outputs, plus .smoke/<step>.{out,err,rc}),
# <run>/review/ (small copies of each image for a person to look at).
# Exit 0 when nothing failed, 1 on any FAIL, 2 on a usage or setup error,
# 3 when the estimate is over the cap.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
MAX_USD=6
OUT="${SMOKE_OUT:-}"
REUSE=""
ESTIMATE=0

while [ $# -gt 0 ]; do
	case "$1" in
	--estimate) ESTIMATE=1 ;;
	--max-usd) MAX_USD="${2:?--max-usd needs a value}"; shift ;;
	--out) OUT="${2:?--out needs a folder}"; shift ;;
	--reuse) REUSE="$(cd "${2:?--reuse needs a folder}" && pwd)"; shift ;;
	-h | --help) sed -n '2,20p' "$0"; exit 0 ;;
	*) echo "smoke-docs: unknown argument $1" >&2; exit 2 ;;
	esac
	shift
done

for tool in go python3 jq; do
	command -v "$tool" >/dev/null || { echo "smoke-docs: $tool is required" >&2; exit 2; }
done
python3 -I -c 'import PIL' 2>/dev/null || { echo "smoke-docs: python3 needs Pillow (PIL)" >&2; exit 2; }

if [ -z "$OUT" ]; then
	# A fresh private folder: never reuse a predictable path in a shared /tmp.
	OUT="$(mktemp -d "${TMPDIR:-/tmp}/imgen-smoke.XXXXXX")"
fi
mkdir -p "$OUT"
chmod 700 "$OUT"
RUN="$(cd "$OUT" && pwd)"
mkdir -p "$RUN/bin" "$RUN/state" "$RUN/cache" "$RUN/pages" "$RUN/tools" "$RUN/fixtures" "$RUN/review"

# Pin the binary under test, so a parallel `go install` cannot change it mid-run.
(cd "$ROOT" && go build -o "$RUN/bin/imgen" ./cmd/imgen)
export PATH="$RUN/bin:$PATH"
# Isolate history, cache and the budget ledger: --last sees only this run, and
# IMGEN_BUDGET caps this run's own spend.
export XDG_STATE_HOME="$RUN/state" XDG_CACHE_HOME="$RUN/cache"
export IMGEN_HISTORY=off IMGEN_BUDGET="$MAX_USD" IMGEN_QUIET=1
export SMOKE_ROOT="$ROOT" SMOKE_RUN="$RUN" SMOKE_REUSE="$REUSE" SMOKE_FIXTURES="$RUN/fixtures"
imgen version | tee "$RUN/version.txt"

cat >"$RUN/tools/lib.sh" <<'SH'
# Helpers sourced by each page script. Step logs live in .smoke/ so they never
# count as outputs.
_list() { find . -path ./.smoke -prune -o -type f -printf '%P\n' | LC_ALL=C sort; }
# _need FILE...: create a stub for each input that no earlier step produced.
_need() {
	local f
	for f in "$@"; do
		[ -e "$f" ] && continue
		if [ -e "$SMOKE_FIXTURES/$f" ]; then cp "$SMOKE_FIXTURES/$f" "$f"; else cp "$SMOKE_FIXTURES/stub.${f##*.}" "$f"; fi
		echo "$f" >>".smoke/$S.stubbed"
	done
}
_step() { S="$1"; _list >".smoke/$S.before"; }
_done() {
	echo "$1" >".smoke/$S.rc"
	_list >".smoke/$S.after"
	LC_ALL=C comm -13 ".smoke/$S.before" ".smoke/$S.after" >".smoke/$S.outputs"
}
# _dup DIR ID: same command shape as step ID in page DIR; reuse its outputs.
_dup() {
	local f n=0
	# Pages run in parallel: wait until the earlier page has finished this step.
	while [ ! -f "$1/.smoke/$2.outputs" ] && [ "$n" -lt 900 ]; do sleep 2; n=$((n + 1)); done
	while IFS= read -r f; do [ -e "$f" ] || (cd "$1" && cp --parents "$f" "$OLDPWD"); done <"$1/.smoke/$2.outputs"
	cp "$1/.smoke/$2.outputs" ".smoke/$S.outputs"
	echo "$1 $2" >".smoke/$S.dup"
}
# _reuse KEY: copy a PASS result with the same key from an earlier run.
_reuse() {
	local dir id f
	[ -n "$SMOKE_REUSE" ] && [ -f "$SMOKE_REUSE/reuse-index.tsv" ] || return 1
	read -r dir id < <(awk -F'\t' -v k="$1" '$1 == k {print $2, $3; exit}' "$SMOKE_REUSE/reuse-index.tsv") || return 1
	[ -n "$id" ] || return 1
	while IFS= read -r f; do (cd "$dir" && cp --parents "$f" "$OLDPWD"); done <"$dir/.smoke/$id.outputs"
	for f in out err rc outputs; do cp "$dir/.smoke/$id.$f" ".smoke/$S.$f"; done
	echo "$dir $id" >".smoke/$S.reused"
}
SH

cat >"$RUN/tools/smoke.py" <<'PY'
"""Plan and check the docs smoke test. Called by scripts/smoke-docs.sh."""
import glob, hashlib, json, os, re, shlex, subprocess, sys

ROOT = os.environ["SMOKE_ROOT"]
RUN = os.environ["SMOKE_RUN"]
FIX = os.environ["SMOKE_FIXTURES"]
IMG_EXT = (".png", ".jpg", ".jpeg", ".webp", ".svg")
# Inputs the docs name as stand-ins; a stub for these is expected.
PLACEHOLDER_INPUTS = {"in.png", "photo.png", "room.png", "mask.png"}
BOOL_FLAGS = {"--last", "--enhance", "--json", "--meta", "--grid", "--no-embed",
              "--embed-prompt", "--quiet", "--dry-run", "--cache", "--force", "--open",
              "--preview", "--copy", "--notify", "--remove-bg", "--local", "--names",
              "--cost", "--denoise", "-h", "--help"}
MAKERS = {"gen", "edit", "compare", "batch", "upscale"}
FREE_SUBS = {"doctor", "providers", "styles", "profiles", "history", "completion",
             "version", "help", "mcp"}
SAMPLE_CONCEPT = "a satellite orbiting the earth"


def logical_lines(block):
    """Join backslash continuations; keep heredoc bodies as one item."""
    out, buf, start, i = [], "", None, 0
    while i < len(block):
        n, line = block[i]
        if start is None:
            start = n
        if buf == "":
            m = re.search(r"<<-?\s*'?(\w+)'?", line)
            if m:
                tag, body, i = m.group(1), [], i + 1
                while i < len(block) and block[i][1].strip() != tag:
                    body.append(block[i][1])
                    i += 1
                out.append((start, "heredoc", line, body, tag))
                start, i = None, i + 1
                continue
        if line.rstrip().endswith("\\"):
            buf += line.rstrip()[:-1].rstrip() + " "
            i += 1
            continue
        out.append((start, "line", (buf + line.strip()).strip(), None, None))
        buf, start, i = "", None, i + 1
    return [o for o in out if o[2]]


def blocks_of(path):
    lines = open(path, encoding="utf-8").read().split("\n")
    blocks, cur, lang = [], None, None
    for n, line in enumerate(lines, 1):
        m = re.match(r"^\s*```\s*(\w*)", line)
        if m:
            if cur is None:
                cur, lang = [], m.group(1)
            else:
                if lang in ("sh", "bash", "shell"):
                    blocks.append(cur)
                cur = None
            continue
        if cur is not None:
            cur.append((n, line.strip()))
    return blocks


def home_rewrite(s):
    """Keep doc examples out of the real home folder: ~/a/b.json -> b.json."""
    s = re.sub(r'"?\$\{fpath\[1\]\}/([\w.-]+)"?', r"\1", s)
    return re.sub(r"~/(?:[\w.-]+/)*([\w.-]+)", r"\1", s)


def tokens(line):
    lx = shlex.shlex(line, posix=True, punctuation_chars=True)
    lx.whitespace_split = True
    lx.commenters = "#"
    return list(lx)


def imgen_segment(line, env_vars):
    """Return (assignments, argv) of the first imgen command in a shell line."""
    for k, v in env_vars.items():
        line = re.sub(r"\$\{?%s\}?(?!\w)" % re.escape(k), lambda _m, v=v: v.replace("\\", "\\\\"), line)
    try:
        toks = tokens(line)
    except ValueError:
        return None
    seg, segs, skip = [], [], False
    for t in toks:
        if skip:
            skip = False
            continue
        if t in (">", ">>", "<", "2>"):
            skip = True
            continue
        if t in ("|", "||", "&&", ";", "(", ")", "&"):
            segs.append(seg)
            seg = []
            continue
        seg.append(t)
    segs.append(seg)
    for seg in segs:
        assigns = []
        while seg and re.match(r"^[A-Za-z_]\w*=", seg[0]):
            assigns.append(seg.pop(0))
        if seg and seg[0] == "imgen":
            return assigns, seg
    return None


def split_args(argv):
    """Flags (with values) and positionals of an imgen argv after the subcommand."""
    flags, pos, i = [], [], 2
    while i < len(argv):
        a = argv[i]
        if a.startswith("-") and a != "-":
            if "=" in a or a in BOOL_FLAGS:
                flags.append(a)
            else:
                flags.append(a + " " + (argv[i + 1] if i + 1 < len(argv) else ""))
                i += 1
        else:
            pos.append(a)
        i += 1
    return flags, pos


def flag(argv, *names):
    for i, a in enumerate(argv):
        for n in names:
            if a == n and i + 1 < len(argv):
                return argv[i + 1]
            if a.startswith(n + "="):
                return a.split("=", 1)[1]
    return None


def inputs_of(argv):
    sub = argv[1] if len(argv) > 1 else ""
    ins = [argv[i + 1] for i, a in enumerate(argv[:-1]) if a in ("-i", "--input", "--mask")]
    if sub == "upscale":
        ins += split_args(argv)[1]
    if sub == "batch":
        ins += split_args(argv)[1]
    return [x for x in ins if not re.match(r"^(https?:|clip:)", x)]


def doctor_ready():
    out = subprocess.run(["imgen", "doctor"], capture_output=True, text=True).stdout
    return set(re.findall(r"^\s+✓\s+(\S+)", out, re.M))


def provider_types(env):
    out = subprocess.run(["imgen", "providers"], capture_output=True, text=True, env=env).stdout
    return {l.split("\t")[0]: l.split("\t")[1].replace("type=", "") for l in out.splitlines() if "\t" in l}


def write_fixtures():
    from PIL import Image, ImageDraw
    w = h = 1024
    stub = Image.new("RGB", (w, h), (245, 245, 245))
    d = ImageDraw.Draw(stub)
    d.polygon([(380, 420), (560, 360), (700, 430), (520, 500)], fill=(220, 40, 40))
    d.polygon([(380, 420), (520, 500), (520, 700), (380, 620)], fill=(170, 25, 25))
    d.polygon([(520, 500), (700, 430), (700, 630), (520, 700)], fill=(120, 15, 15))
    stub.save(os.path.join(FIX, "stub.png"))
    stub.save(os.path.join(FIX, "stub.jpg"), quality=92)
    stub.save(os.path.join(FIX, "stub.jpeg"), quality=92)
    stub.save(os.path.join(FIX, "stub.webp"))
    # A plain room: wall, floor, window, and an empty space for the sofa.
    room = Image.new("RGB", (w, h), (226, 218, 200))
    d = ImageDraw.Draw(room)
    d.rectangle([0, 640, w, h], fill=(150, 110, 75))
    d.rectangle([380, 140, 650, 420], fill=(170, 210, 240), outline=(255, 255, 255), width=14)
    d.rectangle([60, 700, 200, 900], fill=(90, 70, 50))
    room.save(os.path.join(FIX, "room.png"))
    # OpenAI mask: the transparent area is the part to change.
    mask = Image.new("RGBA", (w, h), (0, 0, 0, 255))
    ImageDraw.Draw(mask).rectangle([260, 520, 860, 900], fill=(0, 0, 0, 0))
    mask.save(os.path.join(FIX, "mask.png"))
    with open(os.path.join(FIX, "in.png"), "wb") as f:
        stub.save(f, "PNG")
    with open(os.path.join(FIX, "photo.png"), "wb") as f:
        stub.save(f, "PNG")
    # Rules for the extending.md examples: the user's own rules plus the
    # documented `local` server and the repo's echo plugin.
    rules = {}
    user = os.environ.get("IMGEN_RULES") or os.path.expanduser("~/.config/imgen/rules.json")
    if os.path.exists(user):
        rules = json.load(open(user))
    json.dump(rules, open(os.path.join(FIX, "rules-user.json"), "w"), indent=1)
    rules = json.loads(json.dumps(rules))
    rules.setdefault("providers", {})
    rules["providers"]["local"] = {"type": "openai", "base_url": "http://localhost:8080/v1", "default_model": "sdxl"}
    rules["providers"]["echo"] = {"type": "exec", "command": ["python3", os.path.join(ROOT, "examples", "echo-plugin.py")]}
    json.dump(rules, open(os.path.join(FIX, "rules-extending.json"), "w"), indent=1)


def page_rules(rel):
    """Rules for one page: the user's rules plus the styles and profiles the page
    defines in its own json blocks, as a reader would add them."""
    base = json.load(open(os.path.join(FIX, "rules-extending.json" if rel == "docs/extending.md" else "rules-user.json")))
    text = open(os.path.join(ROOT, rel), encoding="utf-8").read()
    added = False
    for blk in re.findall(r"```json\n(.*?)```", text, re.S):
        try:
            obj = json.loads(blk)
        except ValueError:
            continue
        if not isinstance(obj, dict):
            continue
        for k in ("styles", "profiles"):
            if isinstance(obj.get(k), dict):
                base.setdefault(k, {}).update(obj[k])
                added = True
    if not added and rel != "docs/extending.md":
        return None
    path = os.path.join(FIX, "rules-%s.json" % re.sub(r"[^\w]+", "-", rel))
    json.dump(base, open(path, "w"), indent=1)
    return path


def dry_json(argv, cwd, env):
    """Run argv with --dry-run --json and return the parsed objects."""
    a = list(argv)
    if "--last" in a:  # a dry run cannot read the history; price it as an edit of a stub
        a[a.index("--last")] = "-i"
        a.insert(a.index("-i") + 1, "stub.png")
    a = a[:2] + ["--dry-run", "--json"] + [x for x in a[2:] if x not in ("--dry-run", "--json")]
    p = subprocess.run(a, cwd=cwd, env=env, capture_output=True, text=True, stdin=subprocess.DEVNULL)
    objs = []
    for l in p.stdout.splitlines():
        try:
            objs.append(json.loads(l))
        except ValueError:
            pass
    return p.returncode, objs, p.stderr.strip()


def requests_of(sub, objs):
    """Flatten dry-run JSON to [(job_name, provider, request, cost, known)]."""
    out = []
    for o in objs:
        if sub == "compare":
            for pr in o.get("providers", []):
                r = pr.get("result", {})
                out.append((pr.get("provider"), r.get("provider"), r.get("request", {}), r.get("cost_usd", 0), r.get("cost_known", False)))
        elif "request" in o:
            out.append((o.get("line"), o.get("provider"), o.get("request", {}), o.get("cost_usd", 0), o.get("cost_known", False)))
    return out


def plan():
    write_fixtures()
    ready = doctor_ready()
    base_env = dict(os.environ)
    types = provider_types(dict(base_env, IMGEN_RULES=os.path.join(FIX, "rules-extending.json")))
    files = ["README.md"] + sorted(glob.glob(os.path.join(ROOT, "docs", "*.md")))
    files = [os.path.relpath(os.path.join(ROOT, f), ROOT) for f in files]
    steps, seen, pages = [], {}, []
    for pi, rel in enumerate(files):
        page = "%02d-%s" % (pi, re.sub(r"[^\w]+", "-", rel).strip("-"))
        pdir = os.path.join(RUN, "pages", page)
        drydir = os.path.join(RUN, "plan", page)
        os.makedirs(os.path.join(pdir, ".smoke"), exist_ok=True)
        os.makedirs(drydir, exist_ok=True)
        rules = page_rules(rel)
        page_env = dict(base_env, IMGEN_RULES=rules) if rules else base_env
        script = ["# %s" % rel, 'cd "%s" || exit 2' % pdir, '. "%s/tools/lib.sh"' % RUN]
        if rules:
            script.append("export IMGEN_RULES=%s" % shlex.quote(rules))
        page_steps = []
        for block in blocks_of(os.path.join(ROOT, rel)):
            env_vars = {}
            for (n, kind, text, body, tag) in logical_lines(block):
                if kind == "heredoc":
                    head = home_rewrite(text)
                    m = re.match(r"^cat\s*>\s*(\S+)\s*<<", head)
                    if not m:
                        continue
                    script.append("%s\n%s\n%s" % (head, "\n".join(body), tag))
                    with open(os.path.join(drydir, m.group(1)), "w") as f:
                        f.write("\n".join(body) + "\n")
                    continue
                m = re.match(r"^(export\s+)?([A-Za-z_]\w*)=(\S.*)$", text)
                if m and "imgen" not in text:
                    try:
                        env_vars[m.group(2)] = shlex.split(m.group(3))[0]
                    except (ValueError, IndexError):
                        env_vars[m.group(2)] = m.group(3)
                    if m.group(2) != "IMGEN_BUDGET":  # the run sets its own cap
                        script.append(text)
                    continue
                text = home_rewrite(text).replace("<concept>", SAMPLE_CONCEPT)
                seg = imgen_segment(text, env_vars)
                if seg is None:
                    continue
                assigns, argv = seg
                st = {"id": len(steps), "page": page, "pdir": pdir, "loc": "%s:%d" % (rel, n),
                      "doc": text, "argv": argv, "assigns": assigns, "notes": []}
                classify(st, ready, types, drydir, page_env)
                steps.append(st)
                page_steps.append(st)
        # History only where an example reads it: --last/rerun and the step before.
        for i, st in enumerate(page_steps):
            if st["mode"] == "run" and ("--last" in st["argv"] or (len(st["argv"]) > 1 and st["argv"][1] == "rerun")):
                st["history"] = True
                for prev in reversed(page_steps[:i]):
                    if prev["mode"] in ("run", "dup"):
                        prev["history"] = True
                        if prev["mode"] == "dup":  # it must really run to land in the history
                            prev["mode"], prev["dup_of"] = "run", None
                        break
        for st in page_steps:
            script.append(step_script(st, seen))
        with open(os.path.join(RUN, "pages", page + ".sh"), "w") as f:
            f.write("\n".join(script) + "\n")
        pages.append(page)
    with open(os.path.join(RUN, "pages", "order.txt"), "w") as f:
        f.write("\n".join(pages) + "\n")
    json.dump(steps, open(os.path.join(RUN, "plan.json"), "w"), indent=1)
    with open(os.path.join(RUN, "manifest.tsv"), "w") as f:
        f.write("loc\tclass\tmode\test_usd\tcommand\n")
        for st in steps:
            f.write("%s\t%s\t%s\t%.3f\t%s\n" % (st["loc"], st["cls"], st["mode"], st.get("est", 0), " ".join(shlex.quote(a) for a in st["assigns"] + st["argv"])))
    total = sum(st.get("est", 0) for st in steps if st["mode"] == "run")
    unknown = [st["loc"] for st in steps if st["mode"] == "run" and not st.get("known", True)]
    print("\n%-34s %-16s %-6s %8s  %s" % ("WHERE", "CLASS", "MODE", "EST", "COMMAND"))
    for st in steps:
        print("%-34s %-16s %-6s %8s  %s" % (st["loc"], st["cls"], st["mode"],
              "$%.3f" % st["est"] if st["mode"] == "run" else "", " ".join(st["argv"])[:110]))
    print("\nestimated spend for paid runs: $%.3f%s" % (total, " (unknown price: %s)" % ", ".join(unknown) if unknown else ""))
    with open(os.path.join(RUN, "estimate.txt"), "w") as f:
        f.write("%.3f %d\n" % (total, len(unknown)))


def classify(st, ready, types, drydir, env):
    argv = st["argv"]
    sub = argv[1] if len(argv) > 1 else ""
    flags, pos = split_args(argv)
    st.update(sub=sub, est=0.0, known=True, reqs=[])
    bare = re.sub(r"'[^']*'|\"[^\"]*\"", "", st["doc"])
    synopsis = bool(re.search(r"\[[^\]]*\]|(?<!<)<[\w|.-]+>", bare))
    if sub == "init":
        st.update(cls="SKIP", mode="skip", reason="interactive (asks for keys)")
        return
    if synopsis:
        st.update(cls="SKIP-SYNOPSIS", mode="skip", reason="usage line, not a runnable example")
        return
    if sub in FREE_SUBS or "-h" in argv or "--help" in argv:
        st.update(cls="RUN-FREE", mode="run", reason="no API call")
        return
    if sub == "models":
        p = flag(argv, "-p", "--provider") or "gemini"
        if p == "echo" or p in ready and types.get(p) != "exec":
            st.update(cls="RUN-FREE", mode="run", reason="model listing (no image cost)")
        elif types.get(p) == "exec" or p == "local":
            st.update(cls="SKIP-LOCAL", mode="skip", reason="needs the local server or ComfyUI running")
        else:
            st.update(cls="SKIP-NOKEY", mode="skip", reason="no key for %s here; models has no --dry-run" % p)
        return
    if sub not in MAKERS:
        st.update(cls="SKIP", mode="skip", reason="unknown subcommand %r" % sub)
        return
    # Price and resolve the request with stub inputs.
    for f in inputs_of(argv):
        dst = os.path.join(drydir, f)
        if not os.path.exists(dst):
            src = os.path.join(FIX, f) if os.path.exists(os.path.join(FIX, f)) else os.path.join(FIX, "stub" + os.path.splitext(f)[1])
            if os.path.exists(src):
                subprocess.run(["cp", src, dst])
    if not os.path.exists(os.path.join(drydir, "stub.png")):
        subprocess.run(["cp", os.path.join(FIX, "stub.png"), drydir])
    denv = dict(env, XDG_STATE_HOME=os.path.join(RUN, "plan", ".state"))
    for a in st["assigns"]:
        k, v = a.split("=", 1)
        denv[k] = os.path.join(drydir, v) if not v.startswith("/") and os.path.exists(os.path.join(drydir, v)) else v
    if sub == "upscale" and "--local" in argv:
        st.update(cls="RUN-FREE", mode="run", reason="local upscale")
        return
    rc, objs, err = dry_json(argv, drydir, denv)
    st["reqs"] = requests_of(sub, objs)
    st["dry_rc"], st["dry_err"] = rc, err
    provs = [r[1] for r in st["reqs"]] or (flag(argv, "-p", "--provider") or "gemini").split(",")
    st["providers"] = provs
    if "--dry-run" in argv:
        st.update(cls="RUN-FREE", mode="run", reason="documented --dry-run")
    elif "..." in pos:
        st.update(cls="SKIP-PLACEHOLDER", mode="dry", reason='prompt is "..."; dry-run only')
    elif any(p == "local" or types.get(p) == "exec" for p in provs):
        if all(p == "echo" for p in provs):
            st.update(cls="RUN-FREE", mode="run", reason="echo test plugin, no API call")
        else:
            st.update(cls="SKIP-LOCAL", mode="dry", reason="local server/ComfyUI; dry-run only")
    elif any(p not in ready for p in provs):
        st.update(cls="SKIP-NOKEY", mode="dry", reason="no key for %s; dry-run only" % ",".join(p for p in provs if p not in ready))
    else:
        st.update(cls="RUN", mode="run", reason="")
        st["est"] = sum(r[3] or 0 for r in st["reqs"])
        st["known"] = all(r[4] for r in st["reqs"]) and rc == 0
        if rc != 0:
            st["notes"].append("dry-run failed: " + err.splitlines()[-1] if err else "dry-run failed")
    if st["cls"] == "RUN":
        key = (sub, tuple(flags), tuple(st["assigns"]))
        st["shape"] = repr(key)
    if "--remove-bg" in argv or flag(argv, "-b", "--background") == "transparent":
        st["promise_alpha"] = True


def dry_line(text, sub):
    return re.sub(r"\bimgen %s\b" % re.escape(sub), "imgen %s --dry-run" % sub, text, count=1)


def step_script(st, seen):
    sid = "s%03d" % st["id"]
    st["sid"] = sid
    st["key"] = hashlib.sha256(("%s\n%s\n%s" % (st["page"], st["doc"], st["mode"])).encode()).hexdigest()[:16]
    lines = ["", "# %s [%s]" % (st["loc"], st["cls"])]
    if st["mode"] == "skip":
        lines.append("# skipped: %s" % st["reason"])
        return "\n".join(lines)
    text = st["doc"]
    if st["mode"] == "dry":
        text = dry_line(text, st["sub"])
    ins = [f for f in inputs_of(st["argv"]) if "/" not in f]
    if st["cls"] == "RUN" and st.get("shape") in seen and not st.get("history"):
        orig = seen[st["shape"]]
        st["mode"], st["dup_of"] = "dup", orig["loc"]
        lines.append("_step %s; _dup %s %s" % (sid, shlex.quote(orig["pdir"]), orig["sid"]))
        return "\n".join(lines)
    if st["cls"] == "RUN":
        seen.setdefault(st["shape"], st)
    hist = "unset IMGEN_HISTORY; " if st.get("history") else ""
    if st["cls"] != "RUN":  # free steps: a local plugin has no price, which a budget refuses
        hist += "unset IMGEN_BUDGET; "
    if ins:
        lines.append("_need %s" % " ".join(shlex.quote(f) for f in ins))
    lines.append("_step %s" % sid)
    lines.append("if ! _reuse %s; then" % st["key"])
    lines.append("  ( %s%s\n  ) </dev/null >.smoke/%s.out 2>.smoke/%s.err; _done $?" % (hist, text, sid, sid))
    lines.append("fi")
    return "\n".join(lines)


# ---------- check ----------

def img_info(path):
    from PIL import Image
    if path.endswith(".svg"):
        data = open(path, "rb").read(4096)
        return {"format": "SVG", "ok": b"<svg" in data}
    with Image.open(path) as im:
        im.verify()
    with Image.open(path) as im:
        im.load()
        info = {"format": im.format, "size": im.size, "mode": im.mode, "ok": True}
        if im.mode in ("RGBA", "LA", "PA") or (im.mode == "P" and "transparency" in im.info):
            a = im.convert("RGBA").getchannel("A").getextrema()
            info["alpha"] = a
        return info


def review_copy(path, tag):
    from PIL import Image
    if path.endswith(".svg"):
        return
    with Image.open(path) as im:
        im = im.convert("RGBA")
        im.thumbnail((1024, 1024))
        bg = Image.new("RGBA", im.size, (255, 0, 255, 255))
        cs = 32
        for y in range(0, im.size[1], cs):
            for x in range(0, im.size[0], cs):
                if (x // cs + y // cs) % 2:
                    bg.paste((200, 200, 200, 255), (x, y, x + cs, y + cs))
                else:
                    bg.paste((255, 255, 255, 255), (x, y, x + cs, y + cs))
        bg.alpha_composite(im)
        bg.convert("RGB").save(os.path.join(RUN, "review", tag + ".jpg"), quality=85)


def read(path, default=""):
    try:
        return open(path, encoding="utf-8", errors="replace").read()
    except OSError:
        return default


def check_image(path, req, fails, want_alpha):
    try:
        info = img_info(path)
    except Exception as e:  # noqa: BLE001 - any decode error is a failure
        fails.append("%s does not decode: %s" % (os.path.basename(path), e))
        return None
    name = os.path.basename(path)
    p = (req or {}).get("params", {}) or {}
    post = (req or {}).get("post", {}) or {}
    fmt = (p.get("format") or "").lower()
    if post.get("remove_bg"):
        fmt = fmt or "png"
    want = {"png": "PNG", "jpeg": "JPEG", "jpg": "JPEG", "webp": "WEBP", "svg": "SVG"}.get(fmt)
    if want and info["format"] != want:
        fails.append("%s is %s, asked for %s" % (name, info["format"], want))
    if info["format"] == "SVG":
        return info
    w, h = info["size"]
    rs = re.match(r"^(\d+)x(\d+)", post.get("resize") or "")
    sz = re.match(r"^(\d+)x(\d+)$", p.get("size") or "")
    ar = re.match(r"^(\d+(?:\.\d+)?):(\d+(?:\.\d+)?)$", p.get("aspect_ratio") or "")
    if post.get("crop"):
        pass
    elif rs:
        if (w, h) != (int(rs.group(1)), int(rs.group(2))):
            fails.append("%s is %dx%d, --resize asked for %sx%s" % (name, w, h, rs.group(1), rs.group(2)))
    elif sz:
        if (w, h) != (int(sz.group(1)), int(sz.group(2))):
            fails.append("%s is %dx%d, -r asked for %sx%s" % (name, w, h, sz.group(1), sz.group(2)))
    elif ar:
        want_r = float(ar.group(1)) / float(ar.group(2))
        if abs((w / h) / want_r - 1) > 0.02:
            fails.append("%s is %dx%d (%.3f), -a asked for %s (%.3f)" % (name, w, h, w / h, p["aspect_ratio"], want_r))
    if want_alpha or (p.get("background") == "transparent") or post.get("remove_bg"):
        a = info.get("alpha")
        if not a:
            fails.append("%s has no alpha channel (mode %s)" % (name, info["mode"]))
        elif a[0] != 0:
            fails.append("%s has alpha but no transparent pixel (min alpha %d)" % (name, a[0]))
    return info


def check():
    steps = json.load(open(os.path.join(RUN, "plan.json")))
    rows, reuse = [], []
    for st in steps:
        sid, pdir = "s%03d" % st["id"], st["pdir"]
        sm = os.path.join(pdir, ".smoke", sid)
        status, reason, files = "SKIP", st.get("reason", ""), []
        if st["mode"] == "skip":
            rows.append((st, "SKIP", reason, files))
            continue
        if not os.path.exists(sm + ".outputs"):
            rows.append((st, "FAIL", "step did not run", files))
            continue
        files = [f for f in read(sm + ".outputs").splitlines() if f]
        if st["mode"] == "dup":
            rows.append((st, "SKIP", "same command shape as %s; ran once there" % st.get("dup_of"), files))
            continue
        rc = read(sm + ".rc", "?").strip()
        out, err = read(sm + ".out"), read(sm + ".err")
        fails = []
        if rc != "0":
            last = [l for l in err.splitlines() if l.strip()][-3:]
            fails.append("exit %s: %s" % (rc, " | ".join(last)[:400]))
        stubbed = [f for f in read(sm + ".stubbed").splitlines() if f]
        for f in stubbed:
            if f not in PLACEHOLDER_INPUTS:
                fails.append("input %s was never produced by an earlier example (a stub was used)" % f)
        if st["mode"] == "dry":
            if rc == "0" and "dry run" not in out:
                fails.append("no dry-run report on stdout")
            rows.append((st, "FAIL" if fails else "SKIP", "; ".join(fails) if fails else st["reason"] + " (dry-run OK)", files))
            continue
        sub, argv = st["sub"], st["argv"]
        if rc == "0" and sub in MAKERS and "--dry-run" not in argv:
            imgs = [f for f in files if f.lower().endswith(IMG_EXT)]
            sheets = [f for f in imgs if f.endswith("-grid.png") or os.path.basename(f) in ("compare-sheet.png", "_contact-sheet.png")]
            main = [f for f in imgs if f not in sheets]
            if not main:
                fails.append("no image file was written")
            name = flag(argv, "--name")
            reqs = st.get("reqs") or []
            n = int(flag(argv, "-n", "--count") or 1)
            if sub in ("gen", "edit") and len(main) < n:
                fails.append("asked for %d images, got %d" % (n, len(main)))
            if name and sub in ("gen", "edit"):
                want = [name] if n == 1 else ["%s-%d" % (name, k) for k in range(1, n + 1)]
                for w in want:
                    if not any(os.path.splitext(os.path.basename(f))[0] == w for f in main):
                        fails.append("expected %s.<ext>, got %s" % (w, ", ".join(main) or "nothing"))
            if "--grid" in argv and sub in ("gen", "edit") and n > 1:
                g = (name or "") + "-grid.png"
                if name and g not in [os.path.basename(f) for f in sheets]:
                    fails.append("--grid promised %s" % g)
                elif not sheets:
                    fails.append("--grid wrote no sheet")
            if sub == "compare":
                if "compare-sheet.png" not in sheets:
                    fails.append("no compare-sheet.png")
                for r in reqs:
                    if name and not any(os.path.splitext(f)[0] == "%s-%s" % (name, r[0]) for f in main):
                        fails.append("expected %s-%s.<ext>" % (name, r[0]))
            if sub == "batch" and "--grid" in argv and "_contact-sheet.png" not in sheets:
                fails.append("--grid promised _contact-sheet.png")
            m = re.search(r"#\s*/path/to/(\S+)", st["doc"])
            if m and m.group(1) not in main:
                fails.append("doc shows %s, got %s" % (m.group(1), ", ".join(main)))
            for f in main:
                req = reqs[0][2] if reqs else {}
                if sub == "compare":
                    req = next((r[2] for r in reqs if name and f.startswith("%s-%s" % (name, r[0]))), {})
                if sub == "batch":
                    jobs = [json.loads(l) for l in read(os.path.join(pdir, flag_pos_file(argv))).splitlines() if l.strip()]
                    req = {}
                    for k, j in enumerate(jobs):
                        if os.path.splitext(f)[0].startswith(j.get("name", "\0")):
                            req = next((r[2] for r in reqs if r[0] == k + 1), {})
                check_image(os.path.join(pdir, f), req, fails, st.get("promise_alpha"))
            for f in sheets:
                check_image(os.path.join(pdir, f), {}, fails, False)
        # Every path printed on stdout must exist.
        for l in out.splitlines():
            cand = l.split("\t")[-1].strip()
            if cand.lower().endswith(IMG_EXT) and not cand.startswith("{"):
                p = cand if os.path.isabs(cand) else os.path.join(pdir, cand)
                if not os.path.exists(p):
                    fails.append("stdout names %s, which does not exist" % cand)
        if sub == "completion" and rc == "0":
            for f in files:
                if os.path.getsize(os.path.join(pdir, f)) == 0:
                    fails.append("%s is empty" % f)
        if sub == "mcp" and rc == "0" and '"tools"' not in out:
            fails.append("tools/list reply missing")
        for f in files:
            if f.lower().endswith(IMG_EXT):
                try:
                    review_copy(os.path.join(pdir, f), "%s-%s-%s" % (st["page"][:2], sid, os.path.splitext(f)[0]))
                except Exception:  # noqa: BLE001 - review copies are best effort
                    pass
        status = "FAIL" if fails else "PASS"
        why = "; ".join(fails) if fails else (st.get("reason") or "ok")
        if os.path.exists(sm + ".reused"):
            why += " (reused from %s)" % read(sm + ".reused").split()[0]
        rows.append((st, status, why, files))
        if status == "PASS":
            reuse.append("%s\t%s\t%s" % (st["key"], pdir, sid))
    with open(os.path.join(RUN, "reuse-index.tsv"), "w") as f:
        f.write("\n".join(reuse) + "\n")
    counts = {}
    with open(os.path.join(RUN, "summary.md"), "w") as f:
        f.write("# imgen docs smoke test\n\n%s\n\n| # | Where | Class | Result | Command | Reason | Files |\n|---|---|---|---|---|---|---|\n" % read(os.path.join(RUN, "version.txt")).strip())
        for st, status, why, files in rows:
            counts[status] = counts.get(status, 0) + 1
            cmd = " ".join(shlex.quote(a) for a in st["assigns"] + st["argv"]).replace("|", "\\|")
            f.write("| %d | %s | %s | %s | `%s` | %s | %s |\n" % (st["id"], st["loc"], st["cls"], status, cmd[:160], why.replace("|", "\\|"), ", ".join(files)[:200]))
        f.write("\n%s\n" % "  ".join("%s %d" % kv for kv in sorted(counts.items())))
    for st, status, why, files in rows:
        print("%-4s %-30s %-16s %s" % (status, st["loc"], st["cls"], why[:150]))
    print("\n" + "  ".join("%s %d" % kv for kv in sorted(counts.items())))
    print("summary: %s" % os.path.join(RUN, "summary.md"))
    sys.exit(1 if counts.get("FAIL") else 0)


def flag_pos_file(argv):
    return split_args(argv)[1][-1]


if __name__ == "__main__":
    {"plan": plan, "check": check}[sys.argv[1]]()
PY

echo "== plan: extract, classify, price (dry runs only)"
python3 -I "$RUN/tools/smoke.py" plan | tee "$RUN/plan.txt"
read -r EST UNKNOWN <"$RUN/estimate.txt"

if [ "$UNKNOWN" != "0" ]; then
	echo "smoke-docs: $UNKNOWN paid step(s) have no price; refusing to run (see $RUN/manifest.tsv)" >&2
	exit 3
fi
if awk -v e="$EST" -v m="$MAX_USD" 'BEGIN { exit !(e > m) }'; then
	echo "smoke-docs: estimate \$$EST is over the \$$MAX_USD cap; nothing was run" >&2
	exit 3
fi
if [ "$ESTIMATE" = 1 ]; then
	echo "estimate only: \$$EST (cap \$$MAX_USD). Run without --estimate to spend it."
	exit 0
fi

echo "== run: \$$EST estimated, cap \$$MAX_USD"
# Pages are independent (each chains only its own commands), so they run at
# once; the budget ledger is shared and locked, so the cap still holds.
start=$SECONDS
while IFS= read -r page; do
	[ -n "$page" ] || continue
	(bash "$RUN/pages/$page.sh" >"$RUN/pages/$page.log" 2>&1 || echo "smoke-docs: page script $page exited $?" >&2
		echo "-- $page done ($((SECONDS - start))s)") &
done <"$RUN/pages/order.txt"
wait

echo "== spend (this run's isolated history and budget ledger)"
imgen history --cost --since 1d | tee "$RUN/spend.txt" || true
if [ -f "$RUN/state/imgen/budget.jsonl" ]; then
	jq -s 'map(.cost_usd // 0) | add // 0 | "ledger (history off): $\(. * 1000 | round / 1000)"' -r \
		"$RUN/state/imgen/budget.jsonl" | tee -a "$RUN/spend.txt"
fi

echo "== check"
python3 -I "$RUN/tools/smoke.py" check
