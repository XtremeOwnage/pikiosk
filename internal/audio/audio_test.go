package audio

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAndLibrary(t *testing.T) {
	user, builtin := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(builtin, "chime.ogg"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(builtin, "notes.txt"), []byte("x"), 0o644)
	p := New("mpv", t.TempDir(), user, []string{builtin}, slog.New(slog.DiscardHandler))

	if _, err := p.Save("Door Bell.mp3", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Save("evil.sh", strings.NewReader("x")); err == nil {
		t.Error("non-audio upload accepted")
	}
	names := p.Names()
	if strings.Join(names, ",") != "Door_Bell,chime" {
		t.Errorf("library: %v", names)
	}

	ok := map[string]string{
		"chime":                              filepath.Join(builtin, "chime.ogg"),
		"Door_Bell":                          filepath.Join(user, "Door_Bell.mp3"),
		filepath.Join(builtin, "chime.ogg"):  filepath.Join(builtin, "chime.ogg"),
		"https://ha.lan/api/tts_proxy/x.mp3": "https://ha.lan/api/tts_proxy/x.mp3",
	}
	for in, want := range ok {
		if got, err := p.Resolve(in); err != nil || got != want {
			t.Errorf("%q: got %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"/etc/passwd", filepath.Join(builtin, "..", "x.ogg"), "file:///etc/passwd", "../../etc/passwd", "missing", ""} {
		if got, err := p.Resolve(bad); err == nil {
			t.Errorf("%q resolved to %q", bad, got)
		}
	}

	if err := p.Delete("chime"); err == nil {
		t.Error("deleted a built-in sound")
	}
	if err := p.Delete("Door_Bell"); err != nil {
		t.Error(err)
	}
}
