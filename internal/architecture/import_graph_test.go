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
		"../blob",
		"../inference",
		"../failure",
		"../identity",
		"../tenant",
		"../limits",
		"../credentials",
		"../ratelimit",
		"../presets",
		"../usage",
		"../response/cache",
		"../protocol/mediaform",
		"../protocol/openai",
		"../protocol/openrouter",
	)
	for _, packagePath := range []string{
		"github.com/agentstation/starport/internal/routing",
		"github.com/agentstation/starport/internal/catalog",
		"github.com/agentstation/starport/internal/execution",
		"github.com/agentstation/starport/internal/availability",
		"github.com/agentstation/starport/internal/blob",
		"github.com/agentstation/starport/internal/inference",
		"github.com/agentstation/starport/internal/failure",
		"github.com/agentstation/starport/internal/identity",
		"github.com/agentstation/starport/internal/tenant",
		"github.com/agentstation/starport/internal/limits",
		"github.com/agentstation/starport/internal/credentials",
		"github.com/agentstation/starport/internal/ratelimit",
		"github.com/agentstation/starport/internal/presets",
		"github.com/agentstation/starport/internal/usage",
		"github.com/agentstation/starport/internal/response/cache",
		"github.com/agentstation/starport/internal/protocol/mediaform",
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
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/response/cache"],
		"github.com/agentstation/starport/internal/inference",
	)
	// A repository-owning concept reaches durable storage and the shared
	// limits vocabulary, and nothing else inside the module.
	for _, packagePath := range []string{
		"github.com/agentstation/starport/internal/tenant",
		"github.com/agentstation/starport/internal/credentials",
		"github.com/agentstation/starport/internal/ratelimit",
		"github.com/agentstation/starport/internal/presets",
		"github.com/agentstation/starport/internal/usage",
	} {
		assertOnlyInternalImports(t, packages[packagePath],
			"github.com/agentstation/starport/internal/storage",
			"github.com/agentstation/starport/internal/limits",
		)
	}
	// A gateway API key belongs to a tenant, so identity reaches the account
	// model for its ID rules and its canonical ID. The loop above holds the
	// other direction closed: tenant may never reach identity, because an
	// account exists whether or not a key names it.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/identity"],
		"github.com/agentstation/starport/internal/storage",
		"github.com/agentstation/starport/internal/limits",
		"github.com/agentstation/starport/internal/tenant",
	)
	// Limits is the vocabulary both a gateway API key and a tenant hold. It
	// stays a leaf so neither owner can reach the other through it.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/limits"])
	// Blob stores opaque bytes at an opaque key. It is a leaf with no internal
	// import at all, because a store that could reach a Starport concept would
	// start reading meaning into the bytes it holds. The owner of the key holds
	// every meaning instead.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/blob"])
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
	// A media upload arrives as multipart form data, and both protocol
	// families spell its parts the same way. Mediaform owns that reading once,
	// and stays a leaf beside the canonical vocabulary so neither codec can
	// reach the other through it.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/protocol/mediaform"],
		"github.com/agentstation/starport/internal/inference",
	)
	for _, packagePath := range []string{
		"github.com/agentstation/starport/internal/protocol/openai",
		"github.com/agentstation/starport/internal/protocol/openrouter",
	} {
		assertOnlyInternalImports(t, packages[packagePath],
			"github.com/agentstation/starport/internal/inference",
			"github.com/agentstation/starport/internal/protocol/mediaform",
		)
		assertNoImports(t, packages[packagePath],
			"github.com/agentstation/starport/internal/proxy",
			"github.com/agentstation/starport/internal/providers",
			"github.com/agentstation/starport/internal/server",
		)
	}
}

func TestProviderAuthenticationPackageHasNoCloudSDKImports(t *testing.T) {
	packages := listPackages(t, "../providers/auth")
	imports, exists := packages["github.com/agentstation/starport/internal/providers/auth"]
	require.True(t, exists, "provider authentication package is absent from the import graph")
	assertNoImports(t, imports,
		"cloud.google.com/go/",
		"github.com/Azure/azure-sdk-for-go/",
		"github.com/aws/aws-sdk-go-v2/",
	)
	assertOnlyInternalImports(t, imports,
		"github.com/agentstation/starport/internal/credentials",
	)
}

func TestCloudChainPackageDoesNotMutateHTTPRequests(t *testing.T) {
	packages := listPackages(t, "../credentials/cloudchain")
	imports, exists := packages["github.com/agentstation/starport/internal/credentials/cloudchain"]
	require.True(t, exists, "cloud credential chain package is absent from the import graph")
	assertNoImports(t, imports,
		"net/http",
		"github.com/agentstation/starport/internal/providers/auth",
		"github.com/agentstation/starport/internal/providers/connectors",
	)
	assertOnlyInternalImports(t, imports,
		"github.com/agentstation/starport/internal/credentials",
	)
}

func TestProductionConnectorCallsUseExecutionDeadline(t *testing.T) {
	root := repositoryRoot(t)
	connectorFiles, err := filepath.Glob(filepath.Join(root, "internal", "providers", "connectors", "*.go"))
	require.NoError(t, err)
	for _, path := range connectorFiles {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		require.NotContainsf(t, string(source), "context.WithTimeout(",
			"connector %s must use its caller's execution deadline", path)
		require.NotContainsf(t, string(source), "context.WithDeadline(",
			"connector %s must use its caller's execution deadline", path)
	}

	executorSource, err := os.ReadFile(filepath.Join(root, "internal", "execution", "executor.go"))
	require.NoError(t, err)
	require.Contains(t, string(executorSource), "context.WithTimeout(")
	streamSource, err := os.ReadFile(filepath.Join(root, "internal", "execution", "stream.go"))
	require.NoError(t, err)
	require.Contains(t, string(streamSource), "context.WithTimeout(")
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
