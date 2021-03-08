package delve

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

//returns key, cert
func GenerateSelfCaSignedTLSCertFiles(namespace string) ([]byte, []byte, error) {
	caData := NewCertData(namespace, true)
	tlsData := NewCertData(namespace, false)
	caKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	tlsKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	return GenerateCertPEMFiles(tlsData, tlsKey, caData, caKey)
}

func NewCertData(namespace string, isCa bool) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(420),
		Subject: pkix.Name{
			CommonName:    "skavo-webhook." + namespace + ".svc",
			Organization:  []string{"Skavo"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"Utah"},
			StreetAddress: []string{"Salt Lake City"},
			PostalCode:    []string{"84123"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  isCa,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
}

//returns key, cert
func GenerateCertPEMFiles(certData *x509.Certificate, certKey *rsa.PrivateKey, caCert *x509.Certificate, caKey *rsa.PrivateKey) ([]byte, []byte, error) {
	var certificate []byte
	certificate, err := x509.CreateCertificate(rand.Reader, certData, caCert, &certKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certData: %+v", err)
	}

	// pem encode
	pemCert := new(bytes.Buffer)
	err = pem.Encode(pemCert, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certificate,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode certData: %+v", err)
	}

	pemKey := new(bytes.Buffer)
	err = pem.Encode(pemKey, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certKey),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode key: %+v", err)
	}
	return pemKey.Bytes(), pemCert.Bytes(), nil
}
