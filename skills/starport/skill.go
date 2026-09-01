// Package skill carries the canonical starport agent skill. The repository
// versions SKILL.md beside the CLI so a released binary always installs the
// skill written for its own commands.
package skill

import _ "embed"

// Name is the skill's directory name under a skills root.
const Name = "starport"

// Markdown is the embedded skill document, verbatim.
//
//go:embed SKILL.md
var Markdown string
