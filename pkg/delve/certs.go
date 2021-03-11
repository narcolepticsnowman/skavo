package delve

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

func GenerateKeyAndCert(namespace string, isCa bool) (*rsa.PrivateKey, *x509.Certificate, error) {
	cert := NewCertData(namespace, true)
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}

func NewCertData(namespace string, isCa bool) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(420),
		Subject: pkix.Name{
			CommonName:    skavoWebhookName + "." + namespace + ".svc",
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

func GenerateKey() *rsa.PrivateKey {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Errorf("couldn't create private key: %v", err))
	}
	return privateKey
}

func PrivateKeyPem(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func CreateCSRPem(namespace string, serviceName string, privateKey *rsa.PrivateKey) []byte {
	extensionValue, err := asn1.Marshal(BasicConstraints{false, 0})
	if err != nil {
		panic(fmt.Errorf("failed to marshal basic constraints: %+v", err))
	}
	request := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: fmt.Sprintf("%s.%s.svc", serviceName, namespace),
		},
		DNSNames: []string{
			serviceName,
			fmt.Sprintf("%s.%s", serviceName, namespace),
			fmt.Sprintf("%s.%s.svc", serviceName, namespace),
		},
		Extensions: []pkix.Extension{
			{
				Id:       asn1.ObjectIdentifier{2, 5, 29, 19},
				Value:    extensionValue,
				Critical: true,
			},
		},
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &request, privateKey)
	if err != nil {
		panic(fmt.Errorf("failed to create CSR: %+v", err))
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})
}

// BasicConstraints CSR information RFC 5280, 4.2.1.9
type BasicConstraints struct {
	IsCA       bool `asn1:"optional"`
	MaxPathLen int  `asn1:"optional,default:-1"`
}
