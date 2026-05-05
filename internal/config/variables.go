package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// applyVariables replaces substitution tokens in data:
//   - {$ key $}    — replaced from the config's variables map
//   - ${ENV_VAR}   — replaced from the process environment
//
// Missing environment variables cause an error. Unknown {$ key $} tokens are
// left unchanged (for forward compatibility).
//
// Security note: substitution is performed on raw YAML bytes before parsing.
// Values containing YAML structural characters (newlines, unquoted colons)
// could alter the parsed config structure. This is acceptable because both
// substitution sources (env vars and the variables: block) are operator-
// controlled. Do not source env vars from untrusted external systems.
func applyVariables(data []byte, vars map[string]string) ([]byte, error) {
	s := string(data)

	// Replace {$ key $} tokens from config variables map.
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{$ "+k+" $}", v)
	}

	// Replace ${ENV_VAR} tokens from environment.
	var missing []string
	s = envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1] // strip ${ and }
		val, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return match // leave unchanged; we'll report the error below
		}
		return val
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("config: undefined environment variable(s): %s", strings.Join(missing, ", "))
	}

	return []byte(s), nil
}
