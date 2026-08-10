package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/agentstation/starport/internal/diagnosis"
)

const doctorFormatJSON = "json"

// ErrDiagnosisFailed reports one or more failed diagnostic checks.
var ErrDiagnosisFailed = errors.New("diagnosis failed")

func writeDiagnosis(writer io.Writer, report diagnosis.Report, asJSON bool) error {
	if asJSON {
		return writeIndentedJSON(writer, report)
	}
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(
			writer,
			"%s %s: %s\n",
			strings.ToUpper(check.Status),
			check.ID,
			check.Message,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(
		writer,
		"Summary: %d passed, %d failed, %d skipped\n",
		report.Passed,
		report.Failed,
		report.Skipped,
	)
	return err
}
