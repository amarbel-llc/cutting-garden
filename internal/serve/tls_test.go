package serve

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLoadOrCreateTLSCert_PersistsAcrossLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", tlsCertFileName)

	first, err := loadOrCreateTLSCert(path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, err := loadOrCreateTLSCert(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if certFingerprint(first) != certFingerprint(second) {
		t.Fatalf("fingerprint changed across loads: %s != %s",
			certFingerprint(first), certFingerprint(second))
	}
}

// The LocalSend app computes a device's HTTPS-mode fingerprint as the
// SHA-256 of the certificate DER, uppercase hex (see localsend/localsend
// app/lib/util/security_helper.dart and its unit test). Pin that shape.
func TestCertFingerprint_UppercaseHexOfLeafDER(t *testing.T) {
	cert, err := loadOrCreateTLSCert(
		filepath.Join(t.TempDir(), tlsCertFileName))
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	fp := certFingerprint(cert)
	if !regexp.MustCompile(`^[0-9A-F]{64}$`).MatchString(fp) {
		t.Fatalf("fingerprint not 64 uppercase hex chars: %q", fp)
	}

	sum := sha256.Sum256(cert.Certificate[0])
	want := strings.ToUpper(hex.EncodeToString(sum[:]))
	if fp != want {
		t.Fatalf("fingerprint %q, want sha256(leaf DER) %q", fp, want)
	}
}

// Listener-level wiring test: a TLS client must complete a handshake
// against the wrapped listener and read /info over HTTPS, with the
// presented certificate hashing to the advertised fingerprint (the
// property the LocalSend app's favorites pinning relies on). Guards
// against configuring TLS without terminating it (cf. dodder#258).
func TestTLSListener_HandshakeServesInfo(t *testing.T) {
	cert, err := loadOrCreateTLSCert(
		filepath.Join(t.TempDir(), tlsCertFileName))
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	listener := tls.NewListener(inner, tlsServerConfig(cert))

	s := &server{
		info: deviceInfo{
			Alias:       "test",
			Version:     protocolVersion,
			Fingerprint: certFingerprint(cert),
			Protocol:    "https",
		},
		log: func(string, ...any) {},
	}
	httpServer := &http.Server{Handler: s.handler()}
	go func() { _ = httpServer.Serve(listener) }()
	t.Cleanup(func() { _ = httpServer.Close() })

	var presented [][]byte
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			// LocalSend clients skip CA validation and pin the cert
			// hash instead; mirror that.
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				presented = raw
				return nil
			},
		},
	}}

	resp, err := client.Get(
		"https://" + inner.Addr().String() + apiPrefix + "/info")
	if err != nil {
		t.Fatalf("https GET /info: %v", err)
	}
	defer resp.Body.Close()

	var info deviceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if info.Protocol != "https" {
		t.Fatalf("advertised protocol %q, want https", info.Protocol)
	}

	if len(presented) == 0 {
		t.Fatal("no peer certificate captured during handshake")
	}
	sum := sha256.Sum256(presented[0])
	seen := strings.ToUpper(hex.EncodeToString(sum[:]))
	if info.Fingerprint != seen {
		t.Fatalf("advertised fingerprint %q != presented cert hash %q",
			info.Fingerprint, seen)
	}
}
