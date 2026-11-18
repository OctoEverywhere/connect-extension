//go:build ignore

// Run in the pinned core module, which supplies its normal YAML decoder:
// go -C .core run ../scripts/check-manifest.go ..
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prusa3d/prusa-connect/sdk/manifest"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 2 {
		fail("provide the extension repository path")
	}
	root, err := filepath.Abs(os.Args[1])
	if err != nil {
		fail(err.Error())
	}
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			fail(err.Error())
		}
		return data
	}
	held, err := manifest.Parse("module.yaml", read("module.yaml"), yaml.Unmarshal)
	if err != nil {
		fail(err.Error())
	}
	if _, err = routesapi.ParseFragment(read(held.OpenAPI)); err != nil {
		fail(err.Error())
	}
	var document struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err = json.Unmarshal(read(held.OpenAPI), &document); err != nil {
		fail(err.Error())
	}
	if document.Info.Version != held.Version {
		fail("manifest and OpenAPI release versions differ")
	}
	if !strings.Contains(string(read("CHANGELOG.md")), "## "+held.Version+" ") {
		fail("changelog does not describe the manifest version")
	}
	if held.Name != "octoeverywhere" || held.UI == nil {
		fail("manifest must contain the OctoEverywhere process and page")
	}
	if held.Needs.MemoryMiB != 128 || !held.Needs.Data || held.Needs.Database || len(held.Needs.Listen) != 0 {
		fail("manifest process budget or storage grants differ from the design")
	}
	fmt.Printf("Manifest, OpenAPI, and changelog agree: %s %s\n", held.Name, held.Version)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
