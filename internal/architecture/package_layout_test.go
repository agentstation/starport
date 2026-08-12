package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type approvedPackage struct {
	path string
	name string
}

func TestApprovedInternalPackageLayout(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	approved := []approvedPackage{
		{path: "internal/protocol/openai", name: "openai"},
		{path: "internal/protocol/openrouter", name: "openrouter"},
		{path: "internal/repotest", name: "repotest"},
	}
	removed := []string{
		filepath.Join("internal", "http"+"api"),
		filepath.Join("internal", "repository"+"test"),
		filepath.Join("internal", "test"+"util"),
	}

	for _, path := range removed {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Errorf("removed package path %q exists or cannot be checked: %v", path, err)
		}
	}

	for _, pkg := range approved {
		assertPackageName(t, filepath.Join(root, pkg.path), pkg.name)
	}

	args := []string{"list", "-f", "{{.ImportPath}}\t{{.Name}}"}
	for _, pkg := range approved {
		args = append(args, "./"+pkg.path)
	}
	command := exec.Command("go", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list approved packages: %v\n%s", err, output)
	}
	listed := make(map[string]string, len(approved))
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			t.Fatalf("unexpected go list line %q", line)
		}
		listed[fields[0]] = fields[1]
	}
	for _, pkg := range approved {
		importPath := "github.com/agentstation/starport/" + pkg.path
		if got := listed[importPath]; got != pkg.name {
			t.Errorf("package %q name = %q, want %q", importPath, got, pkg.name)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func assertPackageName(t *testing.T, directory, expected string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read approved package %q: %v", directory, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
		if err != nil {
			t.Errorf("parse package declaration %q: %v", path, err)
			continue
		}
		if file.Name.Name != expected && file.Name.Name != expected+"_test" {
			t.Errorf(
				"%s package = %q, want %q or %q",
				path,
				file.Name.Name,
				expected,
				expected+"_test",
			)
		}
	}
}
