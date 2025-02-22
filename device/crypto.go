// Copyright © 2020 Ispirata Srl
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package device

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

// ClearCrypto clears all the temporary crypto files of the Device.
// Usually, you shouldn't need to call this function.
func (d *Device) ClearCrypto() error {
	// Delete all files in the crypto dir
	cryptoDir := d.getCryptoDir()
	dirRead, err := os.Open(cryptoDir)
	if err != nil {
		return err
	}
	dirFiles, err := dirRead.Readdir(0)
	if err != nil {
		return err
	}

	// Loop over the directory's files.
	for index := range dirFiles {
		// Remove the file.
		if err := os.Remove(filepath.Join(cryptoDir, dirFiles[index].Name())); err != nil {
			return err
		}
	}

	return nil
}

func (d *Device) hasValidCertificate() bool {
	// Does the certificate exist?
	_, err := tls.LoadX509KeyPair(filepath.Join(d.getCryptoDir(), "device.crt"),
		filepath.Join(d.getCryptoDir(), "device.key"))
	if err != nil {
		return false
	}

	// In this case, load the certificate (LoadX509KeyPair won't work here)
	r, err := ioutil.ReadFile(filepath.Join(d.getCryptoDir(), "device.crt"))
	if err != nil {
		return false
	}

	block, _ := pem.Decode(r)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		// Didn't work
		return false
	}

	// Return whether it's still valid.
	return time.Now().Before(cert.NotAfter)
}

func (d *Device) getTLSConfig() (*tls.Config, error) {
	// Load Device certificate
	cert, err := tls.LoadX509KeyPair(filepath.Join(d.getCryptoDir(), "device.crt"),
		filepath.Join(d.getCryptoDir(), "device.key"))
	if err != nil {
		return nil, err
	}

	tlsConfig := new(tls.Config)
	tlsConfig.Certificates = []tls.Certificate{cert}
	tlsConfig.RootCAs = d.RootCAs
	tlsConfig.InsecureSkipVerify = d.IgnoreSSLErrors

	return tlsConfig, nil
}

func (d *Device) getCryptoDir() string {
	cryptoDir := filepath.Join(d.persistencyDir, "crypto")
	if err := os.MkdirAll(cryptoDir, 0700); err != nil {
		fmt.Println("WARNING - could not access crypto dir!")
	}
	return cryptoDir
}

func (d *Device) ensureCSR() error {
	if err := d.ensureKeyPair(); err != nil {
		return err
	}

	csrFilename := filepath.Join(d.getCryptoDir(), "device.csr")
	if _, err := os.Stat(csrFilename); err == nil {
		// The file exists, we're fine
		return nil
	}

	// Generate the CSR
	template := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("%s/%s", d.realm, d.deviceID),
			Organization: []string{"Devices"},
		},
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	// Get the private key
	priv, err := ioutil.ReadFile(filepath.Join(d.getCryptoDir(), "device.key"))
	if err != nil {
		return err
	}
	privPem, _ := pem.Decode(priv)
	if privPem == nil {
		return errors.New("Corrupted data in Device Private key, clearing the crypto store")
	}

	var parsedKey interface{}
	if parsedKey, err = x509.ParsePKCS1PrivateKey(privPem.Bytes); err != nil {
		if parsedKey, err = x509.ParsePKCS8PrivateKey(privPem.Bytes); err != nil { // note this returns type `interface{}`
			return err
		}
	}

	privateKey, ok := parsedKey.(*rsa.PrivateKey)
	if !ok {
		return errors.New("Unable to parse RSA private key, clearing the crypto store")
	}

	// Sign
	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, &template, privateKey)
	if err != nil {
		return err
	}
	csrFile, err := os.Create(csrFilename)
	if err != nil {
		return err
	}
	defer csrFile.Close()

	pemBlock := &pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrBytes,
	}

	if err := pem.Encode(csrFile, pemBlock); err != nil {
		return err
	}

	return nil
}

func (d *Device) getCSRString() (string, error) {
	b, err := ioutil.ReadFile(filepath.Join(d.getCryptoDir(), "device.csr"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (d *Device) ensureKeyPair() error {
	keyFile := filepath.Join(d.getCryptoDir(), "device.key")
	if _, err := os.Stat(keyFile); err == nil {
		// The file exists, we're fine
		return nil
	}

	// We need to generate the key
	// First of all, clear the crypto dir, just to be sure.
	if err := d.ClearCrypto(); err != nil {
		return err
	}

	reader := rand.Reader
	// Certificates are short-lived, 2048 is fine.
	bitSize := 2048

	key, err := rsa.GenerateKey(reader, bitSize)
	if err != nil {
		return err
	}

	publicKey := key.PublicKey

	if err := savePublicPEMKey(filepath.Join(d.getCryptoDir(), "device.pub"), publicKey); err != nil {
		return err
	}
	return savePEMKey(keyFile, key)
}

func (d *Device) saveCertificateFromString(certificateString string) error {
	certFile := filepath.Join(d.getCryptoDir(), "device.crt")
	// Attempt loading the certificate to ensure we can use it
	p, _ := pem.Decode([]byte(certificateString))
	if p == nil {
		return errors.New("Could not decode PEM certificate")
	}

	// If it worked, just write the file and call it a day.
	return ioutil.WriteFile(certFile, []byte(certificateString), 0600)
}

func savePEMKey(fileName string, key *rsa.PrivateKey) error {
	outFile, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer outFile.Close()

	var privateKey = &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}

	return pem.Encode(outFile, privateKey)
}

func savePublicPEMKey(fileName string, pubkey rsa.PublicKey) error {
	pkixBytes, err := x509.MarshalPKIXPublicKey(&pubkey)
	if err != nil {
		return err
	}
	var pemkey = &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pkixBytes,
	}

	pemfile, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer pemfile.Close()

	return pem.Encode(pemfile, pemkey)
}
