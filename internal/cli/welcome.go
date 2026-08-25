package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// welcomeStampContents is written into the stamp file so a person who finds it
// can tell what it is. The file's existence is the whole signal; the text is
// for whoever opens it wondering whether deleting it breaks something.
const welcomeStampContents = "Starport has greeted this machine. Delete this file to see the welcome again.\n"

// welcome is the greeting a machine gets once.
//
// It answers the two questions a first run leaves open — how to reach the
// console, and what the credential in front of them is for — because those are
// the two the gateway cannot answer by starting successfully. Everything else
// an operator needs is in `starport --help`, and repeating it here would bury
// the two lines that are only true on a first run.
const welcome = `
Welcome to Starport.

  starport ui        Open the console. No key to paste: the link signs
                     this browser in with a session from this machine.
  starport auth url  Print that link instead of opening it.

A gateway API key is for a program calling the gateway, not for the
console. Create one in the console under Keys, or with ` + "`starport init`" + `.
`

// greetOnce prints the welcome the first time this machine runs the gateway
// and does nothing afterwards.
//
// A failure to write the stamp is not reported. The greeting has already been
// printed by then, and the worst a lost stamp can do is print it again on the
// next run — a far smaller harm than refusing to start a gateway because a
// cosmetic file could not be created.
func greetOnce(writer io.Writer, deps Dependencies) {
	if deps.ResolvePaths == nil {
		return
	}
	paths, err := deps.ResolvePaths()
	if err != nil || paths.WelcomeStampFile == "" {
		return
	}
	if _, err := os.Stat(paths.WelcomeStampFile); err == nil {
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		// An unreadable stamp is not a reason to greet. Something is wrong with
		// the data directory, and a welcome message would be the least useful
		// thing to say about it.
		return
	}
	if _, err := fmt.Fprint(writer, welcome); err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(paths.WelcomeStampFile), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(paths.WelcomeStampFile, []byte(welcomeStampContents), 0o600)
}
