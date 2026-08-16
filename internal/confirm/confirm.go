// Package confirm implements the interactive yes/no confirmation prompt
// used before destructive CLI operations.
package confirm

import (
	"bufio"
	"io"
	"strings"
)

// Prompt writes prompt to w, then reads a single line from r and reports
// whether the answer was an affirmative "y" or "yes" (case-insensitive,
// surrounding whitespace trimmed). Any other answer — including empty input
// from a bare Enter press — is treated as "no", matching the conventional
// "[y/N]" default-to-no prompt style.
func Prompt(w io.Writer, r io.Reader, prompt string) (bool, error) {
	if _, err := io.WriteString(w, prompt); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
