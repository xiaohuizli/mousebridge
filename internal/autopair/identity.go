package autopair

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"unicode/utf8"
)

func tlsLeaf(cert tls.Certificate) (string, error) {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:]), nil
}
func peerID(state tls.ConnectionState) (string, error) {
	if len(state.PeerCertificates) != 1 {
		return "", errors.New("expected one auto identity certificate")
	}
	sum := sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:]), nil
}
func lastRune(value string) (rune, int) { return utf8.DecodeLastRuneInString(value) }
