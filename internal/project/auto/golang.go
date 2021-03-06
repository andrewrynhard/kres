// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package auto

import (
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/talos-systems/kres/internal/dag"
	"github.com/talos-systems/kres/internal/project/common"
	"github.com/talos-systems/kres/internal/project/golang"
	"github.com/talos-systems/kres/internal/project/meta"
	"github.com/talos-systems/kres/internal/project/service"
	"github.com/talos-systems/kres/internal/project/wrap"
)

// DetectGolang check if project at rootPath is Go-based project.
//
//nolint: gocognit,gocyclo
func DetectGolang(rootPath string, options *meta.Options) (bool, error) {
	gomodPath := filepath.Join(rootPath, "go.mod")

	gomod, err := os.Open(gomodPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, err
	}

	defer gomod.Close() //nolint: errcheck

	contents, err := ioutil.ReadAll(gomod)
	if err != nil {
		return true, err
	}

	options.CanonicalPath = modfile.ModulePath(contents)

	for _, srcDir := range []string{"src", "internal", "pkg", "cmd"} {
		exists, err := directoryExists(rootPath, srcDir)
		if err != nil {
			return true, err
		}

		if exists {
			options.Directories = append(options.Directories, srcDir)
			options.GoDirectories = append(options.GoDirectories, srcDir)
		}
	}

	if len(options.GoDirectories) == 0 {
		// no standard directories found, assume any directory with `.go` files is a source directory
		topLevel, err := ioutil.ReadDir(rootPath)
		if err != nil {
			return true, err
		}

		for _, item := range topLevel {
			if !item.IsDir() {
				continue
			}

			result, err := hasGoFiles(filepath.Join(rootPath, item.Name()))
			if err != nil {
				return true, err
			}

			if result {
				options.Directories = append(options.Directories, item.Name())
				options.GoDirectories = append(options.GoDirectories, item.Name())
			}
		}
	}

	{
		res, err := hasGoFiles(rootPath)
		if err != nil {
			return true, err
		}

		if res {
			contents, err := ioutil.ReadDir(rootPath)
			if err != nil {
				return true, err
			}

			for _, item := range contents {
				if !item.IsDir() && strings.HasSuffix(item.Name(), ".go") {
					options.SourceFiles = append(options.SourceFiles, item.Name())
					options.GoSourceFiles = append(options.GoSourceFiles, item.Name())
				}
			}
		}
	}

	options.SourceFiles = append(options.SourceFiles, "go.mod", "go.sum")

	for _, candidate := range []string{"pkg/version", "internal/version"} {
		exists, err := directoryExists(rootPath, candidate)
		if err != nil {
			return true, err
		}

		if exists {
			options.VersionPackage = path.Join(options.CanonicalPath, candidate)
		}
	}

	{
		cmdExists, err := directoryExists(rootPath, "cmd")
		if err != nil {
			return true, err
		}

		if cmdExists {
			dirs, err := ioutil.ReadDir(filepath.Join(rootPath, "cmd"))
			if err != nil {
				return true, err
			}

			for _, dir := range dirs {
				if dir.IsDir() {
					options.Commands = append(options.Commands, dir.Name())
				}
			}
		}
	}

	return true, nil
}

// BuildGolang builds project structure for Go project.
func BuildGolang(meta *meta.Options, inputs []dag.Node) ([]dag.Node, error) {
	// toolchain as the root of the tree
	toolchain := golang.NewToolchain(meta)
	toolchain.AddInput(inputs...)

	// linters
	golangciLint := golang.NewGolangciLint(meta)
	gofumpt := golang.NewGofumpt(meta)

	// linters are input to the toolchain as they inject into toolchain build
	toolchain.AddInput(golangciLint, gofumpt)

	// common lint target
	lint := common.NewLint(meta)
	lint.AddInput(toolchain, golangciLint, gofumpt)

	// unit-tests
	unitTests := golang.NewUnitTests(meta)
	unitTests.AddInput(toolchain)

	coverage := service.NewCodeCov(meta)
	coverage.InputPath = "coverage.txt"
	coverage.AddInput(unitTests)

	outputs := []dag.Node{lint, unitTests, coverage}

	// process commands
	for _, cmd := range meta.Commands {
		build := golang.NewBuild(meta, cmd, filepath.Join("cmd", cmd))
		build.AddInput(toolchain)

		image := common.NewImage(meta, cmd)
		image.AddInput(build, common.NewFHS(meta), common.NewCACerts(meta), lint, wrap.Drone(unitTests))

		outputs = append(outputs, build, image)
	}

	return outputs, nil
}

func hasGoFiles(path string) (bool, error) {
	contents, err := ioutil.ReadDir(path)
	if err != nil {
		return false, err
	}

	for _, item := range contents {
		if !item.IsDir() && strings.HasSuffix(item.Name(), ".go") {
			return true, nil
		}
	}

	return false, nil
}
