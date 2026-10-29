// Tests of the site check: a sound page passes, and each kind of problem is
// reported.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sound := `<html lang="en"><head><title>x</title><meta name="viewport" content="w"><meta name="description" content="d"></head>` +
		`<body><a href="#top">t</a><a href="https://example.com/">e</a><img src="images/a.svg"><a href="images/a.svg#x">f</a></body></html>`
	write("index.html", sound)
	write("images/a.svg", "<svg/>")
	if problems, err := check(dir); err != nil || len(problems) != 0 {
		t.Fatalf("a sound site: %v, %v", problems, err)
	}

	write("index.html", `<html><head></head><body><a href="http://example.com/">p</a><img src="missing.png"><a href="/docs/">abs</a></body></html>`)
	problems, err := check(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lacks a <title>", "lacks a lang", "lacks a viewport", "lacks a description", "plain HTTP link", "not in " + dir + ": missing.png", "absolute path"} {
		if !strings.Contains(strings.Join(problems, "\n"), want) {
			t.Errorf("problems %q lack %q", problems, want)
		}
	}
	if _, err := check(filepath.Join(dir, "nowhere")); err == nil {
		t.Error("a missing site passed")
	}
}
