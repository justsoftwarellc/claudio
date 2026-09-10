// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package portdetect

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestDetectNextJS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "next.config.js", "module.exports = {}")

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].ServiceName != "web" || got[0].Container != 3000 || got[0].From != "next.config.js" {
		t.Fatalf("Detect = %+v, want single web:3000 from next.config.js", got)
	}
}

func TestDetectVite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "vite.config.ts", "export default {}")

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].Container != 5173 {
		t.Fatalf("Detect = %+v, want web:5173", got)
	}
}

func TestDetectReactScripts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"dependencies": {"react-scripts": "5.0.1"}}`)

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].Container != 3000 || got[0].From != "package.json" {
		t.Fatalf("Detect = %+v, want web:3000 from package.json", got)
	}
}

func TestDetectReactScriptsInDevDependencies(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"devDependencies": {"react-scripts": "5.0.1"}}`)

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Detect = %+v, want react-scripts detected from devDependencies too", got)
	}
}

func TestDetectDjango(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manage.py", "#!/usr/bin/env python")

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].Container != 8000 {
		t.Fatalf("Detect = %+v, want web:8000", got)
	}
}

func TestDetectRails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Gemfile", "source 'https://rubygems.org'")
	writeFile(t, dir, "config.ru", "run Rails.application")

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].Container != 3000 {
		t.Fatalf("Detect = %+v, want web:3000", got)
	}
}

func TestDetectRailsRequiresBothFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Gemfile", "source 'https://rubygems.org'")
	// no config.ru

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Detect = %+v, want no match without config.ru", got)
	}
}

func TestDetectGoModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/api\n\ngo 1.25\n")

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].ServiceName != "api" || got[0].Container != 8080 {
		t.Fatalf("Detect = %+v, want api:8080", got)
	}
}

func TestDetectMultipleSignals(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/api\n")
	writeFile(t, dir, "vite.config.js", "export default {}")

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Detect = %+v, want both go.mod and vite signals", got)
	}
}

func TestDetectNoSignals(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "hello")

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Detect = %+v, want no signals", got)
	}
}

func TestDetectMalformedPackageJSONIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{not valid json`)

	got, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Detect = %+v, want no match on malformed package.json", got)
	}
}
