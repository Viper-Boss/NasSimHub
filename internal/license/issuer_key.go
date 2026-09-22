package license

// The licence issuer's public key, compiled in.
//
// It is a constant rather than configuration, deliberately.
//
// This key is the trust root of the whole licensing scheme: a certificate is
// honoured if and only if it verifies against this key. Whoever chooses the
// value chooses who may issue licences.
//
// It used to be read from MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY, which handed
// that choice to the operator of each gateway -- that is, to the customer. The
// bypass took three commands and no tampering with any binary:
//
//	licensegen keygen                          # their own key pair
//	echo MODEMDECK_LICENSE_ISSUER_PUBLIC_KEY=<their public key> >> .env
//	licensegen issue -scope machine -features ... -days 3650
//
// Import that certificate and every paid feature unlocks, permanently, because
// the gateway was verifying against a key the customer controls. cmd/licensegen
// signs certificates as well as generating keys, so nothing had to be written
// for this; the tool ships in the repository.
//
// Compiling the key in does not make the scheme unbreakable -- someone can
// still patch the binary -- but it moves the attack from "edit one line of a
// text file that the deployment documentation tells you to edit" to "modify a
// compiled image", which is a different population of people entirely.
//
// A build-time constant is also the honest shape for this value. There is
// exactly one issuer key, it is the same on every gateway ever shipped, and
// changing it invalidates every certificate ever issued (see
// issuer.GenerateKey, which refuses to overwrite a key file for that reason).
// Configuration is for things that legitimately differ per deployment. This
// differs per PRODUCT, and there is one product.
//
// ROTATION, if it ever has to happen: change the line below, rebuild, and
// re-issue every outstanding certificate from the console's archive. There is
// no shortcut, and there was no shortcut when this was an environment variable
// either -- the old certificates were signed by the old private key and no
// amount of configuration makes them verify again.
//
// This is the PUBLIC half. Publishing it costs nothing: it verifies signatures
// and cannot produce them. The private half never leaves the issuing console,
// and nothing in this repository can sign with it.
const IssuerPublicKey = "uBOkirZI98qbfdWw2u8v4KY1OPLoh6Uo9Pwb5Yt5NS8="
