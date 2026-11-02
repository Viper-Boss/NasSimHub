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
//	the operator set. That one was NOT fixed by wiring it through: making the
//	licensing trust root settable per deployment was the deeper mistake, so
//	the key is compiled in and the variable is gone. The sweep below therefore
//	no longer sees it, and the test underneath this one keeps it from coming
//	back.
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

	// EVERY binary that Compose starts, not just the API.
	//
	// This swept only cmd/modemdeck at first, which left the updater -- the
	// process that decides where releases come from -- outside the check. It
	// read MODEMDECK_RELEASE_MANIFEST_URL_TEMPLATE, no Compose file named it,
	// and so the one setting that lets an operator point the gateway at their
	// own release server did nothing when set. Exactly the failure this test
	// exists for, sitting just outside where it was looking.
	binaries := []string{
		filepath.Join("cmd", "modemdeck", "main.go"),
		// Node settings are read beside their wiring, not in main.go.
		filepath.Join("cmd", "modemdeck", "nassimhub_nodes.go"),
		filepath.Join("cmd", "modemdeck", "airsim.go"),
		filepath.Join("cmd", "modemdeck-updater", "main.go"),
	}

	// Only MODEMDECK_ and HOMESIM_ variables: those are this product's own.
	// PATH, HOME and friends come from the image and are not ours to wire.
	pattern := regexp.MustCompile(`os\.Getenv\("((?:MODEMDECK|HOMESIM)_[A-Z0-9_]+)"\)`)
	seen := map[string]bool{}
	for _, binary := range binaries {
		source, err := os.ReadFile(filepath.Join(root, binary))
		if err != nil {
			t.Fatalf("read %s: %v", binary, err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
			seen[match[1]] = true
		}
	}
	if len(seen) == 0 {
		t.Fatal("no MODEMDECK_/HOMESIM_ environment reads found; this test has stopped testing anything")
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
		"docker-compose.airsim.yml",
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
			"these variables are read by a shipped binary but named in no Compose file, so no deployment can set them: %s\n"+
				"Add each to a service's environment: block, with an empty default if it is optional:\n"+
				"      %s: ${%s:-}",
			strings.Join(missing, ", "), missing[0], missing[0],
		)
	}
}

// TestTheIssuerKeyIsNotConfigurableFromTheEnvironment is the inverse of the
// sweep above, and it exists because this variable was wired up as ordinary
// configuration and should never have been.
//
// The licence issuer's public key is the trust root of the whole scheme: a
// certificate is honoured if and only if it verifies against that key. While
// the key came from the environment, the operator of each gateway -- the
// customer -- chose it. cmd/licensegen, in this repository, both generates key
// pairs and signs certificates, so unlocking every paid feature for good was:
//
//	licensegen keygen
//	echo MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY=<their key> >> .env
//	licensegen issue -scope machine -features ... -days 3650
//
// No binary patched, no protection defeated -- just a documented configuration
// value set to a documented value. So the key is compiled in now (see
// internal/license/issuer_key.go) and this test keeps it that way, because the
// variable is exactly the kind of thing a future reader would helpfully
// "restore" after finding it in the git history or in a deployment guide.
//
// What is asserted: main.go must not read it, and no Compose or example env
// file may ASSIGN it. Mentioning it in a comment is fine and in fact wanted --
// docker-compose.yml carries a note saying why it is absent, which is how the
// next person finds out rather than re-adding it.
func TestTheIssuerKeyIsNotConfigurableFromTheEnvironment(t *testing.T) {
	root := repositoryRoot(t)
	const variable = "MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY"

	source, err := os.ReadFile(filepath.Join(root, "cmd", "modemdeck", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if strings.Contains(string(source), `os.Getenv("`+variable+`")`) {
		t.Errorf(
			"main.go reads %s from the environment.\n"+
				"That hands the licensing trust root to whoever deploys the gateway: with cmd/licensegen\n"+
				"they can generate a key pair, set this variable to their own public key, and sign\n"+
				"themselves certificates for every paid feature. Use license.IssuerPublicKey instead.",
			variable,
		)
	}

	for _, name := range []string{
		"docker-compose.yml",
		"docker-compose.advanced.yml",
		"docker-compose.ota.yml",
		"docker-compose.cloudflare.yml",
		"docker-compose.cloudflare-turn.yml",
		filepath.Join("deploy", "fnos", "compose.fnos.yml"),
		".env.example",
		filepath.Join("deploy", "fnos", ".env.fnos.example"),
	} {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for number, line := range strings.Split(string(contents), "\n") {
			trimmed := strings.TrimSpace(line)
			// A comment naming the variable is deliberate documentation.
			// Both file formats comment with '#'.
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if !strings.Contains(trimmed, variable) {
				continue
			}
			// An assignment in either syntax: `VAR: value` (Compose) or
			// `VAR=value` (env file).
			if strings.Contains(trimmed, variable+":") || strings.Contains(trimmed, variable+"=") {
				t.Errorf(
					"%s:%d assigns %s:\n\t%s\n"+
						"The issuer key is compiled in on purpose. Anything that can set it can\n"+
						"self-sign licences; see internal/license/issuer_key.go.",
					name, number+1, variable, trimmed,
				)
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
