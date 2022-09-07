package ovirt

import (
	"io"
	"os"
	"strings"

	"k8s.io/klog"
)

//TODO: remove CAFILE and use CABundle on CreateAPIConnection

func writeCA(creds *Credentials) error {
	// Write CA bundle to a file if exist. Its best if we could mount the secret into a file,
	// but this controller deployment cannot
	if creds.CABundle != "" {
		caFilePath, err := writeCAToTempFile(strings.NewReader(creds.CABundle))
		if err != nil {
			klog.Errorf("failed to extract and store the CA %s", err)
			return err
		}
		creds.CAFile = caFilePath
	}
	return nil
}

// TODO: remove after we port to CABundle
func writeCAToTempFile(source io.Reader) (string, error) {
	f, err := os.CreateTemp("", "ovirt-ca-bundle")
	if err != nil {
		return "", err
	}
	defer f.Close()
	content, err := io.ReadAll(source)
	if err != nil {
		return "", err
	}
	_, err = f.Write(content)
	if err != nil {
		return "", err
	}
	return f.Name(), nil
}
