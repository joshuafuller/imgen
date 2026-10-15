package mcpserver

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// png1x1 is a valid 1x1 PNG.
var png1x1 = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func guardRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	old := workspaceRoot
	workspaceRoot = root
	t.Cleanup(func() { workspaceRoot = old })
	return root
}

func argsMap(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestGuardRejectsInputOutsideRoot(t *testing.T) {
	guardRoot(t)
	outside := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(outside, []byte("-----BEGIN KEY-----"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := argsMap(t, `{"prompt":"x","images":[`+jsonStr(outside)+`]}`)
	if _, err := testServer().genArgs("edit", m, true); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("want outside-root error, got %v", err)
	}
}

func TestGuardRejectsNonImageInsideRoot(t *testing.T) {
	root := guardRoot(t)
	p := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(p, []byte("secret text"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := argsMap(t, `{"prompt":"x","images":[`+jsonStr(p)+`]}`)
	if _, err := testServer().genArgs("edit", m, true); err == nil || !strings.Contains(err.Error(), "not an image") {
		t.Fatalf("want not-an-image error, got %v", err)
	}
}

func TestGuardRejectsSymlinkEscape(t *testing.T) {
	root := guardRoot(t)
	target := filepath.Join(t.TempDir(), "x.png")
	if err := os.WriteFile(target, png1x1, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.png")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unsupported")
	}
	m := argsMap(t, `{"prompt":"x","images":[`+jsonStr(link)+`]}`)
	if _, err := testServer().genArgs("edit", m, true); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestGuardAcceptsImageInsideRoot(t *testing.T) {
	root := guardRoot(t)
	p := filepath.Join(root, "in.png")
	if err := os.WriteFile(p, png1x1, 0o600); err != nil {
		t.Fatal(err)
	}
	m := argsMap(t, `{"prompt":"x","images":["in.png"],"mask":"in.png","output_dir":"out"}`)
	args, err := testServer().genArgs("edit", m, true)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-i "+p) || !strings.Contains(joined, "--mask "+p) ||
		!strings.Contains(joined, "-o "+filepath.Join(root, "out")) {
		t.Fatalf("args %v", args)
	}
}

func TestGuardRejectsMaskOutsideRoot(t *testing.T) {
	root := guardRoot(t)
	if err := os.WriteFile(filepath.Join(root, "in.png"), png1x1, 0o600); err != nil {
		t.Fatal(err)
	}
	m := argsMap(t, `{"prompt":"x","images":["in.png"],"mask":"/etc/passwd"}`)
	if _, err := testServer().genArgs("edit", m, true); err == nil {
		t.Fatal("mask outside root accepted")
	}
}

func TestGuardRejectsOutputOutsideRoot(t *testing.T) {
	guardRoot(t)
	for _, dir := range []string{"/tmp/elsewhere", "../up", "/"} {
		m := argsMap(t, `{"prompt":"x","output_dir":`+jsonStr(dir)+`}`)
		if _, err := testServer().genArgs("gen", m, false); err == nil {
			t.Errorf("output_dir %q accepted", dir)
		}
	}
}

func TestGuardRejectsBadNames(t *testing.T) {
	guardRoot(t)
	for _, n := range []string{"../x", "a/b", `a\b`, "..", ".hidden"} {
		m := argsMap(t, `{"prompt":"x","name":`+jsonStr(n)+`}`)
		if _, err := testServer().genArgs("gen", m, false); err == nil {
			t.Errorf("name %q accepted", n)
		}
	}
}

func TestGuardDefaultOutputIsRoot(t *testing.T) {
	root := guardRoot(t)
	args, err := testServer().genArgs("gen", argsMap(t, `{"prompt":"x"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "-o "+root) {
		t.Fatalf("args %v", args)
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestInitRootDefaultsToWorkDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("IMGEN_MCP_ROOT", "")
	old := workspaceRoot
	workspaceRoot = ""
	t.Cleanup(func() { workspaceRoot = old })
	if err := initRoot(); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(home, "imgen-work"))
	if workspaceRoot != want {
		t.Fatalf("root %q want %q", workspaceRoot, want)
	}
	if st, err := os.Stat(want); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("work dir not created private: %v %v", st, err)
	}
}

func TestInitRootRefusesHomeAndSlash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, r := range []string{"/", home} {
		t.Setenv("IMGEN_MCP_ROOT", r)
		old := workspaceRoot
		workspaceRoot = ""
		if err := initRoot(); err == nil {
			t.Errorf("root %q accepted", r)
		}
		workspaceRoot = old
	}
}

func TestGuardRejectsDotDirsInsideRoot(t *testing.T) {
	root := guardRoot(t)
	hidden := filepath.Join(root, ".secret")
	if err := os.MkdirAll(hidden, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "a.png"), png1x1, 0o600); err != nil {
		t.Fatal(err)
	}
	m := argsMap(t, `{"prompt":"x","images":[".secret/a.png"]}`)
	if _, err := testServer().genArgs("edit", m, true); err == nil {
		t.Error("input in dot-dir accepted")
	}
	m = argsMap(t, `{"prompt":"x","output_dir":".config/x"}`)
	if _, err := testServer().genArgs("gen", m, false); err == nil {
		t.Error("output in dot-dir accepted")
	}
}

func TestGuardRejectsDimensionBombInput(t *testing.T) {
	root := guardRoot(t)
	p := filepath.Join(root, "bomb.png")
	if err := os.WriteFile(p, bombPNGFile(10000, 10000), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{"prompt":"x","images":["bomb.png"]}`, `{"prompt":"x","images":["in.png"],"mask":"bomb.png"}`} {
		if err := os.WriteFile(filepath.Join(root, "in.png"), png1x1, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := testServer().genArgs("edit", argsMap(t, args), true); err == nil {
			t.Errorf("bomb accepted: %s", args)
		}
	}
}

// bombPNGFile is a PNG signature plus an IHDR declaring w x h, with no pixel data.
func bombPNGFile(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	body := append([]byte("IHDR"), ihdr...)
	b.Write(body)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(body))
	return b.Bytes()
}
