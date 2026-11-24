package xerr

import (
	"strings"
)

// PreflightError reports startup blockers and commands that address them.
type PreflightError struct {
	Title    string
	Problems []string
	Hints    []string
}

func (e *PreflightError) Error() string {
	var output strings.Builder

	output.WriteString("error: " + e.Title)

	for _, problem := range e.Problems {
		output.WriteString("\n  - " + strings.ReplaceAll(problem, "\n", "; "))
	}

	if len(e.Hints) > 0 {
		output.WriteByte('\n')
	}

	for _, hint := range e.Hints {
		output.WriteString("\nhint: " + hint)
	}

	return output.String()
}

// ShellArg quotes a value for use as one argument in a shell command.
func ShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
