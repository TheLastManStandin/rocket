package botpack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsNamesAndAvatars(t *testing.T) {
	dir := t.TempDir()

	names := filepath.Join(dir, "names.txt")
	const text = "# a comment\n\n  Мурка  \nzerocool\n\n# another\nМурка\npixel\n"
	if err := os.WriteFile(names, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}

	avatars := filepath.Join(dir, "avatars")
	if err := os.Mkdir(avatars, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"b.png", "a.JPG", "notes.txt", "README.md", "c.svg"} {
		if err := os.WriteFile(filepath.Join(avatars, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	pack, err := Load(names, avatars, URLPrefix)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Trimmed, comments and blanks dropped, and no name twice: the same
	// nickname in two rows of one round reads as a bug.
	want := []string{"Мурка", "zerocool", "pixel"}
	if len(pack.Names) != len(want) {
		t.Fatalf("read %v, want %v", pack.Names, want)
	}
	for i, name := range want {
		if pack.Names[i] != name {
			t.Errorf("name %d is %q, want %q", i, pack.Names[i], name)
		}
	}

	// Only pictures, as URLs, in an order the filesystem cannot shuffle.
	wantURLs := []string{"/bot-avatars/a.JPG", "/bot-avatars/b.png", "/bot-avatars/c.svg"}
	if len(pack.Avatars) != len(wantURLs) {
		t.Fatalf("listed %v, want %v", pack.Avatars, wantURLs)
	}
	for i, url := range wantURLs {
		if pack.Avatars[i] != url {
			t.Errorf("avatar %d is %q, want %q", i, pack.Avatars[i], url)
		}
	}
}

// A fresh checkout has neither file, and that is not an error.
func TestLoadIsHappyWithNothingThere(t *testing.T) {
	dir := t.TempDir()

	pack, err := Load(filepath.Join(dir, "nope.txt"), filepath.Join(dir, "nope"), URLPrefix)
	if err != nil {
		t.Fatalf("Load on an empty checkout: %v", err)
	}
	if len(pack.Names) != 0 || len(pack.Avatars) != 0 {
		t.Errorf("got %+v, want nothing", pack)
	}
}
