//go:build !windows

package linktest

import "errors"

func junction(link, target string) ([]byte, error) {
	return nil, errors.New("junctions exist on Windows only")
}

func shortName(path string) (string, error) {
	return "", errors.New("8.3 short names exist on Windows only")
}
