package credentials

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// compileDestinationPath limits variables to the path of an approved origin.
// Each request retains its exact target after the template check.
func compileDestinationPath(u *url.URL) (*regexp.Regexp, error) {
	if strings.ContainsAny(u.Host+u.RawQuery, "{}") || !safeDestinationPath(u.Path) {
		return nil, ErrDestinationUnapproved
	}
	var expression strings.Builder
	expression.WriteString("\\A")
	rest := u.Path
	names := make(map[string]bool)
	for {
		literal, variable, found := strings.Cut(rest, "{")
		if strings.Contains(literal, "}") {
			return nil, ErrDestinationUnapproved
		}
		expression.WriteString(regexp.QuoteMeta(literal))
		if !found {
			break
		}
		name, after, closed := strings.Cut(variable, "}")
		name, multiple := strings.CutSuffix(name, "...")
		if !closed || !validDestinationVariable(name) || names[name] || strings.HasPrefix(after, "{") {
			return nil, ErrDestinationUnapproved
		}
		names[name] = true
		expression.WriteString(`[^/?#\\{}\x00-\x20\x7f]+`)
		if multiple {
			expression.WriteString(`(?:/[^/?#\\{}\x00-\x20\x7f]+)*`)
		}
		rest = after
	}
	if len(names) == 0 {
		return nil, ErrDestinationUnapproved
	}
	expression.WriteString("\\z")
	compiled, err := regexp.Compile(expression.String())
	if err != nil {
		return nil, ErrDestinationUnapproved
	}
	return compiled, nil
}

func validDestinationVariable(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, char := range name {
		if char != '_' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func safeDestinationPath(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}
	for _, char := range path {
		if unicode.IsControl(char) || unicode.IsSpace(char) || strings.ContainsRune(`\\%?#`, char) {
			return false
		}
	}
	for segment := range strings.SplitSeq(path[1:], "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func (t *destinationTarget) matchesPath(u *url.URL) bool {
	if t.template == nil {
		return u.EscapedPath() == t.path
	}
	return safeDestinationPath(u.Path) && t.template.MatchString(u.Path)
}
