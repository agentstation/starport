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
		"../document",
		"../files",
		"../jobs",
		"../inference",
		"../failure",
		"../apikey",
		"../identity",
		"../account",
		"../sqlstore",
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
		"github.com/agentstation/starport/internal/document",
		"github.com/agentstation/starport/internal/files",
		"github.com/agentstation/starport/internal/jobs",
		"github.com/agentstation/starport/internal/inference",
		"github.com/agentstation/starport/internal/failure",
		"github.com/agentstation/starport/internal/apikey",
		"github.com/agentstation/starport/internal/identity",
		"github.com/agentstation/starport/internal/account",
		"github.com/agentstation/starport/internal/sqlstore",
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
	// A repository-owning concept reaches durable storage — the key-value
	// store, and for account templates the relational store — and the shared
	// limits vocabulary, and nothing else inside the module.
	for _, packagePath := range []string{
		"github.com/agentstation/starport/internal/account",
		"github.com/agentstation/starport/internal/credentials",
		"github.com/agentstation/starport/internal/ratelimit",
		"github.com/agentstation/starport/internal/presets",
		"github.com/agentstation/starport/internal/usage",
	} {
		assertOnlyInternalImports(t, packages[packagePath],
			"github.com/agentstation/starport/internal/storage",
			"github.com/agentstation/starport/internal/sqlstore",
			"github.com/agentstation/starport/internal/limits",
		)
	}
	// The humans a deployment knows are purely relational: identity
	// reaches the relational store and the shared limits vocabulary — a
	// team carries a budget the way an account or a key does — and nothing
	// else inside the module, so no concept can smuggle behavior in
	// through a user or a team.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/identity"],
		"github.com/agentstation/starport/internal/sqlstore",
		"github.com/agentstation/starport/internal/limits",
	)
	// The relational store is a leaf beside the key-value store: it holds
	// rows for the concepts that own them and reads no meaning of its own.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/sqlstore"])
	// A gateway API key belongs to an account, so apikey reaches the account
	// model for its ID rules and its canonical ID. The loop above holds the
	// other direction closed: account may never reach apikey, because an
	// account exists whether or not a key names it.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/apikey"],
		"github.com/agentstation/starport/internal/storage",
		"github.com/agentstation/starport/internal/limits",
		"github.com/agentstation/starport/internal/account",
	)
	// Limits is the vocabulary both a gateway API key and an account hold. It
	// stays a leaf so neither owner can reach the other through it.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/limits"])
	// Blob stores opaque bytes at an opaque key. It is a leaf with no internal
	// import at all, because a store that could reach a Starport concept would
	// start reading meaning into the bytes it holds. The owner of the key holds
	// every meaning instead.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/blob"])
	// Document is the native parser engine. It is a leaf, and the rule is the
	// reason the engine is free: an import of a provider, a connector, or a
	// transport would mean a document that carries its own text could still
	// leave the process, and the caller would pay for a read this package
	// already did. The recognition engine is a route, not an import here.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/document"])
	assertNoImports(t, packages["github.com/agentstation/starport/internal/document"],
		"github.com/agentstation/starport/internal/providers",
		"github.com/agentstation/starport/internal/proxy",
		"github.com/agentstation/starport/internal/registry",
		"github.com/agentstation/starport/internal/router",
		"github.com/agentstation/starport/internal/server",
		"net/http",
		"net/url",
	)
	// Files owns the record that gives a stored object a name, an owner, and a
	// lifetime. It reaches the two stores it writes to and nothing else. A
	// dependency on routing, execution, or a protocol codec would let the
	// meaning of a request decide what a stored file is.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/files"],
		"github.com/agentstation/starport/internal/blob",
		"github.com/agentstation/starport/internal/storage",
	)
	// Jobs owns work that outlives its request. It reaches the operation
	// vocabulary and the record store, and nothing else. A dependency on
	// execution or a provider connector would put the poll loop inside the
	// record, and the seam exists to keep the two apart.
	assertOnlyInternalImports(t, packages["github.com/agentstation/starport/internal/jobs"],
		"github.com/agentstation/starport/internal/blob",
		"github.com/agentstation/starport/internal/routing",
		"github.com/agentstation/starport/internal/storage",
	)
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

	// An ordinary call keeps its elapsed bound, so the executor owns one
	// deadline for the whole call.
	executorSource, err := os.ReadFile(filepath.Join(root, "internal", "execution", "executor.go"))
	require.NoError(t, err)
	require.Contains(t, string(executorSource), "context.WithTimeout(")

	// A stream is route-specific: the elapsed budget bounds route selection
	// alone and releases at the first byte. A committed stream therefore
	// carries a cancelable lifetime and no deadline, because a stream a
	// caller reads must not be cut in half.
	streamSource, err := os.ReadFile(filepath.Join(root, "internal", "execution", "stream.go"))
	require.NoError(t, err)
	require.NotContains(t, string(streamSource), "context.WithTimeout(",
		"a committed stream must carry no elapsed deadline")
	require.NotContains(t, string(streamSource), "context.WithDeadline(",
		"a committed stream must carry no elapsed deadline")
	require.Contains(t, string(streamSource), "watchSelection(",
		"the stream must bound route selection")
	require.Contains(t, string(streamSource), "releaseSelection(",
		"the stream must release the selection bound at the first byte")
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
