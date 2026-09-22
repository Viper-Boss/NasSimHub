// Command licensegen issues licence certificates.
//
// This runs on the ISSUER's machine, never on a gateway. The private key it
// uses is the only thing standing between the licence scheme and anyone being
// able to grant themselves anything, so it does not belong on a box that is
// exposed to the internet and handed to customers. The product embeds only the
// matching public key.
//
//	licensegen keygen -out issuer.key
//	    Create a keypair. Prints the public key to paste into the build.
//
//	licensegen issue -key issuer.key -scope machine -id <machine-id> \
//	    -features net.wireguard -days 365
//	    Sign a certificate and print it.
//
//	licensegen verify -pub <base64> -cert cert.json
//	    Check a certificate the way a gateway would.
//
//	licensegen features
//	    List the catalogue and what each feature requires.
//
//	licensegen release-sign -key issuer.key -version v1.4.2 \
//	    -api sha256:... -web sha256:... -hardware sha256:... -updater sha256:...
//	    Sign the release manifest gateways verify before installing anything.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/license"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "issue":
		err = issue(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "features":
		err = features()
	case "release-sign":
		err = releaseSign(os.Args[2:])
	case "report-request":
		err = reportRequest(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `licensegen — issue licence certificates

  keygen   -out <file>                      create the issuer keypair
  issue    -key <file> -scope machine|module -id <value>
           -features a,b -days N [-note ""] sign a certificate
  verify   -pub <base64> -cert <file>       check one as a gateway would
  features                                  list the catalogue
  release-sign -key <file> -version vX.Y.Z
           -api sha256:... -web sha256:... -hardware sha256:...
           -updater sha256:... [-out release.signed.json]
                                            sign a release manifest
  report-request -key <file> -machine <id>  sign a client-report request

The key file holds the private key. Keep it off any machine a customer can
reach; losing it means no new certificates, and leaking it means anyone can
issue them.
`)
}

func keygen(args []string) error {
	set := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := set.String("out", "", "where to write the private key")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("-out is required")
	}
	if _, err := os.Stat(*out); err == nil {
		// Overwriting a key would orphan every certificate issued with it,
		// with no way back. Refusing is the only safe default.
		return fmt.Errorf("%s already exists; refusing to overwrite an issuer key", *out)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(hex.EncodeToString(private)+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Printf("private key written to %s (mode 0600)\n\n", *out)
	fmt.Printf("public key, embed this in the build:\n%s\n", base64.StdEncoding.EncodeToString(public))
	return nil
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("key file is not hex: %w", err)
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("key file is %d bytes, expected %d", len(decoded), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(decoded), nil
}

func issue(args []string) error {
	set := flag.NewFlagSet("issue", flag.ExitOnError)
	keyPath := set.String("key", "", "issuer private key file")
	scope := set.String("scope", "", "machine or module")
	bound := set.String("id", "", "machine id, or module serial number")
	featureList := set.String("features", "", "comma-separated catalogue names")
	days := set.Int("days", 0, "validity in days; 0 issues a perpetual certificate")
	note := set.String("note", "", "free text for your own records; it is signed")
	certID := set.String("cert-id", "", "certificate id; generated when empty")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || *scope == "" || *bound == "" || *featureList == "" {
		return fmt.Errorf("-key, -scope, -id and -features are all required")
	}

	names := strings.Split(*featureList, ",")
	for index, name := range names {
		names[index] = strings.TrimSpace(name)
	}
	// A typo here would produce a certificate that verifies and unlocks
	// nothing, which is the worst outcome: the customer has paid, the file
	// looks right, and no error is reported anywhere.
	known := map[string]bool{}
	for _, feature := range license.Features() {
		known[string(feature)] = true
	}
	for _, name := range names {
		if !known[name] {
			return fmt.Errorf("%q is not in the catalogue; run `licensegen features`", name)
		}
	}

	certificate := license.Certificate{
		Version:  license.CurrentVersion,
		ID:       *certID,
		Scope:    license.Scope(*scope),
		Features: names,
		IssuedAt: time.Now().Unix(),
		Note:     *note,
	}
	if certificate.ID == "" {
		suffix := make([]byte, 8)
		if _, err := rand.Read(suffix); err != nil {
			return err
		}
		certificate.ID = "c_" + hex.EncodeToString(suffix)
	}
	switch certificate.Scope {
	case license.ScopeMachine:
		certificate.MachineID = *bound
	case license.ScopeModule:
		certificate.ModuleSN = *bound
	default:
		return fmt.Errorf("-scope must be machine or module")
	}
	if *days > 0 {
		certificate.ExpiresAt = time.Now().AddDate(0, 0, *days).Unix()
	}

	key, err := loadPrivateKey(*keyPath)
	if err != nil {
		return err
	}
	signed, err := license.Sign(certificate, key)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func verify(args []string) error {
	set := flag.NewFlagSet("verify", flag.ExitOnError)
	pub := set.String("pub", "", "issuer public key, base64")
	certPath := set.String("cert", "", "certificate file")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *pub == "" || *certPath == "" {
		return fmt.Errorf("-pub and -cert are required")
	}
	decoded, err := base64.StdEncoding.DecodeString(*pub)
	if err != nil {
		return fmt.Errorf("public key is not base64: %w", err)
	}
	raw, err := os.ReadFile(*certPath)
	if err != nil {
		return err
	}
	var signed license.Signed
	if err := json.Unmarshal(raw, &signed); err != nil {
		return err
	}
	if err := license.Verify(signed, ed25519.PublicKey(decoded), time.Now()); err != nil {
		return err
	}
	fmt.Printf("valid: %s certificate %s\n", signed.Certificate.Scope, signed.Certificate.ID)
	if expiry, has := signed.Certificate.Expiry(); has {
		fmt.Printf("expires %s (%.0f days)\n", expiry.Format(time.RFC3339), time.Until(expiry).Hours()/24)
	} else {
		fmt.Println("perpetual")
	}
	fmt.Printf("features: %s\n", strings.Join(signed.Certificate.Features, ", "))
	return nil
}

func features() error {
	fmt.Println("catalogue:")
	for _, feature := range license.Features() {
		requirements, _ := license.Requirements(feature)
		if len(requirements) == 0 {
			fmt.Printf("  %-16s free\n", feature)
			continue
		}
		scopes := make([]string, 0, len(requirements))
		for _, scope := range requirements {
			scopes = append(scopes, string(scope))
		}
		fmt.Printf("  %-16s requires %s\n", feature, strings.Join(scopes, " + "))
	}
	return nil
}
