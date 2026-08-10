// Package doclinks verifies local destinations in Markdown documents.
package doclinks

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Link is one Markdown link or image destination.
type Link struct {
	Line        int
	Destination string
}

// BrokenLink identifies a local destination that does not exist.
type BrokenLink struct {
	Source string
	Line   int
	Target string
}

// Parse returns links from Markdown syntax nodes. It excludes code and raw HTML.
func Parse(source []byte) ([]Link, error) {
	document := goldmark.New().Parser().Parse(text.NewReader(source))
	links := make([]Link, 0)
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		var destination []byte
		walkStatus := ast.WalkContinue
		switch typed := node.(type) {
		case *ast.Link:
			destination = typed.Destination
		case *ast.Image:
			destination = typed.Destination
			walkStatus = ast.WalkSkipChildren
		default:
			return ast.WalkContinue, nil
		}

		links = append(links, Link{
			Line:        sourceLine(source, node),
			Destination: string(destination),
		})
		return walkStatus, nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk Markdown syntax: %w", err)
	}
	return links, nil
}

// CheckFiles returns local link destinations that do not exist below root.
func CheckFiles(root string, files []string) ([]BrokenLink, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve documentation root: %w", err)
	}
	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve documentation root symlinks: %w", err)
	}

	broken := make([]BrokenLink, 0)
	for _, file := range files {
		sourcePath, err := resolveSource(rootPath, file)
		if err != nil {
			return nil, err
		}
		// #nosec G304 -- resolveSource confines the canonical path below rootPath.
		source, err := os.ReadFile(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		links, err := Parse(source)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		for _, link := range links {
			target, local, err := localTarget(link.Destination)
			if err != nil {
				return nil, fmt.Errorf("parse link in %s:%d: %w", file, link.Line, err)
			}
			if !local {
				continue
			}
			candidate := filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(target))
			if !within(rootPath, candidate) {
				return nil, fmt.Errorf("link target escapes documentation root in %s:%d: %s", file, link.Line, target)
			}
			if _, err := os.Stat(candidate); err == nil {
				resolved, err := filepath.EvalSymlinks(candidate)
				if err != nil {
					return nil, fmt.Errorf("resolve link target %s: %w", candidate, err)
				}
				if !within(rootPath, resolved) {
					return nil, fmt.Errorf("link target escapes documentation root in %s:%d: %s", file, link.Line, target)
				}
				continue
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("inspect link target %s: %w", candidate, err)
			}
			broken = append(broken, BrokenLink{
				Source: file,
				Line:   link.Line,
				Target: target,
			})
		}
	}
	return broken, nil
}

func resolveSource(root, file string) (string, error) {
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve documentation file %s: %w", file, err)
	}
	if !within(root, resolved) {
		return "", fmt.Errorf("documentation file escapes root: %s", file)
	}
	return resolved, nil
}

func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func localTarget(destination string) (string, bool, error) {
	if destination == "" || strings.HasPrefix(destination, "#") || strings.HasPrefix(destination, "//") {
		return "", false, nil
	}
	parsed, err := url.Parse(destination)
	if err != nil {
		return "", false, fmt.Errorf("invalid destination %q: %w", destination, err)
	}
	if parsed.Scheme != "" || parsed.Host != "" || parsed.Path == "" || strings.HasPrefix(parsed.Path, "/") {
		return "", false, nil
	}
	return parsed.Path, true, nil
}

func sourceLine(source []byte, node ast.Node) int {
	offset := node.Pos()
	if offset >= 0 && offset <= len(source) {
		return bytes.Count(source[:offset], []byte{'\n'}) + 1
	}

	_ = ast.Walk(node, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if value, ok := child.(*ast.Text); ok {
			offset = value.Segment.Start
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	if offset >= 0 && offset <= len(source) {
		return bytes.Count(source[:offset], []byte{'\n'}) + 1
	}
	return 1
}
