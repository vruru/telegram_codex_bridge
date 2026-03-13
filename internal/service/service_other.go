//go:build !darwin && !linux

package service

import "fmt"

func newManager(projectRoot string) (Manager, error) {
	_ = projectRoot
	return nil, fmt.Errorf("service management is not supported on this platform")
}
