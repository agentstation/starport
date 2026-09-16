package cli

import (
	"fmt"
	"io"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starport/internal/config"
)

func writeLegacyPaths(writer io.Writer, conflicts []config.LegacyPath, asJSON bool) error {
	if asJSON {
		return writeIndentedJSON(writer, conflicts)
	}
	if len(conflicts) == 0 {
		_, err := fmt.Fprintln(writer, "No legacy path conflicts detected.")
		return err
	}
	for _, conflict := range conflicts {
		if _, err := fmt.Fprintf(writer, "%s: previous=%q selected=%q\n  Preserve the previous location with %s before startup.\n",
			conflict.Role, conflict.PreviousPath, conflict.SelectedPath, conflict.Selector); err != nil {
			return err
		}
	}
	return nil
}

func writeFileManifest(writer io.Writer, report productpaths.FileManifest, asJSON bool) error {
	if asJSON {
		return writeIndentedJSON(writer, report)
	}
	for _, entry := range report.Files {
		if _, err := fmt.Fprintf(writer, "%s: %s [%s, %s]\n  Origin: %s\n  Creation: %s\n  Recovery: %s\n",
			entry.ID, entry.Location.Path, entry.Availability, entry.Policy.Access, entry.Location.Origin, entry.Creation, entry.Recovery); err != nil {
			return err
		}
	}
	for _, entry := range report.External {
		if _, err := fmt.Fprintf(writer, "%s: %s [external-system]\n  Recovery: %s\n", entry.ID, entry.Selection, entry.Recovery); err != nil {
			return err
		}
	}
	if report.Inspection != nil {
		for _, entry := range report.Inspection.Observations {
			if _, err := fmt.Fprintf(writer, "%s: %s [%s, access=%s]\n", entry.ID, entry.Path, entry.State, entry.AccessStatus); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(writer, "Inspection complete: %t (%d/%d entries)\n", report.Inspection.Complete, report.Inspection.Examined, report.Inspection.EntryLimit); err != nil {
			return err
		}
		for _, limitation := range report.Inspection.Limitations {
			if _, err := fmt.Fprintln(writer, limitation); err != nil {
				return err
			}
		}
	}
	return nil
}
