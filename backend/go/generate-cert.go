package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"time"
)

func main() {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "localhost",
			Organization: []string{"FAIRPASS Development"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().AddDate(1, 0, 0),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
		DNSNames: []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(
		rand.Reader, &template, &template,
		&privateKey.PublicKey, privateKey,
	)
	check(err)

	certFile, err := os.Create("certs/server-cert.pem")
	check(err)
	defer certFile.Close()

	check(pem.Encode(certFile, &pem.Block{
		Type: "CERTIFICATE",
		Bytes: certDER,
	}))

	keyFile, err := os.Create("certs/server-key.pem")
	check(err)
	defer keyFile.Close()

	keyBytes, err := x509.MarshalECPrivateKey(privateKey)
	check(err)

	check(pem.Encode(keyFile, &pem.Block{
		Type: "EC PRIVATE KEY",
		Bytes: keyBytes,
	}))

	println("FAIRPASS HTTPS certificates generated!")
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}