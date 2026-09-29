package cli

import (
	"fmt"
	"io"

	"github.com/agentstation/starport/internal/localauth"
)

func writeAuthRotationMetadata(writer io.Writer, token localauth.Token, path string, asJSON bool) error {
	if asJSON {
		return writeIndentedJSON(writer, struct {
			authStatusView
			RestartRequired bool `json:"restart_required"`
		}{authStatusView: authStatusView{
			Present: true, TokenFile: path, Generation: token.Generation, IssuedAt: &token.IssuedAt,
			RotatedAt: token.RotatedAt, AllowsNetworkBind: token.Rotated(),
		}, RestartRequired: true})
	}
	_, err := fmt.Fprintf(writer,
		"Rotated the local admin token to generation %d.\nToken file: %s\nThe secret remains in the token file.\nA running gateway requires a restart to use this token.\n",
		token.Generation, path)
	return err
}
