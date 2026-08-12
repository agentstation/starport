// Package architecture contains executable dependency rules for Starport.
package architecture

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImportGraphArchitecture(t *testing.T) {
	packages := listPackages(t,
		"../routing",
		"../catalog",
		"../execution",
		"../availability",
		"../inference",
		"../failure",
		"../identity",
		"../credentials",
		"../ratelimit",
		"../presets",
		"../responsecache",
		"../protocol/openai",
		"../protocol/openrouter",
	)
	for _, packagePath := range []string{
		"github.com/agentstation/starport/internal/routing",
		"github.com/agentstation/starport/internal/catalog",
		"github.com/agentstation/starport/internal/execution",
		"github.com/agentstation/starport/internal/availability",
		"github.com/agentstation/starport/internal/inference",
		"github.com/agentstation/starport/internal/failure",
		"github.com/agentstation/starport/internal/identity",
		"github.com/agentstation/starport/internal/credentials",
		"github.com/agentstation/starport/internal/ratelimit",
		"github.com/agentstation/starport/internal/presets",
		"github.com/agentstation/starport/internal/responsecache",
		"github.com/agentstation/starport/internal/protocol/openai",
		"github.com/agentstation/starport/internal/protocol/openrouter",
	} {
		require.Containsf(t, packages, packagePath, "required package %s is absent from the import graph", packagePath)
	}

	assertNoImports(t, packages["github.com/agentstation/starport/internal/routing"],
		"github.com/agentstation/starport/internal/",
		"net/http",
	)
	assertNoImports(t, packages["github.com/agentstation/starport/internal/catalog"],
		"github.com/agentstation/starport/internal/providers",
		"github.com/agentstation/starport/internal/proxy",
		"github.com/agentstation/starport/internal/registry",
		"github.com/agentstation/starport/internal/router",
		"github.com/agentstation/starport/internal/server",
	)
	for _, packagePath := range []string{
		"github.com/agentstation/starport/internal/execution",
		"github.com/agentstation/starport/internal/availability",
	} {
		assertNoImports(t, packages[packagePath],
			"github.com/agentstation/starport/internal/providers",
			"github.com/agentstation/starport/internal/proxy",
			"github.com/agentstation/starport/internal/registry",
			"github.com/agentstation/starport/internal/router",
			"github.com/agentstation/starport/internal/server",
			"net/http",
		)
	}
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/responsecache"],
		"github.com/agentstation/starport/internal/inference",
	)
	for _, packagePath := range []string{
		"github.com/agentstation/starport/internal/identity",
		"github.com/agentstation/starport/internal/credentials",
		"github.com/agentstation/starport/internal/ratelimit",
		"github.com/agentstation/starport/internal/presets",
	} {
		assertOnlyInternalImports(t, packages[packagePath],
			"github.com/agentstation/starport/internal/storage",
		)
	}
	for _, packagePath := range []string{
		"github.com/agentstation/starport/internal/inference",
		"github.com/agentstation/starport/internal/failure",
	} {
		assertNoImports(t, packages[packagePath],
			"github.com/agentstation/starport/internal/providers",
			"github.com/agentstation/starport/internal/proxy",
			"github.com/agentstation/starport/internal/server",
			"net/http",
		)
	}
	for _, packagePath := range []string{
		"github.com/agentstation/starport/internal/protocol/openai",
		"github.com/agentstation/starport/internal/protocol/openrouter",
	} {
		assertOnlyInternalImports(t, packages[packagePath],
			"github.com/agentstation/starport/internal/inference",
		)
		assertNoImports(t, packages[packagePath],
			"github.com/agentstation/starport/internal/proxy",
			"github.com/agentstation/starport/internal/providers",
			"github.com/agentstation/starport/internal/server",
		)
	}
}

func TestPublicPackageBoundary(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	_, err := os.Stat(filepath.Join(repositoryRoot, "pkg"))
	require.ErrorIs(t, err, os.ErrNotExist, "binary-first v1 must not expose an unused public package tree")

	err = filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, declaration := range file.Decls {
			importDeclaration, ok := declaration.(*ast.GenDecl)
			if !ok || importDeclaration.Tok != token.IMPORT {
				continue
			}
			for _, specification := range importDeclaration.Specs {
				importSpecification := specification.(*ast.ImportSpec)
				dependency, unquoteErr := strconv.Unquote(importSpecification.Path.Value)
				if unquoteErr != nil {
					return unquoteErr
				}
				require.Falsef(t, strings.HasPrefix(dependency, "github.com/agentstation/starport/pkg/"),
					"%s imports removed public package %s", path, dependency)
			}
		}
		return nil
	})
	require.NoError(t, err)
}

func assertOnlyInternalImports(t *testing.T, imports []string, allowed ...string) {
	t.Helper()
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, dependency := range allowed {
		allowedSet[dependency] = struct{}{}
	}
	for _, dependency := range imports {
		if !strings.HasPrefix(dependency, "github.com/agentstation/starport/internal/") {
			continue
		}
		_, ok := allowedSet[dependency]
		require.Truef(t, ok, "internal import %s is not allowed", dependency)
	}
}

type packageImports struct {
	ImportPath string
	Imports    []string
}

func listPackages(t *testing.T, patterns ...string) map[string][]string {
	t.Helper()
	arguments := append([]string{"list", "-json"}, patterns...)
	command := exec.Command("go", arguments...)
	output, err := command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())

	packages := make(map[string][]string)
	decoder := json.NewDecoder(output)
	for {
		var item packageImports
		err := decoder.Decode(&item)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		sort.Strings(item.Imports)
		packages[item.ImportPath] = item.Imports
	}
	require.NoError(t, command.Wait())
	return packages
}

func assertNoImports(t *testing.T, imports []string, forbidden ...string) {
	t.Helper()
	for _, dependency := range imports {
		for _, prefix := range forbidden {
			require.Falsef(t, strings.HasPrefix(dependency, prefix),
				"forbidden import %s matches %s", dependency, prefix)
		}
	}
}
