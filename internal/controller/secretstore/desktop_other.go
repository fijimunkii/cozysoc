//go:build !darwin

package secretstore

import "fmt"

func NewDesktop() (Store, error) {
	return nil, fmt.Errorf("%w: desktop secret storage is not selected for this platform", ErrUnsupported)
}
