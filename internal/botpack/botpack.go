// Package botpack loads what the crowd under the curve is dealt from: the
// names the fake players wear and the avatars they wear them with.
//
// Both are files on disk rather than lists in the source, so changing the crowd
// is dropping a picture in a folder or editing a line -- no rebuild. Both are
// read once, at start-up: the engine must never touch a disk mid-round.
package botpack

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// URLPrefix is where the avatar folder is served from. The loader builds URLs
// under it and the router hangs the folder off it, so the two cannot drift.
const URLPrefix = "/bot-avatars"

// Pack is the crowd's wardrobe. Either half may come back empty, and the game
// is expected to cope: no names falls back to the built-in list, no avatars
// leaves every bot with the letter-on-a-disc it has always had.
type Pack struct {
	Names   []string
	Avatars []string
}

// pictures are what a browser will actually draw. Anything else in the folder
// -- a README, a stray .txt, a hidden file the OS left behind -- is not an
// avatar and must not be handed out as one.
var pictures = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".svg": true,
}

// Load reads the names file and lists the avatar folder. A missing file or
// folder is not an error: it is the ordinary state of a fresh checkout, and the
// game runs perfectly well without either.
//
// urlPrefix is where the avatar folder is served from, and the returned avatar
// entries are URLs under it rather than paths on disk.
func Load(namesFile, avatarDir, urlPrefix string) (Pack, error) {
	names, err := readNames(namesFile)
	if err != nil {
		return Pack{}, err
	}
	avatars, err := listAvatars(avatarDir, urlPrefix)
	if err != nil {
		return Pack{Names: names}, err
	}
	return Pack{Names: names, Avatars: avatars}, nil
}

func readNames(file string) ([]string, error) {
	f, err := os.Open(file)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("botpack: opening %s: %w", file, err)
	}
	defer f.Close() //nolint:errcheck // read-only

	var names []string
	seen := map[string]bool{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		name := strings.TrimSpace(scan.Text())
		// A duplicate name would let the same player sit down twice in one
		// round, which reads as a bug however honestly it got there.
		if name == "" || strings.HasPrefix(name, "#") || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("botpack: reading %s: %w", file, err)
	}
	return names, nil
}

func listAvatars(dir, urlPrefix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("botpack: reading %s: %w", dir, err)
	}

	var urls []string
	for _, e := range entries {
		if e.IsDir() || !pictures[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		urls = append(urls, path.Join(urlPrefix, e.Name()))
	}
	// Sorted, so which bot wears which is decided by the name and not by
	// whatever order the filesystem happened to hand the folder back in.
	sort.Strings(urls)
	return urls, nil
}
