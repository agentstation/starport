package config

import (
	"bytes"
	"maps"
	"strings"

	"github.com/joho/godotenv"
)

// editDotenv applies field edits to dotenv bytes. Each edit removes every
// line of its Starport and Starmap names. A set value replaces the first
// Starport line in place, or the file gains it at the end. The result must
// parse to the original values with only the edited names changed, so a
// layout that this editor cannot change safely refuses.
func editDotenv(current []byte, edits []FieldEdit) ([]byte, error) {
	before, err := godotenv.Unmarshal(string(current))
	if err != nil {
		return nil, &Refusal{Reason: RefusalInvalidEdit, Message: "the configuration file does not parse"}
	}
	want := maps.Clone(before)
	lines := splitLines(current)
	for _, edit := range edits {
		product := catalogEnvironmentName(edit.Name)
		delete(want, product)
		delete(want, edit.Name)
		var line string
		if edit.Value != nil {
			if !dotenvSafeValue(*edit.Value) {
				return nil, &Refusal{
					Reason: RefusalInvalidEdit, Setting: edit.Key,
					Message: "the value contains a single quote, a line break, or a trailing backslash. A configuration file cannot hold it",
				}
			}
			want[product] = *edit.Value
			line = product + "='" + *edit.Value + "'\n"
		}
		next := lines[:0:0]
		for _, existing := range lines {
			key := dotenvLineKey(existing)
			if key == product && line != "" {
				next = append(next, line)
				line = ""
				continue
			}
			if key == product || key == edit.Name {
				continue
			}
			next = append(next, existing)
		}
		if line != "" {
			if count := len(next); count > 0 && !strings.HasSuffix(next[count-1], "\n") {
				next[count-1] += "\n"
			}
			next = append(next, line)
		}
		lines = next
	}
	edited := []byte(strings.Join(lines, ""))
	after, err := godotenv.Unmarshal(string(edited))
	if err != nil || !maps.Equal(after, want) {
		return nil, &Refusal{Reason: RefusalInvalidEdit, Message: "the configuration file layout prevents a safe field save. Edit the file by hand"}
	}
	return edited, nil
}

// splitLines keeps each line terminator with its line.
func splitLines(data []byte) []string {
	var lines []string
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			lines = append(lines, string(data))
			break
		}
		lines = append(lines, string(data[:end+1]))
		data = data[end+1:]
	}
	return lines
}

// dotenvLineKey returns the variable name that a line assigns, or an empty
// string for a comment, a blank line, or a continuation line.
func dotenvLineKey(line string) string {
	line = strings.TrimLeft(line, " \t")
	if rest, found := strings.CutPrefix(line, "export"); found && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
		line = strings.TrimLeft(rest, " \t")
	}
	end := strings.IndexFunc(line, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '.'
	})
	if end <= 0 {
		return ""
	}
	if rest := strings.TrimLeft(line[end:], " \t"); rest == "" || (rest[0] != '=' && rest[0] != ':') {
		return ""
	}
	return line[:end]
}

// dotenvSafeValue reports whether a single-quoted dotenv value reads back
// unchanged.
func dotenvSafeValue(value string) bool {
	return !strings.ContainsAny(value, "'\r\n\x00") && !strings.HasSuffix(value, `\`)
}
