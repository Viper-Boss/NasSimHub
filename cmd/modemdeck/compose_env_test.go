package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every environment variable this binary reads has to be named in a Compose
// file, or the deployment cannot supply it.
//
// THIS TEST EXISTS BECAUSE THE FAILURE IS SILENT, and it has now happened
// twice. Compose does not forward the host environment into a service: a
// variable reaches the container only if the service's `environment:` block
// names it, and the `.env` file beside the Compose file feeds ${...}
// substitution rather than the container. So a variable read here but absent
// from every Compose file can be set on the NAS, in the shell, in .env, in all
// three -- and the process still sees "".
//
// What makes that dangerous rather than merely annoying is that this codebase
// is careful to treat absent configuration as a legitimate state. An absent
// licence issuer key means "this build cannot verify certificates", which
// locks paid features and leaves the free ones working. An absent updater URL
// means "no updater is deployed". Those are correct behaviours for a genuinely
// unconfigured install -- and indistinguishable from a wiring mistake.
//
// The two that shipped:
//
//	MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY was read here and named in no Compose
//	file at all, so no gateway could ever verify a certificate no matter what
//	the operator set.
//
//	The DHT service was missing from the API's options in the same way -- see
//	the comment on the `DHT:` line in main.go -- so every production build
//	answered "the agent does not offer this capability", which is also a
//	legitimate answer.
//
// A variable that is genuinely meant to be unset still has to appear, with an
// empty default (`${VAR:-}`). Writing it down is the whole point: it makes the
// choice reviewable instead of leaving absence to mean either "on purpose" or
// "forgotten".
func TestEveryEnvironmentVariableReachesTheContainer(t *testing.T) {
	root := repositoryRoot(t)

	source, err := os.ReadFile(filepath.Join(root, "cmd", "modemdeck", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	// Only MODEMDECK_ and HOMESIM_ variables: those are this product's own.
	// PATH, HOME and friends come from the image and are not ours to wire.
	pattern := regexp.MustCompile(`os\.Getenv\("((?:MODEMDECK|HOMESIM)_[A-Z0-9_]+)"\)`)
	seen := map[string]bool{}
	for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
		seen[match[1]] = true
	}
	if len(seen) == 0 {
		t.Fatal("no MODEMDECK_/HOMESIM_ environment reads found in main.go; this test has stopped testing anything")
	}

	// Every Compose file counts, including the overlays: the OTA, Cloudflare
	// and fnOS files are applied on top of the base with -f, so a variable
	// supplied by one of them does reach the container in the deployment that
	// uses it.
	composeFiles := []string{
		"docker-compose.yml",
		"docker-compose.advanced.yml",
		"docker-compose.ota.yml",
		"docker-compose.cloudflare.yml",
		"docker-compose.cloudflare-turn.yml",
		filepath.Join("deploy", "fnos", "compose.fnos.yml"),
	}
	var compose strings.Builder
	for _, name := range composeFiles {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			// A Compose file that has been renamed or removed should fail this
			// test rather than silently shrink its coverage.
			t.Fatalf("read %s: %v", name, err)
		}
		compose.Write(contents)
		compose.WriteByte('\n')
	}
	haystack := compose.String()

	var missing []string
	for name := range seen {
		if !strings.Contains(haystack, name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf(
			"these variables are read by main.go but named in no Compose file, so no deployment can set them: %s\n"+
				"Add each to a service's environment: block, with an empty default if it is optional:\n"+
				"      %s: ${%s:-}",
			strings.Join(missing, ", "), missing[0], missing[0],
		)
	}
}

// TestTheIssuerKeyIsDocumentedWhereOperatorsLook covers the other half: the
// variable being wired is no use if nobody knows it exists. Both example env
// files are what an operator copies, so the key has to be in them -- and it has
// to default to empty, because a gateway with no key is the normal case.
func TestTheIssuerKeyIsDocumentedWhereOperatorsLook(t *testing.T) {
	root := repositoryRoot(t)
	const variable = "MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY"

	for _, name := range []string{
		".env.example",
		filepath.Join("deploy", "fnos", ".env.fnos.example"),
	} {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(contents)
		if !strings.Contains(text, variable+"=") {
			t.Errorf("%s does not mention %s, so an operator has no way to know it exists", name, variable)
			continue
		}
		// Empty default. A committed example carrying somebody's real key
		// would be the worst possible content for this line.
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, variable+"=") && line != variable+"=" {
				t.Errorf("%s sets a value for %s; the example must leave it empty", name, variable)
			}
		}
	}
}

// repositoryRoot walks up from the test's working directory to the module root.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for depth := 0; depth < 8; depth++ {
		if _, err := os.Stat(filepath.Join(directory, "docker-compose.yml")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	t.Fatal("could not find the repository root from the test's working directory")
	return ""
}
