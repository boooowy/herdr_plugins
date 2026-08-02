package main

import (
	"os"
	"regexp"
	"testing"
)

func TestBinaryAndManifestVersionsMatch(t *testing.T) {
	data, err := os.ReadFile("herdr-plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version = "([^"]+)"$`).FindSubmatch(data)
	if len(m) != 2 {
		t.Fatal("manifest version not found")
	}
	if string(m[1]) != version {
		t.Errorf("manifest=%s binary=%s", m[1], version)
	}
}
