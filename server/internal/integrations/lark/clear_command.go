package lark

import "strings"

const clearCommandPrefix = "/clear"

func parseClearCommand(body string) bool {
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, clearCommandPrefix) {
			return false
		}
		rest := trimmed[len(clearCommandPrefix):]
		if rest == "" {
			return true
		}
		return rest[0] == ' ' || rest[0] == '\t'
	}
	return false
}
