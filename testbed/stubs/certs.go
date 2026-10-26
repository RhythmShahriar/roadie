// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// stubHosts are the names the host certificate covers; the sinkhole's
// -stub-hosts sends the same names here.
var stubHosts = []string{
	"api.stripe.com", "api.paypal.com", "api-m.paypal.com", "api.twilio.com",
	"hooks.zapier.com", "*.api.mailchimp.com", "*.mailchimp.com",
}

// makeCerts writes ca.pem (public, mounted into the sites), host.pem and
// host.key (0600) to dir. The CA's key is never written: nothing else is
// ever signed with it.
func makeCerts(dir string, hosts []string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Roadie testbed stubs CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(30 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	hostKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: hosts[0]},
		DNSNames:     hosts,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(30 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &hostKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(hostKey)
	if err != nil {
		return err
	}
	for name, block := range map[string]*pem.Block{
		"ca.pem":   {Type: "CERTIFICATE", Bytes: caDER},
		"host.pem": {Type: "CERTIFICATE", Bytes: leafDER},
		"host.key": {Type: "EC PRIVATE KEY", Bytes: keyDER},
	} {
		// ca.pem is read by every site container, whatever its user.
		mode := os.FileMode(0o600)
		if name == "ca.pem" {
			mode = 0o644
		}
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), mode); err != nil {
			return err
		}
	}
	return nil
}
